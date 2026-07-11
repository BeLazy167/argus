package pipeline

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// TestSuppressedKeyGate proves the dismissal-drop signal bridges the two
// snapshots: enrichFindings sets Suppressed + records a key on the post-enrich
// FileReviews, while pattern-learning reads the pre-enrich AllFileReviews copy
// that never receives the in-place flag. The key (path/line/body — identical in
// both snapshots) must let the learning paths skip a dropped finding so it is
// never re-learned as a confirmed pattern.
func TestSuppressedKeyGate(t *testing.T) {
	// Post-enrich FileReviews: one dropped finding, flag set in place.
	dropped := FileComment{Line: 12, Body: "unchecked error from os.Open", Severity: SeverityCritical, Suppressed: true}
	fileReviews := []FileReview{{Path: "svc.go", Comments: []FileComment{dropped}}}

	// Build SuppressedKeys exactly as enrichFindings' counting loop does.
	run := &PipelineRun{}
	for _, fr := range fileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				if run.SuppressedKeys == nil {
					run.SuppressedKeys = make(map[string]struct{})
				}
				run.SuppressedKeys[suppressionKey(fr.Path, c.Line, c.Body)] = struct{}{}
			}
		}
	}

	// Pre-enrich AllFileReviews snapshot = value copy WITHOUT the flag (mirrors
	// scoring.go's copy()). This is what indexConfirmedPatterns/autoLearnPatterns
	// iterate.
	snap := dropped
	snap.Suppressed = false
	if run.isSuppressed("svc.go", snap.Line, snap.Body) != true {
		t.Error("dropped finding must be gated in the pre-enrich snapshot (would be re-learned as a pattern)")
	}

	// Negatives: a nil map, and mismatched path/line/body must not gate.
	var empty PipelineRun
	if empty.isSuppressed("svc.go", 12, "unchecked error from os.Open") {
		t.Error("nil SuppressedKeys must report not suppressed")
	}
	if run.isSuppressed("other.go", snap.Line, snap.Body) {
		t.Error("different path must not match")
	}
	if run.isSuppressed("svc.go", 13, snap.Body) {
		t.Error("different line must not match")
	}
	if run.isSuppressed("svc.go", snap.Line, "a different finding") {
		t.Error("different body must not match")
	}
}

// TestRebalanceSeverityIgnoresSuppressed: a suppressed (dropped) critical must
// not participate in the >50%-critical ratio or consume downgrade slots. Here a
// low-score VISIBLE critical would be downgraded if the high-score suppressed
// critical were counted; excluding it leaves 1/2 == 0.5 → no rebalance.
func TestRebalanceSeverityIgnoresSuppressed(t *testing.T) {
	reviews := []FileReview{{Path: "a.go", Comments: []FileComment{
		{Line: 1, Severity: SeverityCritical, Score: 40},
		{Line: 2, Severity: SeverityWarning, Score: 50},
		{Line: 3, Severity: SeverityCritical, Score: 95, Suppressed: true},
	}}}
	rebalanceSeverity(reviews)
	if got := reviews[0].Comments[0].Severity; got != SeverityCritical {
		t.Errorf("visible critical downgraded to %v — suppressed comment leaked into the rebalance ratio", got)
	}
	if got := reviews[0].Comments[2].Severity; got != SeverityCritical {
		t.Errorf("suppressed comment severity mutated to %v — should be left untouched", got)
	}
}

// TestRebalanceSeverityStillRebalancesVisible is the control: normal rebalancing
// among visible comments must still fire (the fix only excludes suppressed).
func TestRebalanceSeverityStillRebalancesVisible(t *testing.T) {
	reviews := []FileReview{{Path: "a.go", Comments: []FileComment{
		{Line: 1, Severity: SeverityCritical, Score: 10},
		{Line: 2, Severity: SeverityCritical, Score: 20},
		{Line: 3, Severity: SeverityCritical, Score: 90},
		{Line: 4, Severity: SeverityWarning, Score: 50},
	}}}
	rebalanceSeverity(reviews)
	// 3/4 criticals > 0.5 → rebalance; target=2 kept, excess=1 lowest-score
	// critical downgraded → 2 warnings total (1 downgraded + 1 original).
	warnings := 0
	for _, c := range reviews[0].Comments {
		if c.Severity == SeverityWarning {
			warnings++
		}
	}
	if warnings != 2 {
		t.Errorf("expected 2 warnings after rebalance, got %d", warnings)
	}
}

func TestClassifyDismissal(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		want  dismissalAction
	}{
		{"far_below_floor", 0.10, dismissalNone},
		{"just_below_downgrade", 0.59, dismissalNone},
		{"at_downgrade_floor", 0.60, dismissalDowngrade},
		{"mid_downgrade_band", 0.72, dismissalDowngrade},
		{"just_below_drop", 0.849, dismissalDowngrade},
		{"at_drop_floor", 0.85, dismissalDrop},
		{"above_drop", 0.97, dismissalDrop},
		{"perfect", 1.0, dismissalDrop},
		{"zero", 0.0, dismissalNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDismissal(tt.score); got != tt.want {
				t.Errorf("classifyDismissal(%v) = %v, want %v", tt.score, got, tt.want)
			}
		})
	}
}

func TestDowngradeSeverity(t *testing.T) {
	tests := []struct {
		name string
		in   Severity
		want Severity
	}{
		{"critical_to_warning", SeverityCritical, SeverityWarning},
		{"warning_to_suggestion", SeverityWarning, SeveritySuggestion},
		{"suggestion_is_floor", SeveritySuggestion, SeveritySuggestion},
		{"praise_untouched", SeverityPraise, SeverityPraise},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := downgradeSeverity(tt.in); got != tt.want {
				t.Errorf("downgradeSeverity(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestApplyDismissalMatch(t *testing.T) {
	tests := []struct {
		name           string
		score          float64
		pr             int
		startSeverity  Severity
		wantAction     dismissalAction
		wantSuppressed bool
		wantReason     string
		wantSeverity   Severity
		wantDowngrade  bool
		wantPR         int
	}{
		{
			name: "below_floor_untouched", score: 0.40, pr: 12, startSeverity: SeverityCritical,
			wantAction: dismissalNone, wantSuppressed: false, wantReason: "",
			wantSeverity: SeverityCritical, wantDowngrade: false, wantPR: 0,
		},
		{
			name: "downgrade_critical", score: 0.72, pr: 88, startSeverity: SeverityCritical,
			wantAction: dismissalDowngrade, wantSuppressed: false, wantReason: "",
			wantSeverity: SeverityWarning, wantDowngrade: true, wantPR: 88,
		},
		{
			name: "downgrade_warning", score: 0.60, pr: 0, startSeverity: SeverityWarning,
			wantAction: dismissalDowngrade, wantSuppressed: false, wantReason: "",
			wantSeverity: SeveritySuggestion, wantDowngrade: true, wantPR: 0,
		},
		{
			name: "downgrade_suggestion_stays", score: 0.80, pr: 5, startSeverity: SeveritySuggestion,
			wantAction: dismissalDowngrade, wantSuppressed: false, wantReason: "",
			wantSeverity: SeveritySuggestion, wantDowngrade: true, wantPR: 5,
		},
		{
			name: "drop_sets_reason", score: 0.91, pr: 7, startSeverity: SeverityCritical,
			wantAction: dismissalDrop, wantSuppressed: true, wantReason: "dismissed_match:0.91",
			wantSeverity: SeverityCritical, wantDowngrade: false, wantPR: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &FileComment{Severity: tt.startSeverity}
			got := applyDismissalMatch(c, tt.score, tt.pr)
			if got != tt.wantAction {
				t.Errorf("action = %v, want %v", got, tt.wantAction)
			}
			if c.Suppressed != tt.wantSuppressed {
				t.Errorf("Suppressed = %v, want %v", c.Suppressed, tt.wantSuppressed)
			}
			if c.SuppressedReason != tt.wantReason {
				t.Errorf("SuppressedReason = %q, want %q", c.SuppressedReason, tt.wantReason)
			}
			if c.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", c.Severity, tt.wantSeverity)
			}
			if c.DismissedDowngrade != tt.wantDowngrade {
				t.Errorf("DismissedDowngrade = %v, want %v", c.DismissedDowngrade, tt.wantDowngrade)
			}
			if c.DismissedMatchPR != tt.wantPR {
				t.Errorf("DismissedMatchPR = %v, want %v", c.DismissedMatchPR, tt.wantPR)
			}
		})
	}
}

// TestLinkMatchedPattern covers the doc→pattern-id mapping, including the miss
// path: a lookup that finds no patterns row (found=false) must leave
// MatchedPatternID unset so persistence skips the FK and pattern-stats, while
// still recording the similarity score.
func TestLinkMatchedPattern(t *testing.T) {
	tests := []struct {
		name      string
		patternID int64
		found     bool
		score     float64
		wantID    int64
		wantScore float64
	}{
		{"hit_sets_id", 42, true, 0.88, 42, 0.88},
		{"miss_skips_id", 42, false, 0.88, 0, 0.88},
		{"miss_zero_id", 0, false, 0.55, 0, 0.55},
		{"hit_low_score", 7, true, 0.51, 7, 0.51},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &FileComment{}
			linkMatchedPattern(c, tt.patternID, tt.found, tt.score)
			if c.MatchedPatternID != tt.wantID {
				t.Errorf("MatchedPatternID = %d, want %d", c.MatchedPatternID, tt.wantID)
			}
			if c.MatchedPatternScore != tt.wantScore {
				t.Errorf("MatchedPatternScore = %v, want %v", c.MatchedPatternScore, tt.wantScore)
			}
		})
	}
}

// mkDismissal builds a dismissed-feedback PatternMatch for suppression tests.
func mkDismissal(score float64, changeKind string, pr string) memory.PatternMatch {
	md := map[string]string{}
	if changeKind != "" {
		md["change_kind"] = changeKind
	}
	if pr != "" {
		md["pr_number"] = pr
	}
	return memory.PatternMatch{Content: "dismissed finding", Score: score, Metadata: md}
}

func TestFilterDismissalsForClass(t *testing.T) {
	proto := mkDismissal(0.7, ChangeClassOneTimeScript, "")
	protoLegacy := mkDismissal(0.7, "prototype", "")
	prod := mkDismissal(0.7, ChangeClassProduction, "")
	unstamped := mkDismissal(0.7, "", "")

	tests := []struct {
		name         string
		matches      []memory.PatternMatch
		currentClass string
		wantKept     int
	}{
		{"prototype dismissal filtered for production PR", []memory.PatternMatch{proto}, ChangeClassProduction, 0},
		{"legacy 'prototype' kind filtered for migration PR", []memory.PatternMatch{protoLegacy}, ChangeClassMigration, 0},
		{"prototype dismissal filtered when class unknown (nil-contract = production)", []memory.PatternMatch{proto}, "", 0},
		{"prototype dismissal kept for one-off script PR", []memory.PatternMatch{proto}, ChangeClassOneTimeScript, 1},
		{"production dismissal kept for production PR", []memory.PatternMatch{prod}, ChangeClassProduction, 1},
		{"pre-contract dismissal (no stamp) always kept", []memory.PatternMatch{unstamped}, ChangeClassProduction, 1},
		{"mixed set keeps only non-throwaway", []memory.PatternMatch{proto, prod, unstamped}, ChangeClassProduction, 2},
		{"docs PR keeps everything", []memory.PatternMatch{proto, prod}, ChangeClassDocs, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(filterDismissalsForClass(tt.matches, tt.currentClass)); got != tt.wantKept {
				t.Errorf("kept %d matches, want %d", got, tt.wantKept)
			}
		})
	}
}

func TestEvaluateDismissals(t *testing.T) {
	similar := func(n int) []memory.PatternMatch {
		out := make([]memory.PatternMatch, n)
		for i := range out {
			out[i] = mkDismissal(0.65, "", "")
		}
		return out
	}

	tests := []struct {
		name             string
		matches          []memory.PatternMatch
		currentClass     string
		exempt           bool
		categoryAuto     bool
		wantAction       dismissalAction
		wantReasonPrefix string
		wantSimilar      int
	}{
		{
			name: "no matches, nothing suppressed",
			wantAction: dismissalNone,
		},
		{
			name:       "single exact match drops (v1 behavior preserved)",
			matches:    []memory.PatternMatch{mkDismissal(0.91, "", "7")},
			wantAction: dismissalDrop, wantReasonPrefix: "dismissed_match:0.91", wantSimilar: 1,
		},
		{
			name:       "single mid match only downgrades",
			matches:    []memory.PatternMatch{mkDismissal(0.70, "", "")},
			wantAction: dismissalDowngrade, wantSimilar: 1,
		},
		{
			name:       "three similar dismissals drop even below the exact threshold",
			matches:    similar(3),
			wantAction: dismissalDrop, wantReasonPrefix: "team_feedback:3", wantSimilar: 3,
		},
		{
			name:       "two similar dismissals are not enough",
			matches:    similar(2),
			wantAction: dismissalDowngrade, wantSimilar: 2,
		},
		{
			name:         "prototype-era dismissals do not count against a production PR",
			matches:      []memory.PatternMatch{mkDismissal(0.9, ChangeClassOneTimeScript, ""), mkDismissal(0.65, ChangeClassOneTimeScript, ""), mkDismissal(0.65, ChangeClassOneTimeScript, "")},
			currentClass: ChangeClassProduction,
			wantAction:   dismissalNone, wantSimilar: 0,
		},
		{
			name:         "prototype-era dismissals still suppress on a one-off script PR",
			matches:      []memory.PatternMatch{mkDismissal(0.65, ChangeClassOneTimeScript, ""), mkDismissal(0.65, ChangeClassOneTimeScript, ""), mkDismissal(0.65, ChangeClassOneTimeScript, "")},
			currentClass: ChangeClassOneTimeScript,
			wantAction:   dismissalDrop, wantReasonPrefix: "team_feedback:3", wantSimilar: 3,
		},
		{
			name:         "auto-suppressed category drops with zero matches",
			categoryAuto: true,
			wantAction:   dismissalDrop, wantReasonPrefix: "category_auto_suppressed",
		},
		{
			name:       "exempt finding is never dropped by an exact match — capped at downgrade",
			matches:    []memory.PatternMatch{mkDismissal(0.95, "", "")},
			exempt:     true,
			wantAction: dismissalDowngrade, wantSimilar: 1,
		},
		{
			name:       "exempt finding is never dropped by a team-feedback streak",
			matches:    similar(4),
			exempt:     true,
			wantAction: dismissalDowngrade, wantSimilar: 4,
		},
		{
			name:         "exempt finding ignores category auto-suppression entirely",
			exempt:       true,
			categoryAuto: true,
			wantAction:   dismissalNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := evaluateDismissals(tt.matches, tt.currentClass, tt.exempt, tt.categoryAuto)
			if ev.action != tt.wantAction {
				t.Errorf("action = %v, want %v", ev.action, tt.wantAction)
			}
			if tt.wantReasonPrefix != "" && ev.reason != tt.wantReasonPrefix {
				t.Errorf("reason = %q, want %q", ev.reason, tt.wantReasonPrefix)
			}
			if ev.action != dismissalDrop && ev.reason != "" {
				t.Errorf("reason must be empty unless dropping, got %q", ev.reason)
			}
			if ev.similarCount != tt.wantSimilar {
				t.Errorf("similarCount = %d, want %d", ev.similarCount, tt.wantSimilar)
			}
		})
	}
}

func TestEvaluateDismissals_BestPRAttribution(t *testing.T) {
	ev := evaluateDismissals([]memory.PatternMatch{
		mkDismissal(0.62, "", "3"),
		mkDismissal(0.78, "", "42"), // best
	}, "", false, false)
	if ev.action != dismissalDowngrade {
		t.Fatalf("action = %v, want downgrade", ev.action)
	}
	if ev.bestPR != 42 {
		t.Errorf("bestPR = %d, want 42 (the highest-scoring match)", ev.bestPR)
	}
	c := &FileComment{Severity: SeverityCritical}
	applyDismissalEvaluation(c, ev)
	if c.Severity != SeverityWarning || !c.DismissedDowngrade || c.DismissedMatchPR != 42 {
		t.Errorf("downgrade application wrong: severity=%v downgrade=%v pr=%d", c.Severity, c.DismissedDowngrade, c.DismissedMatchPR)
	}
}

func TestSuppressionExempt(t *testing.T) {
	tests := []struct {
		name     string
		category Category
		body     string
		want     bool
	}{
		{"security category always exempt", CategorySecurity, "anything", true},
		{"destructive SQL body exempt", CategoryBug, "DELETE FROM users runs with a missing WHERE clause", true},
		{"secrets-in-logs body exempt", CategoryBug, "the API key is written to the request log", true},
		{"data-loss body exempt", CategoryBug, "this causes irreversible data loss on rollback", true},
		{"swallowed error body exempt", CategoryErrorHandling, "the error is swallowed and never surfaced", true},
		{"plain perf finding not exempt", CategoryPerformance, "N+1 query in the loop", false},
		{"plain testing finding not exempt", CategoryTesting, "missing test for the new branch", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suppressionExempt(tt.category, tt.body); got != tt.want {
				t.Errorf("suppressionExempt(%s, %q) = %v, want %v", tt.category, tt.body, got, tt.want)
			}
		})
	}
}

func TestCountSuppressedFindings(t *testing.T) {
	reviews := []FileReview{
		{Path: "a.go", Comments: []FileComment{{Suppressed: true}, {Suppressed: false}}},
		{Path: "b.go", Comments: []FileComment{{Suppressed: true}}},
	}
	if got := countSuppressedFindings(reviews); got != 2 {
		t.Errorf("countSuppressedFindings = %d, want 2", got)
	}
	if got := countSuppressedFindings(nil); got != 0 {
		t.Errorf("countSuppressedFindings(nil) = %d, want 0", got)
	}
}
