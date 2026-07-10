package pipeline

import (
	"fmt"
	"strconv"
)

// Dismissal-match suppression policy (locked). A generated finding that
// semantically matches a finding a developer previously 👎-dismissed in this
// repo is gated by the match similarity:
//
//	>= DismissalDropThreshold      → DROP (never posted, persisted flagged suppressed)
//	>= DismissalDowngradeThreshold → DOWNGRADE severity one level + attribution note
//	below                          → untouched
//
// The floor sits above the FindingEnrich retrieval threshold (0.50) so a weak
// coincidental match doesn't silently mute a real finding.
const (
	DismissalDropThreshold      = 0.85
	DismissalDowngradeThreshold = 0.60
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
// suppression action. Pure — the whole policy lives here so the score→action
// matrix is table-testable in isolation.
func classifyDismissal(score float64) dismissalAction {
	switch {
	case score >= DismissalDropThreshold:
		return dismissalDrop
	case score >= DismissalDowngradeThreshold:
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
func applyDismissalMatch(c *FileComment, score float64, pr int) dismissalAction {
	action := classifyDismissal(score)
	switch action {
	case dismissalDrop:
		c.Suppressed = true
		c.SuppressedReason = fmt.Sprintf("dismissed_match:%.2f", score)
	case dismissalDowngrade:
		c.Severity = downgradeSeverity(c.Severity)
		c.DismissedDowngrade = true
		c.DismissedMatchPR = pr
	}
	return action
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
