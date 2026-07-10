package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// mappedDoc is a single Postgres row rendered into the Supermemory shape: the
// destination container tag plus the batch document (content + deterministic
// customID + typed metadata). Mappers are PURE — no I/O — so every customID /
// metadata pairing is table-testable against the pipeline's own writers.
type mappedDoc struct {
	Container string
	Doc       memory.BatchDocument
}

// nullStr dereferences a *string to "" when nil.
func nullStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// intOrZero dereferences a *int to 0 when nil (0 = "absent" for metadata ints).
func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// defaultSource mirrors IndexRepoPattern/IndexOwnerPattern: an empty source
// falls back to "pattern" for the default customID derivation.
func defaultSource(s string) string {
	if s == "" {
		return "pattern"
	}
	return s
}

// splitFullName splits an "owner/repo" full_name with the SAME semantics as
// pipeline.splitRepoFullName (SplitN "/" into 2, both parts non-empty). A
// malformed name is a permanent per-row failure so the caller skips it without
// tripping the circuit breaker.
func splitFullName(fullName string) (owner, repo string, err error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo full_name %q: %w", fullName, errPermanent)
	}
	return parts[0], parts[1], nil
}

// repoToken returns the repo segment (part after "owner/") that RepoTagNew and
// every repo-scoped customID builder derive from — identical to the pipeline's
// `repo` token.
func repoToken(fullName string) (string, error) {
	_, repo, err := splitFullName(fullName)
	return repo, err
}

// mapReviewComment renders a review_comments row into a type=review doc in the
// {repo} container (mirrors IndexReviewCommentsBatch). content is the stored
// body (DiffContext is not persisted, so buildReviewContent's Context suffix is
// absent). The customID hashes the stored body; the pipeline hashes the raw
// finding body, which Postgres does not retain — see migrate.sql (a).
func mapReviewComment(row db.ListReviewCommentsForBackfillRow) (mappedDoc, error) {
	repo, err := repoToken(row.FullName)
	if err != nil {
		return mappedDoc{}, err
	}
	category := nullStr(row.Category)
	md, err := memory.Metadata{
		Type:     memory.TypeReview,
		FilePath: row.FilePath,
		Severity: nullStr(row.Severity),
		Category: category,
		PRNumber: row.PRNumber,
		Extra:    map[string]string{"review_id": row.ReviewID.String()},
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.RepoTagNew(repo),
		Doc: memory.BatchDocument{
			Content:  row.Body,
			CustomID: memory.FindingFingerprint("", repo, row.FilePath, category, row.Body),
			Metadata: md,
		},
	}, nil
}

// mapPattern renders a patterns row into a type=pattern doc. NULL repo_id →
// _shared (IndexOwnerPattern semantics, pinned confidence=1.00); otherwise
// {repo} (IndexRepoPattern). The customID is reconstructed via
// PipelinePatternCustomID; unknown sources fall back to the indexer's own
// default derivation.
func mapPattern(row db.ListPatternsForBackfillRow) (mappedDoc, error) {
	source := row.Source
	md := memory.Metadata{
		Type:     memory.TypePattern,
		Source:   source,
		Category: nullStr(row.Category),
		PRNumber: intOrZero(row.PRNumber),
	}
	// Mirror patternMetadataFromLegacy's source-based re-typing. These sources
	// are not persisted to the patterns table today (no CreatePattern writes
	// them), so the branches are defensive.
	switch source {
	case "synthesis":
		md.Type = memory.TypeSynthesis
	case "pr_summary":
		md.Type = memory.TypePRSummary
	case "arch_summary":
		md.Type = memory.TypeTopology
	}

	var container, customID string
	if row.RepoID == nil {
		container = memory.SharedTag
		customID = memory.PipelinePatternCustomID("", source, row.Content, row.Category, true)
		if customID == "" {
			customID = memory.SharedPatternCustomID(defaultSource(source), row.Content)
		}
		// IndexOwnerPattern resets confidence to 1.00 on every _shared write.
		md.Extra = map[string]string{"confidence": "1.00"}
	} else {
		repo, err := repoToken(nullStr(row.FullName))
		if err != nil {
			return mappedDoc{}, err
		}
		container = memory.RepoTagNew(repo)
		customID = memory.PipelinePatternCustomID(repo, source, row.Content, row.Category, false)
		if customID == "" {
			customID = memory.PatternCustomID("", repo, defaultSource(source), row.Content)
		}
	}
	flat, err := md.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: container,
		Doc:       memory.BatchDocument{Content: row.Content, CustomID: customID, Metadata: flat},
	}, nil
}

// mapFeedback renders a comment_outcomes row into a type=feedback doc in the
// {repo} container (mirrors IndexFeedbackSignal). action ∈ {confirmed,
// dismissed}. content is body-only: the live pipeline's reply-origin feedback
// appended a developer-reply suffix, but that reply text was never persisted to
// Postgres (CollectReplyTrace has zero callers), so it's an SM-only data
// category and body-only is the accepted re-derivation. The customID hashes the
// stored comment body + action (reply text is not part of it), so confirmed and
// dismissed coexist and the customID is exact.
func mapFeedback(row db.ListCommentOutcomesForBackfillRow) (mappedDoc, error) {
	repo, err := repoToken(row.FullName)
	if err != nil {
		return mappedDoc{}, err
	}
	var polarity memory.Polarity
	switch row.Outcome {
	case "confirmed":
		polarity = memory.PolarityPositive
	case "dismissed":
		polarity = memory.PolarityNegative
	default:
		return mappedDoc{}, fmt.Errorf("unsupported outcome %q: %w", row.Outcome, errPermanent)
	}
	category := nullStr(row.Category)
	md, err := memory.Metadata{
		Type:     memory.TypeFeedback,
		FilePath: row.FilePath,
		Category: category,
		Polarity: polarity,
		Action:   row.Outcome,
		PRNumber: row.PRNumber,
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.RepoTagNew(repo),
		Doc: memory.BatchDocument{
			Content:  row.Body,
			CustomID: memory.FeedbackCustomID("", repo, row.FilePath, category, row.Body, row.Outcome),
			Metadata: md,
		},
	}, nil
}

// mapScenario renders a scenarios row into a type=scenario doc (mirrors
// IndexScenario): description + "Related files:" suffix, scenario_id metadata,
// deterministic {repo}--scenario--{id} customID.
func mapScenario(row db.ListScenariosForBackfillRow) (mappedDoc, error) {
	repo, err := repoToken(row.FullName)
	if err != nil {
		return mappedDoc{}, err
	}
	content := row.Description
	if len(row.Files) > 0 {
		content += "\n\nRelated files: " + strings.Join(row.Files, ", ")
	}
	md, err := memory.Metadata{
		Type:       memory.TypeScenario,
		ScenarioID: row.ID,
		Severity:   nullStr(row.Severity),
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.RepoTagNew(repo),
		Doc: memory.BatchDocument{
			Content:  content,
			CustomID: memory.ScenarioCustomID(repo, row.ID),
			Metadata: md,
		},
	}, nil
}

// mapTrace renders a decision_traces row into a type=trace doc (mirrors
// IndexDecisionTrace): trace_type in subtype metadata, content-hash customID.
func mapTrace(row db.ListTracesForBackfillRow) (mappedDoc, error) {
	repo, err := repoToken(row.FullName)
	if err != nil {
		return mappedDoc{}, err
	}
	md, err := memory.Metadata{
		Type:     memory.TypeTrace,
		Subtype:  row.TraceType,
		FilePath: row.FilePath,
		Severity: row.Severity,
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.RepoTagNew(repo),
		Doc: memory.BatchDocument{
			Content:  row.Content,
			CustomID: memory.TraceCustomID(repo, row.FilePath, row.TraceType, row.Content),
			Metadata: md,
		},
	}, nil
}

// mapRule renders a rules row into a type=rule doc in _shared (mirrors
// IndexRule): rule_id + priority in Extra, RuleCustomID by DB id.
func mapRule(row db.ListRulesForBackfillRow) (mappedDoc, error) {
	md, err := memory.Metadata{
		Type:     memory.TypeRule,
		Category: row.Category,
		Extra: map[string]string{
			"rule_id":  strconv.FormatInt(row.ID, 10),
			"priority": strconv.Itoa(row.Priority),
		},
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.SharedTag,
		Doc: memory.BatchDocument{
			Content:  row.Content,
			CustomID: memory.RuleCustomID(row.ID),
			Metadata: md,
		},
	}, nil
}

// mapReviewSummary renders a reviews (summary present) row into a type=pr_summary
// doc (mirrors indexPRSummary): the pipeline's summary content is rebuilt from
// the review columns, and the customID = PRSummaryCustomID(repo, pr_number) so
// it collides exactly with the pipeline's own write.
func mapReviewSummary(row db.ListReviewSummariesForBackfillRow) (mappedDoc, error) {
	repo, err := repoToken(row.FullName)
	if err != nil {
		return mappedDoc{}, err
	}
	content := fmt.Sprintf("PR #%d \"%s\" by %s\nScore: %d/10\nFiles: %s\n\n%s",
		row.PRNumber, row.PRTitle, row.PRAuthor, intOrZero(row.Score), row.Files,
		util.Truncate(row.Summary, 800, false))
	md, err := memory.Metadata{
		Type:     memory.TypePRSummary,
		Source:   "pr_summary",
		PRNumber: row.PRNumber,
		PRAuthor: row.PRAuthor,
	}.ToMap()
	if err != nil {
		return mappedDoc{}, err
	}
	return mappedDoc{
		Container: memory.RepoTagNew(repo),
		Doc: memory.BatchDocument{
			Content:  content,
			CustomID: memory.PRSummaryCustomID("", repo, row.PRNumber),
			Metadata: md,
		},
	}, nil
}
