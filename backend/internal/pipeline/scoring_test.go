package pipeline

import (
	"strings"
	"testing"
)

func TestScoringThresholdForSeverity(t *testing.T) {
	scriptContract := &ReviewContract{ChangeClass: ChangeClassOneTimeScript}
	docsContract := &ReviewContract{ChangeClass: ChangeClassDocs}
	genContract := &ReviewContract{ChangeClass: ChangeClassGenerated}
	migContract := &ReviewContract{ChangeClass: ChangeClassMigration}
	secContract := &ReviewContract{ChangeClass: ChangeClassProduction, Signals: []string{"floor:security"}}
	prodContract := &ReviewContract{ChangeClass: ChangeClassProduction}

	tests := []struct {
		name     string
		severity string
		contract *ReviewContract
		want     int
	}{
		{"critical nil contract", "critical", nil, 35},
		{"critical mixed case", "Critical", nil, 35},
		{"warning nil contract", "warning", nil, 45},
		{"suggestion nil contract", "suggestion", nil, 55},
		{"info nil contract", "info", nil, 55},
		{"praise default", "praise", nil, 45},
		{"unknown default", "unknown", nil, 45},
		{"empty default", "", nil, 45},
		// Production class: no conditioning.
		{"production critical unchanged", "critical", prodContract, 35},
		{"production suggestion unchanged", "suggestion", prodContract, 55},
		// Low-stakes classes raise suggestion +15, warning +10.
		{"script suggestion raised", "suggestion", scriptContract, 70},
		{"script warning raised", "warning", scriptContract, 55},
		{"script critical unchanged", "critical", scriptContract, 35},
		{"docs suggestion raised", "suggestion", docsContract, 70},
		{"docs warning raised", "warning", docsContract, 55},
		{"generated suggestion raised", "suggestion", genContract, 70},
		{"generated warning raised", "warning", genContract, 55},
		// Migration / security floor: critical MORE sensitive (-5).
		{"migration critical lowered", "critical", migContract, 30},
		{"migration warning unchanged", "warning", migContract, 45},
		{"security floor critical lowered", "critical", secContract, 30},
		{"security floor suggestion unchanged", "suggestion", secContract, 55},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoringThresholdForSeverity(tt.severity, tt.contract)
			if got != tt.want {
				t.Errorf("scoringThresholdForSeverity(%q, %+v) = %d, want %d", tt.severity, tt.contract, got, tt.want)
			}
		})
	}
}

func TestThresholdDispositionFor(t *testing.T) {
	scriptContract := &ReviewContract{ChangeClass: ChangeClassOneTimeScript}
	tests := []struct {
		name     string
		score    int
		severity Severity
		contract *ReviewContract
		want     thresholdDisposition
	}{
		{"at threshold inline", 45, SeverityWarning, nil, dispositionInline},
		{"just below threshold is minor note", 44, SeverityWarning, nil, dispositionMinorNote},
		{"band edge is minor note", 35, SeverityWarning, nil, dispositionMinorNote},
		{"below band dropped", 34, SeverityWarning, nil, dispositionDrop},
		{"critical at 35 inline", 35, SeverityCritical, nil, dispositionInline},
		{"critical at 25 minor note", 25, SeverityCritical, nil, dispositionMinorNote},
		{"critical at 24 dropped", 24, SeverityCritical, nil, dispositionDrop},
		// Class conditioning shifts the whole band: script suggestion threshold 70.
		{"script suggestion at 69 minor note", 69, SeveritySuggestion, scriptContract, dispositionMinorNote},
		{"script suggestion at 55 dropped", 55, SeveritySuggestion, scriptContract, dispositionDrop},
		{"script suggestion at 70 inline", 70, SeveritySuggestion, scriptContract, dispositionInline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := thresholdDispositionFor(tt.score, tt.severity, tt.contract)
			if got != tt.want {
				t.Errorf("thresholdDispositionFor(%d, %s) = %d, want %d", tt.score, tt.severity, got, tt.want)
			}
		})
	}
}

// TestApplyThresholdFilterNoResurrection pins the minSurvivors deletion: when
// every finding scores below threshold, ZERO comments survive inline — the
// near-miss band lands in MinorNotes and nothing is resurrected.
func TestApplyThresholdFilterNoResurrection(t *testing.T) {
	run := &PipelineRun{
		FileReviews: []FileReview{
			{Path: "a.go", Comments: []FileComment{
				{Line: 1, What: "near miss", Severity: SeverityWarning, Score: 40},  // 45-10 <= 40 < 45 → minor
				{Line: 2, What: "way below", Severity: SeverityWarning, Score: 10},  // drop
				{Line: 3, What: "also below", Severity: SeveritySuggestion, Score: 30}, // drop (55-10=45 > 30)
			}},
		},
	}
	kept, minor, dropped := applyThresholdFilter(run, nil)
	if kept != 0 {
		t.Errorf("kept = %d, want 0 — below-threshold findings must never be resurrected inline", kept)
	}
	if minor != 1 || len(run.MinorNotes) != 1 {
		t.Errorf("minor = %d (notes=%d), want 1", minor, len(run.MinorNotes))
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
	if len(run.FileReviews) != 0 {
		t.Errorf("FileReviews = %d, want 0 inline survivors", len(run.FileReviews))
	}
	if run.MinorNotes[0].Title != "near miss" || run.MinorNotes[0].Path != "a.go" {
		t.Errorf("unexpected minor note: %+v", run.MinorNotes[0])
	}
}

func TestApplyThresholdFilterKeepsSurvivorsAndSkips(t *testing.T) {
	run := &PipelineRun{
		FileReviews: []FileReview{
			{Path: "a.go", Comments: []FileComment{
				{Line: 1, What: "keeper", Severity: SeverityCritical, Score: 80},
				{Line: 2, What: "judge dup", Severity: SeverityCritical, Score: 80},
			}},
		},
	}
	kept, minor, dropped := applyThresholdFilter(run, func(fi, ci int) bool { return ci == 1 })
	if kept != 1 || minor != 0 || dropped != 0 {
		t.Errorf("kept/minor/dropped = %d/%d/%d, want 1/0/0", kept, minor, dropped)
	}
	if len(run.FileReviews) != 1 || len(run.FileReviews[0].Comments) != 1 || run.FileReviews[0].Comments[0].What != "keeper" {
		t.Fatalf("unexpected survivors: %+v", run.FileReviews)
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
		{
			name:      "style category capped at 30 regardless of judge score",
			comment:   FileComment{Body: "helper duplicated, extract shared function", Category: CategoryStyle, Line: 8},
			filePath:  "src/util.ts",
			initScore: 95,
			wantScore: 30,
			wantTag:   "[cap: style]",
		},
		{
			name:      "readability category capped at 30",
			comment:   FileComment{Body: "logic is hard to follow", Category: CategoryReadability, Line: 8},
			filePath:  "src/util.ts",
			initScore: 88,
			wantScore: 30,
			wantTag:   "[cap: style]",
		},
		{
			name:      "style-ish text capped at 30 even with bug category",
			comment:   FileComment{Body: "consider renaming this to be clearer", Category: CategoryBug, Line: 8},
			filePath:  "src/util.ts",
			initScore: 90,
			wantScore: 30,
			wantTag:   "[cap: style]",
		},
		{
			name:      "error_handling capped at 45 on non-security file",
			comment:   FileComment{Body: "this call should handle the error return", Category: CategoryErrorHandling, Line: 12},
			filePath:  "src/render/list.go",
			initScore: 90,
			wantScore: 45,
			wantTag:   "[cap: error-handling]",
		},
		{
			name:      "error_handling NOT capped on security-relevant file",
			comment:   FileComment{Body: "this call should handle the error return", Category: CategoryErrorHandling, Line: 12},
			filePath:  "src/auth/session.go",
			initScore: 90,
			wantScore: 90,
			wantTag:   "",
		},
		{
			name:      "corroboration >=2 boosts +10",
			comment:   FileComment{Body: "nil dereference when cache is cold", Category: CategoryBug, Line: 3, Corroboration: 2},
			filePath:  "src/cache.go",
			initScore: 60,
			wantScore: 70,
			wantTag:   "[corroborated x2: +10]",
		},
		{
			name:      "corroboration boost bounded at 100",
			comment:   FileComment{Body: "nil dereference when cache is cold", Category: CategoryBug, Line: 3, Corroboration: 4},
			filePath:  "src/cache.go",
			initScore: 95,
			wantScore: 100,
			wantTag:   "[corroborated x4: +10]",
		},
		{
			name:      "corroboration below 2 does not boost",
			comment:   FileComment{Body: "nil dereference when cache is cold", Category: CategoryBug, Line: 3, Corroboration: 1},
			filePath:  "src/cache.go",
			initScore: 60,
			wantScore: 60,
			wantTag:   "",
		},
		{
			name:      "cap binds over corroboration boost (boost is never a gate-breaker)",
			comment:   FileComment{Body: "inconsistent formatting in this block", Category: CategoryStyle, Line: 3, Corroboration: 3},
			filePath:  "src/cache.go",
			initScore: 90,
			wantScore: 30,
			wantTag:   "[cap: style]",
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
// silently demoting the posted severity. Verified on a prod review.
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
			threshold := scoringThresholdForSeverity(string(tt.severity), nil)
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

// TestCorroborationOf pins the dedup-merge specialist counting used for the
// scoring boost: distinct specialists only, floored at prior-layer counts.
func TestCorroborationOf(t *testing.T) {
	tests := []struct {
		name    string
		cluster []taggedComment
		want    int
	}{
		{
			name: "two distinct specialists",
			cluster: []taggedComment{
				{comment: FileComment{Specialist: SpecialistBugHunter}},
				{comment: FileComment{Specialist: SpecialistSecurity}},
			},
			want: 2,
		},
		{
			name: "same specialist twice counts once",
			cluster: []taggedComment{
				{comment: FileComment{Specialist: SpecialistBugHunter}},
				{comment: FileComment{Specialist: SpecialistBugHunter}},
			},
			want: 1,
		},
		{
			name: "empty specialists ignored",
			cluster: []taggedComment{
				{comment: FileComment{}},
				{comment: FileComment{}},
			},
			want: 0,
		},
		{
			name: "prior layer count preserved when higher",
			cluster: []taggedComment{
				{comment: FileComment{Specialist: SpecialistBugHunter, Corroboration: 3}},
				{comment: FileComment{Specialist: SpecialistBugHunter}},
			},
			want: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := corroborationOf(tt.cluster); got != tt.want {
				t.Errorf("corroborationOf = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCollectBlockingFiles pins the one-round-ordering demotion input: only
// files with a non-suppressed critical finding count as blocking.
func TestCollectBlockingFiles(t *testing.T) {
	reviews := []FileReview{
		{Path: "a.go", Comments: []FileComment{
			{Severity: SeverityCritical, Line: 1},
			{Severity: SeveritySuggestion, Line: 2},
		}},
		{Path: "b.go", Comments: []FileComment{
			{Severity: SeverityWarning, Line: 1},
		}},
		{Path: "c.go", Comments: []FileComment{
			{Severity: SeverityCritical, Line: 1, Suppressed: true},
		}},
	}
	got := collectBlockingFiles(reviews)
	if !got["a.go"] {
		t.Errorf("a.go should be blocking")
	}
	if got["b.go"] {
		t.Errorf("b.go has no critical — not blocking")
	}
	if got["c.go"] {
		t.Errorf("c.go's critical is suppressed — not blocking")
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
