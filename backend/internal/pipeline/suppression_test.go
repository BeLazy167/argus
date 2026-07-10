package pipeline

import "testing"

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
