package pipeline

import (
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// gaugeTestDiff: one modified line at old-line 12, and a pure insertion
// between old-lines 21 and 22.
const gaugeTestDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,7 +10,7 @@
 context10
 context11
-old12
+new12
 context13
 context14
 context15
 context16
@@ -20,4 +20,6 @@
 c20
 c21
+added
+added2
 c22
 c23
`

func TestTouchedNearAnchor(t *testing.T) {
	ps, err := diff.Parse(gaugeTestDiff)
	if err != nil {
		t.Fatalf("parsing fixture diff: %v", err)
	}

	tests := []struct {
		name   string
		path   string
		anchor int
		want   bool
	}{
		{"exact hit on modified line", "foo.go", 12, true},
		{"within -3 of modified line", "foo.go", 9, true},
		{"within +3 of modified line", "foo.go", 15, true},
		{"just outside -window", "foo.go", 8, false},
		{"replacement add touches boundary 13", "foo.go", 16, true},
		{"just outside +window", "foo.go", 17, false},
		{"insertion boundary hit below", "foo.go", 19, true},
		{"insertion boundary hit above", "foo.go", 24, true},
		{"outside insertion window", "foo.go", 26, false},
		{"wrong file", "bar.go", 12, false},
		{"zero anchor never matches", "foo.go", 0, false},
		{"negative anchor never matches", "foo.go", -5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TouchedNearAnchor(ps.Files, tt.path, tt.anchor); got != tt.want {
				t.Errorf("TouchedNearAnchor(%q, %d) = %v, want %v", tt.path, tt.anchor, got, tt.want)
			}
		})
	}
}

func TestTouchedNearAnchorEmptyPatch(t *testing.T) {
	if TouchedNearAnchor(nil, "foo.go", 12) {
		t.Error("nil patch set must not match")
	}
}

func TestIsAgentLogin(t *testing.T) {
	tests := []struct {
		name  string
		login string
		extra []string
		want  bool
	}{
		{"github app bot suffix", "argus-eye[bot]", nil, true},
		{"agent suffix", "devin-agent", nil, true},
		{"bot dash suffix", "renovate-bot", nil, true},
		{"bot underscore suffix", "deploy_bot", nil, true},
		{"case insensitive suffix", "Argus-Eye[BOT]", nil, true},
		{"plain human", "octocat", nil, false},
		{"bot substring not suffix", "botanist", nil, false},
		{"agent substring not suffix", "agentsmith", nil, false},
		{"configured list match", "cursor", []string{"cursor"}, true},
		{"configured list case-insensitive", "Cursor", []string{"cursor"}, true},
		{"configured list miss", "octocat", []string{"cursor"}, false},
		{"empty login", "", []string{"cursor"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAgentLogin(tt.login, tt.extra); got != tt.want {
				t.Errorf("IsAgentLogin(%q, %v) = %v, want %v", tt.login, tt.extra, got, tt.want)
			}
		})
	}
}

func TestParseAgentLogins(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a,b", 2},
		{" a , , b ", 2},
	}
	for _, tt := range tests {
		if got := parseAgentLogins(tt.raw); len(got) != tt.want {
			t.Errorf("parseAgentLogins(%q) len = %d, want %d", tt.raw, len(got), tt.want)
		}
	}
}

func TestWeightedAddressRate(t *testing.T) {
	tests := []struct {
		name                 string
		human, agent, posted int
		want                 float64
	}{
		{"no posted findings", 0, 0, 0, 0},
		{"negative posted", 1, 0, -1, 0},
		{"all human", 4, 0, 4, 1.0},
		{"all agent counts half", 0, 4, 4, 0.5},
		{"mixed weighting", 2, 2, 4, 0.75},
		{"nothing addressed", 0, 0, 10, 0},
		{"view arithmetic sample", 3, 1, 8, 0.4375},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WeightedAddressRate(tt.human, tt.agent, tt.posted); got != tt.want {
				t.Errorf("WeightedAddressRate(%d, %d, %d) = %v, want %v", tt.human, tt.agent, tt.posted, got, tt.want)
			}
		})
	}
}

func TestBuildGlassBoxLine(t *testing.T) {
	specialists := []string{"bug_hunter", "security", "architecture", "regression"}
	tests := []struct {
		name       string
		contract   *ReviewContract
		checked    []string
		suppressed int
		took       time.Duration
		want       string
	}{
		{
			name:     "nil contract defaults to production/full",
			contract: nil,
			checked:  []string{"single-pass review"},
			took:     42 * time.Second,
			want:     "Contract: production/full · checked: single-pass review · review took 42s",
		},
		{
			name:       "full deep-review block",
			contract:   &ReviewContract{ChangeClass: ChangeClassProduction, Depth: DepthFull},
			checked:    specialists,
			suppressed: 2,
			took:       102 * time.Second,
			want:       "Contract: production/full · checked: bug_hunter, security, architecture, regression · 2 suppressed by team feedback · review took 1m42s",
		},
		{
			name:     "llm-pending contract falls back to production class",
			contract: &ReviewContract{Depth: DepthSkim, Source: ContractSourceLLMPending},
			checked:  []string{"single-pass review"},
			want:     "Contract: production/skim · checked: single-pass review",
		},
		{
			name:       "zero suppressed and zero duration omitted",
			contract:   &ReviewContract{ChangeClass: ChangeClassDocs, Depth: DepthSkim},
			checked:    nil,
			suppressed: 0,
			took:       0,
			want:       "Contract: docs/skim",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildGlassBoxLine(tt.contract, tt.checked, tt.suppressed, tt.took); got != tt.want {
				t.Errorf("BuildGlassBoxLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckedReviewers(t *testing.T) {
	tests := []struct {
		name string
		run  *PipelineRun
		want string
	}{
		{"deep review squad", &PipelineRun{DeepReview: true}, "bug_hunter"},
		{"deep review one-time script", &PipelineRun{DeepReview: true, Contract: &ReviewContract{ChangeClass: ChangeClassOneTimeScript}}, string(SpecialistScript)},
		{"single pass", &PipelineRun{}, "single-pass review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkedReviewers(tt.run)
			if len(got) == 0 || got[0] != tt.want {
				t.Errorf("checkedReviewers() = %v, want first %q", got, tt.want)
			}
		})
	}
}

func TestCountSuppressed(t *testing.T) {
	run := &PipelineRun{FileReviews: []FileReview{
		{Path: "a.go", Comments: []FileComment{{Suppressed: true}, {Suppressed: false}}},
		{Path: "b.go", Comments: []FileComment{{Suppressed: true}}},
	}}
	if got := countSuppressed(run); got != 2 {
		t.Errorf("countSuppressed() = %d, want 2", got)
	}
}
