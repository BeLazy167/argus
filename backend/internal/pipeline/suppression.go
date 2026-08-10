package pipeline

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

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

// permanentCheck names one of the Review Laws Law-12 permanent checks (see
// reviewLaws in review.go). The rubric promises findings of these classes are
// "never suppressed by class, persona, or memory", and permanentCheckMarkers is
// the only thing that keeps that promise. The rubric and the marker table
// drifted apart once already — unit-ambiguous constants had no markers at all
// and behavior equivalence had only "silently chang", so a dismissal match could
// mute either class. TestPermanentCheckMarkersCoverRubric now pins them together.
type permanentCheck string

const (
	checkDestructiveSQL      permanentCheck = "destructive_sql"
	checkSecretsInLogs       permanentCheck = "secrets_in_logs"
	checkUnitAmbiguity       permanentCheck = "unit_ambiguity"
	checkBehaviorEquivalence permanentCheck = "behavior_equivalence"
	checkSwallowedError      permanentCheck = "swallowed_error"
)

// permanentChecks is the closed set of Law-12 classes, in rubric order. Every
// entry must have markers below; a class listed here with no markers is a class
// memory can silently mute.
var permanentChecks = []permanentCheck{
	checkDestructiveSQL,
	checkSecretsInLogs,
	checkUnitAmbiguity,
	checkBehaviorEquivalence,
	checkSwallowedError,
}

// markerPhrase is a conjunction of lowercase parts matched against the
// normalized finding body: EVERY part must be present for the phrase to hit.
// Conjunctions exist because two of the five checks have no canonical wording —
// a behavior-equivalence finding reads "changes the behaviour of", "no longer
// behavior-preserving", or "the behavior differs" — and enumerating exact
// phrases caught almost none of them.
//
// The body is normalized to space-separated tokens first (normalizeForMarkers),
// so a part wrapped in spaces (" unit ") matches whole words only, while a bare
// stem ("behavio", "chang") still matches inside a word.
type markerPhrase []string

// matches reports whether every part of the phrase appears in the already
// normalized body. A phrase with no parts would match every finding and switch
// suppression off wholesale; TestPermanentCheckMarkersCoverRubric rejects one.
func (p markerPhrase) matches(norm string) bool {
	for _, part := range p {
		if !strings.Contains(norm, part) {
			return false
		}
	}
	return true
}

// permanentCheckMarkers maps each Law-12 permanent check to the phrases that
// identify a finding of that class. A hit EXEMPTS the finding from suppression.
// The two error directions are not symmetric, and the table is tuned for that:
// a false exempt only costs a re-posted (downgraded) finding the team already
// dismissed, while a false miss lets memory permanently mute a data-loss,
// secret-leak, or silent-behavior-change report. Recall wins.
var permanentCheckMarkers = map[permanentCheck][]markerPhrase{
	// destructive SQL / data safety
	checkDestructiveSQL: {
		{"drop table"}, {"truncate"}, {"delete from"}, {"missing where"}, {"without a where"},
		{"data loss"}, {"irreversibl"}, {"destructive"},
	},
	// secrets / PII entering logs or telemetry
	checkSecretsInLogs: {
		{"secret"}, {"credential"}, {"api key"}, {"password"}, {"pii"},
		{"personally identifiable"}, {"leak"},
	},
	// Unit-ambiguous numeric constants ("retry delay 300 — seconds or ms?").
	// The bare word "unit" carries this class: such a finding names the unit it
	// cannot infer. Whole-word matching is what makes it usable — the raw
	// substring "unit" also lives inside "opportunity" and "community" — and
	// stripUnitTestNoise removes the "unit" of "unit test" first, without which
	// every "missing unit tests" finding would be exempt and suppression would be
	// dead for the whole testing category. The pair phrases catch the findings
	// that never say "unit" and name the two candidate units instead ("300 —
	// seconds or ms?"); their parts are whole words because the substring
	// "seconds" also sits inside "milliseconds", which would collapse the pair
	// into a single term and exempt every finding that merely measures something.
	checkUnitAmbiguity: {
		{" unit "}, {" units "}, {" unitless "},
		{" seconds ", " milliseconds "}, {" second ", " millisecond "}, {" seconds ", " ms "},
	},
	// Refactors/renames that silently change behavior. Anchored on the stem
	// "behavio" (behavior/behaviour/behavioral) paired with a change cue rather
	// than on fixed phrases, because this class is worded freely; "chang" alone
	// would match nearly every finding, since Law 5 makes every finding request a
	// change.
	checkBehaviorEquivalence: {
		{"silently chang"},
		{"behavio", "chang"}, {"behavio", "preserv"}, {"behavio", "equivalen"},
		{"behavio", "differ"}, {"behavio", "no longer"},
		{"not equivalent"}, {"no longer equivalent"}, {"semantic", "chang"},
	},
	// unchecked errors and swallowed exceptions
	checkSwallowedError: {
		{"unchecked error"}, {"swallow"},
	},
}

// normalizeForMarkers lowercases the finding body and collapses every run of
// non-alphanumeric characters into a single space, padding both ends. Markers
// then match against space-delimited tokens, which is what lets a marker written
// as " unit " mean the word "unit" and not the tail of "opportunity". It also
// makes punctuation irrelevant, so "behaviour-preserving", "`DELETE FROM`", and
// "api_key" match the same markers as their plain-prose spellings.
func normalizeForMarkers(body string) string {
	tokens := strings.FieldsFunc(strings.ToLower(body), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return " " + strings.Join(tokens, " ") + " "
}

// stripUnitTestNoise drops the "unit" of "unit test" / "unit tests" /
// "unit testing" from an already normalized body. "unit test" is the one
// high-frequency review phrase that contains the unit-ambiguity marker without
// being about measurement units; leaving it in would exempt every "missing unit
// test" finding from suppression, so the testing category could never be muted
// by team feedback. Token-walking (rather than string replacement) keeps repeat
// occurrences correct, since neighbouring matches share a delimiter space.
func stripUnitTestNoise(norm string) string {
	fields := strings.Fields(norm)
	kept := make([]string, 0, len(fields))
	for i, f := range fields {
		if f == "unit" && i+1 < len(fields) && strings.HasPrefix(fields[i+1], "test") {
			continue
		}
		kept = append(kept, f)
	}
	return " " + strings.Join(kept, " ") + " "
}

// suppressionExempt reports whether a finding is exempt from suppression:
// security category always; otherwise a Law-12 permanent-check / data-safety
// marker in the finding text. There is no dedicated data-safety category in
// the taxonomy, so the marker scan stands in for it.
func suppressionExempt(category Category, body string) bool {
	if category == CategorySecurity {
		return true
	}
	norm := stripUnitTestNoise(normalizeForMarkers(body))
	for _, phrases := range permanentCheckMarkers {
		for _, phrase := range phrases {
			if phrase.matches(norm) {
				return true
			}
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
