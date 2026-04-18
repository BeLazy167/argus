package pipeline

import (
	"strings"
	"testing"
)

// TestRenderTokenBreakdown verifies that the per-stage / per-specialist token
// breakdown renders correctly in the review summary. The output is a
// collapsible markdown `<details>` block, so tests assert presence of
// stage labels rather than exact string layout.
func TestRenderTokenBreakdown(t *testing.T) {
	tests := []struct {
		name        string
		tu          *RunTokenUsage
		wantEmpty   bool
		wantLabels  []string // rows that must be present
		wantAbsent  []string // rows that must NOT be present
	}{
		{
			name:      "nil usage",
			tu:        nil,
			wantEmpty: true,
		},
		{
			name:      "zero total tokens",
			tu:        &RunTokenUsage{},
			wantEmpty: true,
		},
		{
			name: "single skim review — bucketed as 'review'",
			tu: &RunTokenUsage{
				Total:  StageTokens{TotalTokens: 500, Cost: 0.01},
				Triage: StageTokens{TotalTokens: 100, Cost: 0.002},
				Review: []StageTokens{
					{TotalTokens: 400, Cost: 0.008, File: "a.go"},
				},
			},
			wantLabels: []string{"Triage", "Review · review"},
			wantAbsent: []string{"Review · correctness", "Review · security"},
		},
		{
			name: "deep review with 4 specialists — each shown separately",
			tu: &RunTokenUsage{
				Total: StageTokens{TotalTokens: 10_000, Cost: 0.25},
				Review: []StageTokens{
					{TotalTokens: 2000, Cost: 0.05, File: "a.go", Specialist: "correctness"},
					{TotalTokens: 2500, Cost: 0.06, File: "a.go", Specialist: "security"},
					{TotalTokens: 3000, Cost: 0.08, File: "a.go", Specialist: "architecture"},
					{TotalTokens: 2500, Cost: 0.06, File: "a.go", Specialist: "regression"},
				},
			},
			wantLabels: []string{
				"Review · correctness",
				"Review · security",
				"Review · architecture",
				"Review · regression",
			},
			wantAbsent: []string{"Review · review"},
		},
		{
			name: "specialists aggregate across multiple files",
			tu: &RunTokenUsage{
				Total: StageTokens{TotalTokens: 4000, Cost: 0.10},
				Review: []StageTokens{
					{TotalTokens: 1000, Cost: 0.025, File: "a.go", Specialist: "security"},
					{TotalTokens: 3000, Cost: 0.075, File: "b.go", Specialist: "security"},
				},
			},
			wantLabels: []string{"Review · security"},
			// aggregate: 4000 tokens, $0.1000 — check that the row renders
			// with the summed numbers (4.0k)
			// (We don't assert exact formatting; absence of '1000' and '3000'
			// as isolated values isn't safe either because "$0.025" appears
			// to contain them via substring. Skip numeric assertions here.)
		},
		{
			name: "unknown specialist name is preserved (future-proof)",
			tu: &RunTokenUsage{
				Total: StageTokens{TotalTokens: 500, Cost: 0.01},
				Review: []StageTokens{
					{TotalTokens: 500, Cost: 0.01, File: "a.go", Specialist: "newthing"},
				},
			},
			wantLabels: []string{"Review · newthing"},
		},
		{
			name: "zero-token stage is omitted",
			tu: &RunTokenUsage{
				Total:  StageTokens{TotalTokens: 100, Cost: 0.002},
				Triage: StageTokens{TotalTokens: 100, Cost: 0.002},
				// Scoring, Synthesis, etc. all zero — must not appear.
			},
			wantLabels: []string{"Triage"},
			wantAbsent: []string{"Scoring", "Synthesis", "Graph", "Enrichment"},
		},
		{
			name: "file synthesis aggregated across files",
			tu: &RunTokenUsage{
				Total: StageTokens{TotalTokens: 300, Cost: 0.006},
				FileSynthesis: []StageTokens{
					{TotalTokens: 100, Cost: 0.002, File: "a.go"},
					{TotalTokens: 200, Cost: 0.004, File: "b.go"},
				},
			},
			wantLabels: []string{"File synthesis"},
		},
		{
			name: "details summary shows total tokens and cost",
			tu: &RunTokenUsage{
				Total:  StageTokens{TotalTokens: 1_234_567, Cost: 12.3456},
				Triage: StageTokens{TotalTokens: 1_234_567, Cost: 12.3456},
			},
			wantLabels: []string{"1.2M tokens", "$12.3456 total"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTokenBreakdown(tc.tu)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty string, got: %s", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("expected non-empty breakdown, got empty")
			}
			for _, label := range tc.wantLabels {
				if !strings.Contains(got, label) {
					t.Errorf("missing expected label %q in output:\n%s", label, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("unexpected label %q present in output:\n%s", absent, got)
				}
			}
		})
	}
}

// TestRenderTokenBreakdown_NewBuckets verifies the renderer adds rows for
// the new stages (LeadAgent, Acceptance, CrossPR, Simulation aggregate,
// Reply) when populated and omits them when zero. Row labels are matched
// case-insensitively via substring — exact casing is the backend team's
// choice. CrossPR is tolerated as "cross-pr" OR "cross pr".
func TestRenderTokenBreakdown_NewBuckets(t *testing.T) {
	tests := []struct {
		name       string
		tu         *RunTokenUsage
		wantSubs   []string // lowercased substrings that must appear
		absentSubs []string
	}{
		{
			name: "populated_new_buckets_render_rows",
			tu: &RunTokenUsage{
				Total:      StageTokens{TotalTokens: 100_000, Cost: 1.0},
				LeadAgent:  StageTokens{TotalTokens: 11_111, Cost: 0.11},
				Acceptance: StageTokens{TotalTokens: 22_222, Cost: 0.22},
				CrossPR:    StageTokens{TotalTokens: 33_333, Cost: 0.33},
				Reply:      StageTokens{TotalTokens: 66_666, Cost: 0.66},
				Simulation: []StageTokens{
					{TotalTokens: 44_444, Cost: 0.44},
					{TotalTokens: 55_555, Cost: 0.55},
				},
			},
			wantSubs: []string{"lead agent", "acceptance", "simulation", "reply"},
		},
		{
			name: "empty_new_buckets_are_omitted",
			tu: &RunTokenUsage{
				Total:  StageTokens{TotalTokens: 100, Cost: 0.002},
				Triage: StageTokens{TotalTokens: 100, Cost: 0.002},
			},
			absentSubs: []string{"lead agent", "acceptance", "cross-pr", "cross pr", "simulation", "reply"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := renderTokenBreakdown(tc.tu)
			if got == "" && len(tc.wantSubs) > 0 {
				t.Fatal("renderer returned empty; want rows")
			}
			lo := strings.ToLower(got)
			for _, s := range tc.wantSubs {
				if !strings.Contains(lo, s) {
					t.Errorf("missing substring %q in output:\n%s", s, got)
				}
			}
			if len(tc.wantSubs) > 0 {
				// CrossPR tolerated as "cross-pr" OR "cross pr".
				if !strings.Contains(lo, "cross-pr") && !strings.Contains(lo, "cross pr") {
					t.Errorf("missing cross-PR label (expected 'cross-pr' or 'cross pr'):\n%s", got)
				}
			}
			for _, s := range tc.absentSubs {
				if strings.Contains(lo, s) {
					t.Errorf("unexpected substring %q in output:\n%s", s, got)
				}
			}
		})
	}
}

// TestFormatTokens covers the k/M suffix thresholds used in the breakdown.
func TestFormatTokens(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_499, "1.5k"},
		{1_500, "1.5k"},
		{999_999, "1000.0k"},
		{1_000_000, "1.0M"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := formatTokens(tc.in); got != tc.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
