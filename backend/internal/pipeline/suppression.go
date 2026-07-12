package pipeline

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// dismissalSearch retrieves the top dismissed-feedback matches (type=feedback,
// action=dismissed) semantically matching the finding body in the repo container
// — the read half of dismissal suppression. An empty body has nothing to query,
// so it short-circuits to no matches. Retrieval uses the FindingEnrich floor;
// the caller degrades a search error via memory.BestEffort (suppression is
// non-fatal). Result shaping (drop/downgrade policy) lives in evaluateDismissals.
func dismissalSearch(ctx context.Context, indexer memory.Indexer, repo, body string, threshold float64) ([]memory.PatternMatch, error) {
	if body == "" {
		return nil, nil
	}
	return indexer.Search(ctx, memory.MemoryQuery{
		Query:     body,
		Repo:      repo,
		Scope:     memory.ScopeRepo,
		Type:      memory.TypeFeedback,
		Filters:   []memory.FilterCondition{{Key: "action", Value: "dismissed"}},
		Limit:     dismissalSearchLimit,
		Threshold: threshold,
		Rerank:    true,
	})
}

// Dismissal-match suppression policy (locked). A generated finding that
// semantically matches a finding a developer previously 👎-dismissed in this
// repo is gated by the match similarity, against the memory.Thresholds floors:
//
//	>= SuppressionDrop      → DROP (never posted, persisted flagged suppressed)
//	>= SuppressionDowngrade → DOWNGRADE severity one level + attribution note
//	below                   → untouched
//
// The floors sit above the FindingEnrich retrieval threshold so a weak
// coincidental match doesn't silently mute a real finding. Every floor is now
// read from the resolved memory.Thresholds (single source, no bare literals);
// SuppressionDowngrade doubles as the "sufficiently similar" streak bar.

// Suppression v2 (team-feedback) count knobs (non-similarity; the similarity
// floors live in memory.Thresholds).
const (
	// dismissalSearchLimit is how many dismissed-feedback docs the enrichment
	// pass retrieves per finding — enough to count a SuppressSimilarCount
	// streak with headroom for lifecycle-filtered entries.
	dismissalSearchLimit = 5
	// SuppressSimilarCount is the number of sufficiently-similar dismissed
	// memories at/above the SuppressionDowngrade floor that suppresses a finding
	// outright even when no single match clears the drop threshold.
	SuppressSimilarCount = 3
)

// dismissalAction is the outcome of classifying a finding against the closest
// previously-dismissed finding.
type dismissalAction int

const (
	dismissalNone dismissalAction = iota
	dismissalDowngrade
	dismissalDrop
)

// classifyDismissal maps a dismissed-feedback similarity score onto the
// suppression action against the resolved thresholds. Pure — the whole policy
// lives here so the score→action matrix is table-testable in isolation.
func classifyDismissal(score float64, t memory.Thresholds) dismissalAction {
	switch {
	case score >= t.SuppressionDrop:
		return dismissalDrop
	case score >= t.SuppressionDowngrade:
		return dismissalDowngrade
	default:
		return dismissalNone
	}
}

// downgradeSeverity lowers a severity one level for the dismissal-downgrade
// path: critical→warning→suggestion. Suggestion is the floor; praise is never
// a suppression target and is returned unchanged.
func downgradeSeverity(s Severity) Severity {
	switch s {
	case SeverityCritical:
		return SeverityWarning
	case SeverityWarning:
		return SeveritySuggestion
	default:
		return s
	}
}

// applyDismissalMatch mutates c per the dismissal-match policy for the given
// similarity score and reports the action taken (for enrichment counters).
// pr is the source PR of the dismissed finding (0 = unknown) used to attribute
// the downgrade note. A drop sets Suppressed + SuppressedReason and leaves the
// comment in place for the posting seams to exclude; a downgrade lowers the
// severity and flags the comment so formatCommentBody appends the note.
func applyDismissalMatch(c *FileComment, score float64, pr int, t memory.Thresholds) dismissalAction {
	return applyDismissalEvaluation(c, dismissalEvaluation{
		action:    classifyDismissal(score, t),
		reason:    fmt.Sprintf("dismissed_match:%.2f", score),
		bestScore: score,
		bestPR:    pr,
	})
}

// dismissalEvaluation is the pure verdict of evaluateDismissals: what the
// suppression pass should do to a finding given its dismissed-feedback
// matches, change-kind lifecycle, exemptions, and category streaks.
type dismissalEvaluation struct {
	action       dismissalAction
	reason       string  // SuppressedReason when action == dismissalDrop
	bestScore    float64 // highest lifecycle-surviving match score
	bestPR       int     // source PR of the best match (0 = unknown)
	similarCount int     // matches at/above the SuppressionDowngrade floor after lifecycle filter
}

// evaluateDismissals is the whole suppression-v2 decision matrix, pure so the
// policy is table-testable:
//
//  1. Lifecycle: dismissals recorded on throwaway change kinds
//     (one_time_script/prototype) are ignored when the current PR is
//     production-grade — prototype-era feedback must not silence production
//     review.
//  2. Exempt findings (security / Law-12 permanent checks) may be DOWNGRADED
//     by memory but never dropped.
//  3. A single match >= SuppressionDrop drops (v1 behavior).
//  4. >= SuppressSimilarCount matches >= SuppressionDowngrade drops — the team
//     has repeatedly rejected this finding even if no single match is exact.
//  5. A category the repo auto-suppressed (consecutive-ignore streak) drops.
//  6. Otherwise the best match downgrades or does nothing per the thresholds.
func evaluateDismissals(matches []memory.PatternMatch, currentClass string, exempt, categoryAutoSuppressed bool, t memory.Thresholds) dismissalEvaluation {
	var ev dismissalEvaluation
	for _, m := range filterDismissalsForClass(matches, currentClass) {
		if m.Score >= t.SuppressionDowngrade {
			ev.similarCount++
		}
		if m.Score > ev.bestScore {
			ev.bestScore = m.Score
			ev.bestPR = metaInt(m.Metadata, "pr_number")
		}
	}

	switch {
	case exempt:
		// Memory may lower the volume on an exempt finding, never mute it.
		if classifyDismissal(ev.bestScore, t) != dismissalNone {
			ev.action = dismissalDowngrade
		}
	case classifyDismissal(ev.bestScore, t) == dismissalDrop:
		ev.action = dismissalDrop
		ev.reason = fmt.Sprintf("dismissed_match:%.2f", ev.bestScore)
	case ev.similarCount >= SuppressSimilarCount:
		ev.action = dismissalDrop
		ev.reason = fmt.Sprintf("team_feedback:%d", ev.similarCount)
	case categoryAutoSuppressed:
		ev.action = dismissalDrop
		ev.reason = "category_auto_suppressed"
	default:
		ev.action = classifyDismissal(ev.bestScore, t)
	}
	return ev
}

// applyDismissalEvaluation mutates c per an evaluateDismissals verdict and
// returns the action for enrichment counters. Shared by the v1 single-match
// path (applyDismissalMatch) and the v2 pass.
func applyDismissalEvaluation(c *FileComment, ev dismissalEvaluation) dismissalAction {
	switch ev.action {
	case dismissalDrop:
		c.Suppressed = true
		c.SuppressedReason = ev.reason
	case dismissalDowngrade:
		c.Severity = downgradeSeverity(c.Severity)
		c.DismissedDowngrade = true
		c.DismissedMatchPR = ev.bestPR
	}
	return ev.action
}

// filterDismissalsForClass drops dismissal matches whose recorded change_kind
// is a throwaway kind (one_time_script / prototype) when the current contract
// class is production-grade (production, migration, or unknown — a nil/empty
// contract behaves as production everywhere else in the pipeline). Dismissals
// without a change_kind stamp (pre-contract docs) always survive.
func filterDismissalsForClass(matches []memory.PatternMatch, currentClass string) []memory.PatternMatch {
	if !productionGradeClass(currentClass) {
		return matches
	}
	kept := make([]memory.PatternMatch, 0, len(matches))
	for _, m := range matches {
		if throwawayChangeKind(m.Metadata["change_kind"]) {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// productionGradeClass reports whether a contract change class demands the
// full production bar. Empty class = no contract / undecided = production.
func productionGradeClass(class string) bool {
	return class == "" || class == ChangeClassProduction || class == ChangeClassMigration
}

// throwawayChangeKind reports whether a dismissal's recorded change kind marks
// it as prototype-era feedback. "prototype" is accepted alongside the catalog
// class because branch prefixes (prototype/, spike/, poc/) may be echoed
// verbatim by older writers.
func throwawayChangeKind(kind string) bool {
	return kind == ChangeClassOneTimeScript || kind == "prototype"
}

// permanentCheckMarkers are lowercase substrings anchored on the Review Laws
// Law-12 permanent checks (destructive SQL, secrets/PII in logs, unit-ambiguous
// constants, silent behavior change, swallowed errors) plus data-safety
// vocabulary. A marker hit EXEMPTS the finding from suppression — over-matching
// fails open (the finding posts), never closed.
var permanentCheckMarkers = []string{
	// destructive SQL / data safety
	"drop table", "truncate", "delete from", "missing where", "without a where",
	"data loss", "irreversibl", "destructive",
	// secrets / PII entering logs or telemetry
	"secret", "credential", "api key", "password", "pii", "personally identifiable", "leak",
	// swallowed errors / silent behavior change
	"unchecked error", "swallow", "silently chang",
}

// suppressionExempt reports whether a finding is exempt from suppression:
// security category always; otherwise a Law-12 permanent-check / data-safety
// marker in the finding text. There is no dedicated data-safety category in
// the taxonomy, so the marker scan stands in for it.
func suppressionExempt(category Category, body string) bool {
	if category == CategorySecurity {
		return true
	}
	lower := strings.ToLower(body)
	for _, marker := range permanentCheckMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// countSuppressedFindings counts findings dropped by the suppression pass —
// feeds the "N findings suppressed by team feedback" summary line.
func countSuppressedFindings(reviews []FileReview) int {
	n := 0
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				n++
			}
		}
	}
	return n
}

// suppressionKey identifies a finding across the FileReviews and AllFileReviews
// snapshots. Path/Line/Body are value-copied identically into both (scoring
// snapshots FileReviews by copy BEFORE enrichment sets the Suppressed flag), so
// the same key resolves the finding in either snapshot. The \x00 separators keep
// the key unambiguous even when a body contains delimiters.
func suppressionKey(path string, line int, body string) string {
	return path + "\x00" + strconv.Itoa(line) + "\x00" + body
}

// isSuppressed reports whether a finding (path/line/body) was dropped by the
// dismissal-match pass. Pattern-learning reads the pre-enrich AllFileReviews
// snapshot, whose copies never receive the in-place Suppressed flag; this map
// lookup lets those paths skip dropped findings so a dismissed finding is never
// re-learned as a pattern. Empty/nil map ⇒ nothing suppressed.
func (run *PipelineRun) isSuppressed(path string, line int, body string) bool {
	if len(run.SuppressedKeys) == 0 {
		return false
	}
	_, ok := run.SuppressedKeys[suppressionKey(path, line, body)]
	return ok
}

// linkMatchedPattern records the best-match pattern on a comment. found=false
// is the doc→pattern-id lookup miss: MatchedPatternID is left unset so
// persistence skips the FK and pattern-stats entirely — a miss is non-fatal,
// never an error. The similarity score is persisted regardless of the lookup
// so the dashboard still shows how strong the match was.
func linkMatchedPattern(c *FileComment, patternID int64, found bool, score float64) {
	c.MatchedPatternScore = score
	if found {
		c.MatchedPatternID = patternID
	}
}
