package pipeline

import (
	"strings"
	"testing"
)

func TestScoringThresholdForSeverity(t *testing.T) {
	tests := []struct {
		severity string
		want     int
	}{
		{"critical", 35},
		{"Critical", 35},
		{"CRITICAL", 35},
		{"warning", 45},
		{"Warning", 45},
		{"suggestion", 55},
		{"info", 55},
		{"praise", 45},   // default
		{"unknown", 45},  // default
		{"", 45},         // default
	}
	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			got := scoringThresholdForSeverity(tt.severity)
			if got != tt.want {
				t.Errorf("scoringThresholdForSeverity(%q) = %d, want %d", tt.severity, got, tt.want)
			}
		})
	}
}

func TestAdjustScores(t *testing.T) {
	tests := []struct {
		name      string
		comment   FileComment
		filePath  string
		initScore int
		wantScore int
		wantTag   string // substring expected in Reason
	}{
		{
			name:      "await on non-async capped at 25",
			comment:   FileComment{Body: "missing await on fetchData call", Line: 10},
			filePath:  "src/api.ts",
			initScore: 80,
			wantScore: 25,
			wantTag:   "[FP-cap: await-on-non-async]",
		},
		{
			name:      "await on async function unchanged",
			comment:   FileComment{Body: "missing await on async fetchData call", Line: 10},
			filePath:  "src/api.ts",
			initScore: 80,
			wantScore: 80,
			wantTag:   "",
		},
		{
			name:      "race condition in JS capped at 20",
			comment:   FileComment{Body: "race condition when updating shared state", Line: 5},
			filePath:  "src/store.js",
			initScore: 90,
			wantScore: 20,
			wantTag:   "[FP-cap: race-in-single-threaded-js]",
		},
		{
			name:      "race condition in Go not capped",
			comment:   FileComment{Body: "race condition when updating shared state", Line: 5},
			filePath:  "internal/server.go",
			initScore: 90,
			wantScore: 90,
			wantTag:   "",
		},
		{
			name:      "race condition in JS with Worker not capped",
			comment:   FileComment{Body: "race condition with Worker thread", Line: 5},
			filePath:  "src/worker.ts",
			initScore: 85,
			wantScore: 85,
			wantTag:   "",
		},
		{
			name:      "attacker framing on internal code reduced by 30",
			comment:   FileComment{Body: "an attacker could exploit this endpoint", Line: 15},
			filePath:  "internal/api/handler.go",
			initScore: 70,
			wantScore: 40,
			wantTag:   "[FP-cap: attacker-framing-internal]",
		},
		{
			name:      "attacker framing on public code unchanged",
			comment:   FileComment{Body: "an attacker could exploit this endpoint", Line: 15},
			filePath:  "src/api/handler.go",
			initScore: 70,
			wantScore: 70,
			wantTag:   "",
		},
		{
			name:      "speculative assertion capped at 40",
			comment:   FileComment{Body: "this might cause memory leaks under load", Line: 0},
			filePath:  "src/cache.ts",
			initScore: 75,
			wantScore: 40,
			wantTag:   "[FP-cap: speculative]",
		},
		{
			name:      "speculative with line number not capped",
			comment:   FileComment{Body: "this might cause memory leaks under load", Line: 42},
			filePath:  "src/cache.ts",
			initScore: 75,
			wantScore: 75,
			wantTag:   "",
		},
		{
			name:      "SAST corroborated floored at 75",
			comment:   FileComment{Body: "SQL injection in query builder", Line: 10, SastCorroborated: true},
			filePath:  "src/db.ts",
			initScore: 50,
			wantScore: 75,
			wantTag:   "[SAST-corroborated]",
		},
		{
			name:      "SAST corroborated already above 75 unchanged",
			comment:   FileComment{Body: "SQL injection in query builder", Line: 10, SastCorroborated: true},
			filePath:  "src/db.ts",
			initScore: 90,
			wantScore: 90,
			wantTag:   "[SAST-corroborated]",
		},
		{
			name:      "normal finding unchanged",
			comment:   FileComment{Body: "unused variable x", Line: 5},
			filePath:  "src/main.ts",
			initScore: 60,
			wantScore: 60,
			wantTag:   "",
		},
		{
			name:      "multiple caps: most restrictive wins (race in JS on internal)",
			comment:   FileComment{Body: "data race — an attacker could trigger concurrent access", Line: 5},
			filePath:  "internal/handler.tsx",
			initScore: 90,
			// race cap -> min(90,20)=20, then attacker-internal -> max(0,20-30)=0
			wantScore: 0,
			wantTag:   "[FP-cap: race-in-single-threaded-js]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &PipelineRun{
				FileReviews: []FileReview{
					{Path: tt.filePath, Comments: []FileComment{tt.comment}},
				},
			}
			groups := []judgeGroup{
				{Representative: 0, Score: tt.initScore, Severity: "warning", Reason: "test"},
			}
			allComments := []indexedComment{{fileIdx: 0, commentIdx: 0}}

			adjustScores(run, groups, allComments)

			if groups[0].Score != tt.wantScore {
				t.Errorf("score = %d, want %d", groups[0].Score, tt.wantScore)
			}
			if tt.wantTag != "" {
				if !contains(groups[0].Reason, tt.wantTag) {
					t.Errorf("reason %q missing tag %q", groups[0].Reason, tt.wantTag)
				}
			}
		})
	}
}

// TestPromoteGroupSeverity pins the severity-floor + body-merge contract that
// guards against the LLM judge picking a warning-rep over a critical-dup and
// silently demoting the posted severity. Verified on prod review 00000000.
func TestPromoteGroupSeverity(t *testing.T) {
	t.Parallel()

	newRun := func() *PipelineRun {
		return &PipelineRun{
			FileReviews: []FileReview{
				{Path: "a.tf", Comments: []FileComment{
					// idx 0 — detailed warning rep
					{Severity: SeverityWarning, Line: 318, What: "detailed warning with suggestion", Body: "long body"},
					// idx 1 — terse critical dup
					{Severity: SeverityCritical, Line: 292, What: "terse critical", Body: ""},
				}},
			},
		}
	}
	all := []indexedComment{{fileIdx: 0, commentIdx: 0}, {fileIdx: 0, commentIdx: 1}}

	t.Run("promotes warning rep to critical when dup outranks", func(t *testing.T) {
		t.Parallel()
		run := newRun()
		g := judgeGroup{Representative: 0, Duplicates: []int{1}, Score: 60, Severity: ""}
		p := promoteGroupSeverity(run, g, all)
		if !p.promoted {
			t.Fatalf("expected promoted=true")
		}
		if p.fromSev != SeverityWarning || p.toSev != SeverityCritical {
			t.Errorf("promotion = %s→%s, want warning→critical", p.fromSev, p.toSev)
		}
		rep := run.FileReviews[0].Comments[0]
		if rep.Severity != SeverityCritical {
			t.Errorf("rep severity = %s, want critical", rep.Severity)
		}
		if !strings.Contains(rep.Why, "Also flagged:") || !strings.Contains(rep.Why, "[critical]") {
			t.Errorf("rep Why missing cross-ref annotation: %q", rep.Why)
		}
		if !strings.Contains(rep.Why, "terse critical") {
			t.Errorf("rep Why missing dup snippet: %q", rep.Why)
		}
	})

	t.Run("no-op when rep already highest severity", func(t *testing.T) {
		t.Parallel()
		run := &PipelineRun{
			FileReviews: []FileReview{
				{Path: "a.tf", Comments: []FileComment{
					{Severity: SeverityCritical, Line: 10, What: "critical rep"},
					{Severity: SeverityWarning, Line: 20, What: "warning dup"},
				}},
			},
		}
		g := judgeGroup{Representative: 0, Duplicates: []int{1}, Score: 60}
		p := promoteGroupSeverity(run, g, all)
		if p.promoted {
			t.Errorf("should not promote when rep already highest; got %+v", p)
		}
		// Annotation still happens so author sees the warning's framing.
		if !strings.Contains(run.FileReviews[0].Comments[0].Why, "Also flagged:") {
			t.Errorf("Why should still carry the dup cross-ref annotation")
		}
	})

	t.Run("no duplicates is no-op", func(t *testing.T) {
		t.Parallel()
		run := newRun()
		origWhy := run.FileReviews[0].Comments[0].Why
		g := judgeGroup{Representative: 0, Duplicates: nil, Score: 60}
		if p := promoteGroupSeverity(run, g, all); p.promoted {
			t.Errorf("empty dups should not promote")
		}
		if run.FileReviews[0].Comments[0].Why != origWhy {
			t.Errorf("empty dups should not mutate Why")
		}
	})

	t.Run("out-of-range dup id skipped without panic", func(t *testing.T) {
		t.Parallel()
		run := newRun()
		g := judgeGroup{Representative: 0, Duplicates: []int{999, -1, 1}, Score: 60}
		p := promoteGroupSeverity(run, g, all)
		if !p.promoted {
			t.Errorf("valid dup should still promote despite bogus ids in list")
		}
	})

	t.Run("out-of-range rep returns no-op", func(t *testing.T) {
		t.Parallel()
		run := newRun()
		g := judgeGroup{Representative: 999, Duplicates: []int{1}, Score: 60}
		if p := promoteGroupSeverity(run, g, all); p.promoted {
			t.Errorf("invalid rep should not promote")
		}
	})

	t.Run("appends to non-empty Why", func(t *testing.T) {
		t.Parallel()
		run := newRun()
		run.FileReviews[0].Comments[0].Why = "existing rationale"
		g := judgeGroup{Representative: 0, Duplicates: []int{1}, Score: 60}
		promoteGroupSeverity(run, g, all)
		why := run.FileReviews[0].Comments[0].Why
		if !strings.HasPrefix(why, "existing rationale") {
			t.Errorf("existing Why clobbered: %q", why)
		}
		if !strings.Contains(why, "Also flagged:") {
			t.Errorf("annotation missing: %q", why)
		}
	})
}

func TestVariableThresholdFiltering(t *testing.T) {
	tests := []struct {
		name     string
		severity Severity
		score    int
		wantKept bool
	}{
		{"critical at 35 passes", SeverityCritical, 35, true},
		{"critical at 34 dropped", SeverityCritical, 34, false},
		{"warning at 45 passes", SeverityWarning, 45, true},
		{"warning at 44 dropped", SeverityWarning, 44, false},
		{"suggestion at 55 passes", SeveritySuggestion, 55, true},
		{"suggestion at 54 dropped", SeveritySuggestion, 54, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold := scoringThresholdForSeverity(string(tt.severity))
			kept := tt.score >= threshold
			if kept != tt.wantKept {
				t.Errorf("score %d >= threshold %d = %v, want %v", tt.score, threshold, kept, tt.wantKept)
			}
		})
	}
}

func TestFileExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"src/main.ts", ".ts"},
		{"internal/handler.go", ".go"},
		{"src/component.tsx", ".tsx"},
		{"Makefile", ""},
		{"src/.hidden", ".hidden"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := fileExt(tt.path)
			if got != tt.want {
				t.Errorf("fileExt(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
