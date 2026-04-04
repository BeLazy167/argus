package pipeline

import "testing"

func TestScoringThresholdForSeverity(t *testing.T) {
	tests := []struct {
		severity string
		want     int
	}{
		{"critical", 55},
		{"Critical", 55},
		{"CRITICAL", 55},
		{"warning", 65},
		{"Warning", 65},
		{"suggestion", 75},
		{"info", 75},
		{"praise", 65},   // default
		{"unknown", 65},  // default
		{"", 65},         // default
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

func TestVariableThresholdFiltering(t *testing.T) {
	tests := []struct {
		name     string
		severity Severity
		score    int
		wantKept bool
	}{
		{"critical at 55 passes", SeverityCritical, 55, true},
		{"critical at 54 dropped", SeverityCritical, 54, false},
		{"warning at 65 passes", SeverityWarning, 65, true},
		{"warning at 64 dropped", SeverityWarning, 64, false},
		{"suggestion at 75 passes", SeveritySuggestion, 75, true},
		{"suggestion at 74 dropped", SeveritySuggestion, 74, false},
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
