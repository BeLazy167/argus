package memory

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// Doc is the backend-neutral memory document: exactly the bytes both stores
// persist. One builder per writer produces it, and the
// Postgres indexers are transport adapters over the result — byte-identity
// between backends holds by construction, so the dual-write shadow
// comparison (program PR 6) measures retrieval, not formatting.
type Doc struct {
	ContainerTag string
	CustomID     string
	// Type duplicates Metadata["type"]: the PG store persists it as a
	// filterable column and from the metadata map.
	Type     string
	Content  string
	Metadata map[string]string
}

// buildReviewDocs shapes a review-comment batch. Comments whose metadata
// fails validation are skipped (Warn) and counted; the caller decides how an
// all-dropped batch surfaces.
func buildReviewDocs(owner, repo string, comments []ReviewMemory, logger *slog.Logger) (docs []Doc, skipped int) {
	docs = make([]Doc, 0, len(comments))
	for _, c := range comments {
		meta, err := Metadata{
			Type:     TypeReview,
			FilePath: c.FilePath,
			Severity: c.Severity,
			Category: c.Category,
			PRNumber: c.PRNumber,
			Extra:    map[string]string{"review_id": c.ReviewID},
		}.ToMap()
		if err != nil {
			logger.Warn("skipping review comment with invalid metadata", "error", err, "file", c.FilePath)
			skipped++
			continue
		}
		docs = append(docs, Doc{
			ContainerTag: RepoTagNew(repo),
			CustomID:     FindingFingerprint(owner, repo, c.FilePath, c.Category, c.Body),
			Type:         string(TypeReview),
			Content:      buildReviewContent(c),
			Metadata:     meta,
		})
	}
	return docs, skipped
}

// buildRuleDoc shapes an owner-scoped rule: `_shared` container, type=rule,
// rule_id/priority provenance in Extra.
func buildRuleDoc(rule RuleMemory) (Doc, error) {
	meta, err := Metadata{
		Type:     TypeRule,
		Category: rule.Category,
		Extra:    map[string]string{"rule_id": strconv.FormatInt(rule.RuleID, 10), "priority": strconv.Itoa(rule.Priority)},
	}.ToMap()
	if err != nil {
		return Doc{}, fmt.Errorf("rule metadata: %w", err)
	}
	return Doc{
		ContainerTag: SharedTag,
		CustomID:     RuleCustomID(rule.RuleID),
		Type:         string(TypeRule),
		Content:      rule.Content,
		Metadata:     meta,
	}, nil
}

// buildPatternDoc derives the deterministic customId when p.CustomID is empty
// and mirrors it into metadata["custom_id"] so search hits resolve back to
// their patterns row (search results carry metadata but no
// top-level customId, and the result's own ID may be a chunk id).
func buildPatternDoc(repo string, p PatternMemory) (Doc, error) {
	if p.CustomID == "" {
		p.CustomID = PatternCustomID("", repo, patternSource(p), p.Content)
	}
	m := p.metadata()
	flat, err := m.ToMap()
	if err != nil {
		return Doc{}, fmt.Errorf("pattern metadata: %w", err)
	}
	flat["custom_id"] = p.CustomID
	return Doc{
		ContainerTag: RepoTagNew(repo),
		CustomID:     p.CustomID,
		Type:         string(m.Type),
		Content:      p.Content,
		Metadata:     flat,
	}, nil
}

// buildSharedPatternDoc pins confidence=1.00 on every write — successful
// re-learning is the liveness signal shared-pattern decay keys off. Extra is
// copied before the pin so the caller's map is never mutated.
func buildSharedPatternDoc(p PatternMemory) (Doc, error) {
	if p.CustomID == "" {
		p.CustomID = SharedPatternCustomID(patternSource(p), p.Content)
	}
	m := p.metadata()
	extra := make(map[string]string, len(m.Extra)+1)
	for k, v := range m.Extra {
		extra[k] = v
	}
	extra["confidence"] = "1.00"
	m.Extra = extra
	flat, err := m.ToMap()
	if err != nil {
		return Doc{}, fmt.Errorf("shared pattern metadata: %w", err)
	}
	flat["custom_id"] = p.CustomID
	return Doc{
		ContainerTag: SharedTag,
		CustomID:     p.CustomID,
		Type:         string(m.Type),
		Content:      p.Content,
		Metadata:     flat,
	}, nil
}

// buildFeedbackDoc derives polarity/content via feedbackShape and applies the
// dismissal-specific keying (category + semantic content, file-path-free)
// with change-kind/reason provenance. Unrecognized actions error — the valid
// set is small and stable, so anything else is a caller bug that must
// surface, not silently drop.
func buildFeedbackDoc(owner, repo string, fb FeedbackMemory) (Doc, error) {
	polarity, content, ok := feedbackShape(fb)
	if !ok {
		return Doc{}, fmt.Errorf("indexing feedback signal: unsupported action %q (want confirmed|dismissed|ignored)", fb.Action)
	}
	m := Metadata{
		Type:     TypeFeedback,
		FilePath: fb.FilePath,
		Category: fb.Category,
		Polarity: polarity,
		Action:   fb.Action,
		PRNumber: fb.PRNumber,
		Source:   fb.Source,
	}
	customID := feedbackCustomIDForSource(owner, repo, fb.FilePath, fb.Category, fb.OriginalBody, fb.Action, fb.Source)
	if fb.Action == "dismissed" {
		customID = dismissalCustomIDForSource(repo, fb.Category, fb.OriginalBody, fb.Source)
		extra := map[string]string{"repo": repo}
		if fb.ChangeKind != "" {
			extra["change_kind"] = fb.ChangeKind
		}
		if fb.Reason != "" {
			extra["reason"] = util.Truncate(fb.Reason, 300, false)
		}
		m.Extra = extra
	}
	meta, err := m.ToMap()
	if err != nil {
		return Doc{}, fmt.Errorf("feedback metadata: %w", err)
	}
	return Doc{
		ContainerTag: RepoTagNew(repo),
		CustomID:     customID,
		Type:         string(TypeFeedback),
		Content:      content,
		Metadata:     meta,
	}, nil
}

type feedbackReconciliation struct {
	DeleteFirst           []string
	LegacyDismissalBefore string
	Upsert                *Doc
	DeleteAfter           []string
	LegacyDismissalAfter  string
}

// feedbackReconciliationPlan orders reaction-owned state replacement fail-safe:
// removing a stale dismissal happens before installing confirmation, so a failed
// reinforce write cannot leave suppression active. Installing dismissal happens
// before removing confirmation because suppression is the requested current state.
// Source-specific IDs ensure this plan cannot retract trusted replies or praise.
func feedbackReconciliationPlan(owner, repo string, fb FeedbackMemory) (feedbackReconciliation, error) {
	if fb.Source != SourceReactionFeedback {
		return feedbackReconciliation{}, fmt.Errorf("reconciling feedback signal: source %q is not reversible reaction feedback", fb.Source)
	}
	confirmedID := feedbackCustomIDForSource(owner, repo, fb.FilePath, fb.Category, fb.OriginalBody, "confirmed", fb.Source)
	ignoredID := feedbackCustomIDForSource(owner, repo, fb.FilePath, fb.Category, fb.OriginalBody, "ignored", fb.Source)
	dismissedID := dismissalCustomIDForSource(repo, fb.Category, fb.OriginalBody, fb.Source)
	legacyDismissedID := dismissalCustomID(repo, fb.Category, fb.OriginalBody)

	switch fb.Action {
	case "":
		return feedbackReconciliation{
			DeleteFirst:           []string{dismissedID, confirmedID, ignoredID},
			LegacyDismissalBefore: legacyDismissedID,
		}, nil
	case "confirmed":
		doc, err := buildFeedbackDoc(owner, repo, fb)
		if err != nil {
			return feedbackReconciliation{}, err
		}
		return feedbackReconciliation{
			DeleteFirst:           []string{dismissedID, ignoredID},
			LegacyDismissalBefore: legacyDismissedID,
			Upsert:                &doc,
		}, nil
	case "dismissed":
		doc, err := buildFeedbackDoc(owner, repo, fb)
		if err != nil {
			return feedbackReconciliation{}, err
		}
		return feedbackReconciliation{
			Upsert:               &doc,
			DeleteAfter:          []string{confirmedID, ignoredID},
			LegacyDismissalAfter: legacyDismissedID,
		}, nil
	default:
		return feedbackReconciliation{}, fmt.Errorf("reconciling feedback signal: unsupported action %q (want confirmed|dismissed|empty)", fb.Action)
	}
}

// buildScenarioDoc shapes a scenario doc: description plus a "Related files"
// suffix when files exist, scenario_id in metadata (not content).
func buildScenarioDoc(repo string, scenarioID int64, description, severity string, files []string) (Doc, error) {
	content := description
	if len(files) > 0 {
		content += "\n\nRelated files: " + strings.Join(files, ", ")
	}
	meta, err := Metadata{
		Type:       TypeScenario,
		ScenarioID: scenarioID,
		Severity:   severity,
	}.ToMap()
	if err != nil {
		return Doc{}, fmt.Errorf("scenario metadata: %w", err)
	}
	return Doc{
		ContainerTag: RepoTagNew(repo),
		CustomID:     ScenarioCustomID(repo, scenarioID),
		Type:         string(TypeScenario),
		Content:      content,
		Metadata:     meta,
	}, nil
}

// patternSource is the customId source segment: explicit Source, else the
// "pattern" default both pattern writers share.
func patternSource(p PatternMemory) string {
	if p.Source == "" {
		return "pattern"
	}
	return p.Source
}
