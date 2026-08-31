package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// BriefingProfile selects which prompt block a Briefing renders. The two
// profiles differ in section set, headers, footer, and character cap — the
// deep-review specialist block vs the single-pass reviewer block.
type BriefingProfile int

const (
	// ProfileSpecialist renders the deep-review specialist block: synthesis,
	// combined repo+shared patterns, dismissed false positives, confirmed findings.
	ProfileSpecialist BriefingProfile = iota
	// ProfileReview renders the single-pass reviewer block, which additionally
	// pulls org rules + past-review context that specialists intentionally skip.
	ProfileReview
)

// BriefingOptions tunes a Briefing assembly + render. CharCap is per-call-site
// (specialist 2400, review 3200) so each prompt keeps the exact byte budget it
// had before this seam moved into the module. EmphasizeFalsePositives splits
// dismissed-finding feedback into its own call-out (specialist path).
type BriefingOptions struct {
	Profile                 BriefingProfile
	Thresholds              Thresholds
	CharCap                 int
	EmphasizeFalsePositives bool
}

// Briefing is the typed, per-section memory block assembled for a review
// prompt. Every section carries already-truncated prose (each item ≤500 chars);
// the render methods add headers, numbering, the trailing instruction, and the
// per-profile character cap. Splitting retrieval (assembleBriefing) from
// rendering keeps the byte-sensitive markdown pure and unit-testable.
type Briefing struct {
	// Synthesis is the file-scoped review-history prose (≤500 chars). Empty if
	// no synthesis doc matched.
	Synthesis string
	// Patterns is repo-scoped patterns/scenarios (non-feedback) followed by
	// shared org patterns, in that order, each ≤500 chars.
	Patterns []string
	// Disputed conventions are excluded from ordinary live retrieval and rendered
	// separately so neither side is enforced while the team decides.
	Disputed []string
	// FalsePositives is type=feedback action=dismissed content.
	FalsePositives []string
	// Reinforced is confirmed-finding feedback that raises the priority of recurrences.
	Reinforced []string
	// Rules is org-wide review rules (ProfileReview only).
	Rules []string
	// PastReviews is prior review findings on this repo (ProfileReview only).
	PastReviews []string
}

// BriefingQuery bundles the inputs for a Briefing assembly + render: the repo
// coordinates, the file under review, the semantic query driving the
// repo/shared/past-review reads, and the profile/threshold/cap options.
type BriefingQuery struct {
	Owner    string
	Repo     string
	FilePath string
	Query    string
	Options  BriefingOptions
	Disputed []ConventionConflict
}

// briefingWith is the shared Briefing pipeline over a transport core:
// assemble (shared orchestration, shared floors) then render (already pure).
// The empty-repo no-op lives here, not in the adapters — it is orchestration
// policy (every leg is repo-scoped), and duplicating it per backend is the
// drift class this seam exists to remove. Adapters keep only their own
// disabled-state check.
func briefingWith(ctx context.Context, run runSearchFn, logger *slog.Logger, q BriefingQuery) (rendered string, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	operationID := obs.NewLogID()
	started := time.Now()
	logger.InfoContext(ctx, "memory briefing started", "operation_id", operationID,
		"repo", q.Repo, "file", q.FilePath, "profile", q.Options.Profile,
		"query_len", len(q.Query), "char_cap", q.Options.CharCap,
		"emphasize_false_positives", q.Options.EmphasizeFalsePositives)
	defer func() {
		attrs := []any{"operation_id", operationID, "repo", q.Repo, "file", q.FilePath,
			"profile", q.Options.Profile, "rendered_chars", len(rendered),
			"duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			logger.WarnContext(ctx, "memory briefing failed", attrs...)
		} else {
			logger.InfoContext(ctx, "memory briefing completed", attrs...)
		}
	}()
	if q.Repo == "" {
		logger.InfoContext(ctx, "memory briefing skipped", "operation_id", operationID, "reason", "empty_repo")
		return "", nil
	}
	b, err := assembleBriefingWith(ctx, run, logger, q)
	if err != nil {
		return "", err
	}
	for _, conflict := range q.Disputed {
		b.Disputed = append(b.Disputed, fmt.Sprintf("team is split on %s vs %s — do not enforce either side", conflict.LeftContent, conflict.RightContent))
	}
	logger.DebugContext(ctx, "memory briefing sections assembled", "operation_id", operationID,
		"has_synthesis", b.Synthesis != "", "pattern_count", len(b.Patterns),
		"false_positive_count", len(b.FalsePositives), "reinforced_count", len(b.Reinforced),
		"rule_count", len(b.Rules), "past_review_count", len(b.PastReviews))
	if q.Options.Profile == ProfileReview {
		rendered = b.renderReview(q.Options.CharCap)
		return rendered, nil
	}
	rendered = b.renderSpecialist(q.FilePath, q.Options.CharCap, q.Options.EmphasizeFalsePositives)
	return rendered, nil
}

// assembleBriefing runs the typed reads and dispatches results into sections.
// The specialistBlock legs (synthesis + repo + shared) serve both profiles; the
// review profile adds the rules + past-review side-searches specialists skip.
// All per-item content is truncated to 500 chars here so the render stays pure.
// Any leg error is returned so Briefing degrades the whole block rather than
// serving a partial one.
func assembleBriefingWith(ctx context.Context, run runSearchFn, logger *slog.Logger, q BriefingQuery) (Briefing, error) {
	if logger == nil {
		logger = slog.Default()
	}
	operationID := obs.NewLogID()
	started := time.Now()
	logger.InfoContext(ctx, "memory briefing assembly started", "operation_id", operationID,
		"repo", q.Repo, "file", q.FilePath, "profile", q.Options.Profile, "query_len", len(q.Query))
	// Normalize once so EVERY leg reads resolved floors — the specialistBlock
	// legs AND the review-profile rules / past-review side-searches below. A
	// retried/resumed run delivers a zero Thresholds (PipelineRun.Thresholds is
	// json:"-" and buildRetryRun re-derives neither), which would otherwise floor
	// the side-searches at 0 and flood the briefing with low-similarity noise.
	q.Options.Thresholds = q.Options.Thresholds.WithDefaults()
	if q.Options.Profile != ProfileReview {
		// Specialist profile has no side-searches — one specialistBlock (own 5s).
		block, err := specialistBlockWith(ctx, run, logger, q.Repo, q.FilePath, q.Query, q.Options.Thresholds)
		if err != nil {
			logger.WarnContext(ctx, "memory briefing assembly failed", "operation_id", operationID,
				"strategy", "specialist_only", "duration_ms", time.Since(started).Milliseconds(), "error", err)
			return Briefing{}, err
		}
		b := briefingSections(block)
		logger.InfoContext(ctx, "memory briefing assembly completed", "operation_id", operationID,
			"strategy", "specialist_only", "pattern_count", len(b.Patterns),
			"duration_ms", time.Since(started).Milliseconds())
		return b, nil
	}

	// Review profile: the three legs — specialistBlock (synthesis/repo/shared),
	// the rules side-search, and the past-review side-search — are mutually
	// independent, so run ALL THREE concurrently. Each owns its own 5s timeout,
	// so the worst-case ceiling is ~5s, not specialistBlock(~5s) THEN
	// side-searches(~5s) in series (~10s).
	//
	// Write-partitioned: each goroutine writes a distinct variable; wg.Wait() is
	// the happens-before edge before they are read.
	var block MemoryBlock
	var rules, pastReviews []string
	var blockErr, rulesErr, pastErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		block, blockErr = specialistBlockWith(ctx, run, logger, q.Repo, q.FilePath, q.Query, q.Options.Thresholds)
	}()
	go func() {
		defer wg.Done()
		var m []PatternMatch
		m, rulesErr = searchWith(ctx, run, MemoryQuery{
			Query: "review rules conventions", Scope: ScopeShared, Type: TypeRule,
			Limit: 3, Threshold: q.Options.Thresholds.FindingEnrich, Enrich: true,
		})
		rules = HintStrings(m)
	}()
	go func() {
		defer wg.Done()
		var m []PatternMatch
		m, pastErr = searchWith(ctx, run, MemoryQuery{
			Query: q.Query, Repo: q.Repo, Scope: ScopeRepo, Type: TypeReview,
			Limit: 2, Threshold: q.Options.Thresholds.FindingEnrich, Enrich: true,
		})
		pastReviews = HintStrings(m)
	}()
	wg.Wait()
	// Per-leg degradation: the specialist block (file history + patterns) is
	// the CORE — if it failed there is nothing usable and the error propagates.
	// The rules and past-review side-searches are OPTIONAL: a failed leg is
	// Warn-logged and its section omitted, so a transient single-leg error
	// never blanks the whole briefing (the #147 gate's resilience finding).
	logger.DebugContext(ctx, "memory briefing retrieval legs completed", "operation_id", operationID,
		"specialist_repo_count", len(block.Repo), "specialist_shared_count", len(block.Shared),
		"rule_count", len(rules), "past_review_count", len(pastReviews),
		"specialist_error", blockErr, "rules_error", rulesErr, "past_reviews_error", pastErr)
	if blockErr != nil {
		logger.WarnContext(ctx, "memory briefing assembly failed", "operation_id", operationID,
			"strategy", "review_parallel", "duration_ms", time.Since(started).Milliseconds(), "error", blockErr)
		return Briefing{}, blockErr
	}
	if rulesErr != nil {
		logDegrade(logger, "briefing.rules", SharedTag, len(q.Query), rulesErr)
		rules = nil
	}
	if pastErr != nil {
		logDegrade(logger, "briefing.past_reviews", RepoTagNew(q.Repo), len(q.Query), pastErr)
		pastReviews = nil
	}

	b := briefingSections(block)
	b.Rules = rules
	b.PastReviews = pastReviews
	logger.InfoContext(ctx, "memory briefing assembly completed", "operation_id", operationID,
		"strategy", "review_parallel", "has_synthesis", b.Synthesis != "", "pattern_count", len(b.Patterns),
		"false_positive_count", len(b.FalsePositives), "reinforced_count", len(b.Reinforced),
		"rule_count", len(b.Rules), "past_review_count", len(b.PastReviews),
		"duration_ms", time.Since(started).Milliseconds())
	return b, nil
}

// briefingSections splits a MemoryBlock into the typed prose sections shared by
// both render profiles, truncating each item to 500 chars. Feedback routes by
// explicit action: dismissed suppresses, confirmed reinforces, and ignored or
// action-less legacy rows stay neutral. Everything else lands in Patterns.
func briefingSections(block MemoryBlock) Briefing {
	var b Briefing
	if block.Synthesis != "" {
		b.Synthesis = util.Truncate(block.Synthesis, 500, true)
	}
	for _, m := range block.Repo {
		content := util.Truncate(m.Content, 500, true)
		if m.Metadata["type"] == string(TypeFeedback) {
			switch m.Metadata["action"] {
			case "dismissed":
				b.FalsePositives = append(b.FalsePositives, content)
			case "confirmed":
				b.Reinforced = append(b.Reinforced, content)
			}
			continue
		}
		b.Patterns = append(b.Patterns, content)
	}
	for _, m := range block.Shared {
		b.Patterns = append(b.Patterns, util.Truncate(m.Content, 500, true))
	}
	return b
}

// renderSpecialist renders the deep-review specialist block. charCap bounds the
// body BEFORE the trailing instruction is appended (so the final string may
// exceed charCap by the footer length) — preserving the pre-seam behavior.
func (b Briefing) renderSpecialist(filePath string, charCap int, emphasizeFalsePositives bool) string {
	var sb strings.Builder

	if b.Synthesis != "" {
		sb.WriteString("\n\n## Memory Briefing: " + filePath + "\n\n")
		sb.WriteString("### File History\n")
		sb.WriteString(b.Synthesis + "\n")
	}

	if len(b.Disputed) > 0 {
		sb.WriteString("\n\n## Disputed — do not enforce\n")
		for _, d := range b.Disputed {
			sb.WriteString("- " + d + "\n")
		}
	}

	if len(b.Patterns) > 0 {
		if b.Synthesis == "" {
			sb.WriteString("\n\n## Repo Memory (patterns from past reviews)\n\n")
		} else {
			sb.WriteString("\n### Repo Patterns\n")
		}
		for i, m := range b.Patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if emphasizeFalsePositives && len(b.FalsePositives) > 0 {
		sb.WriteString("\n## Known False Positives (DO NOT re-flag these patterns)\n")
		for i, m := range b.FalsePositives {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if len(b.Reinforced) > 0 {
		sb.WriteString("\n## Confirmed Findings (flag recurrences)\n")
		for i, m := range b.Reinforced {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m))
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	footer := "\nUse this context to inform your review — issues matching known patterns are higher priority.\nWhen a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references."

	result := sb.String()
	if len(result) > charCap {
		result = util.Truncate(result, charCap, true)
	}
	return result + footer
}

// renderReview renders the single-pass reviewer block. charCap bounds the whole
// string INCLUDING the trailing instruction — matching the pre-seam behavior,
// which differs subtly from the specialist path (cap-before-footer).
func (b Briefing) renderReview(charCap int) string {
	var sb strings.Builder

	if b.Synthesis != "" {
		sb.WriteString("\n\n## File History\n")
		sb.WriteString("- " + b.Synthesis + "\n")
	}

	if len(b.Rules) > 0 {
		sb.WriteString("\n## Review Rules\n")
		for i, r := range b.Rules {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(b.Disputed) > 0 {
		sb.WriteString("\n## Disputed — do not enforce\n")
		for _, d := range b.Disputed {
			sb.WriteString("- " + d + "\n")
		}
	}

	if len(b.Patterns) > 0 {
		sb.WriteString("\n## Established Patterns\n")
		for i, r := range b.Patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(b.PastReviews) > 0 {
		sb.WriteString("\n## Past Review Findings (avoid re-raising the same issue)\n")
		for i, r := range b.PastReviews {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if blk := numberedBlock("\n## Known False Positives (DO NOT re-flag these patterns)\n", b.FalsePositives); blk != "" {
		sb.WriteString(blk)
	}

	if blk := numberedBlock("\n## Confirmed Findings (flag recurrences)\n", b.Reinforced); blk != "" {
		sb.WriteString(blk)
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nApply these patterns and past findings when reviewing. When a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references.\n")

	result := sb.String()
	if len(result) > charCap {
		result = util.Truncate(result, charCap, true)
	}
	return result
}

// numberedBlock renders header + a 1-indexed numbered list of items, or "" when
// items is empty. Mirrors the pipeline's formatMemoryBlock with an empty footer.
func numberedBlock(header string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(header)
	for i, r := range items {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	return sb.String()
}
