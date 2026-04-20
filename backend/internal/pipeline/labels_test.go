package pipeline

import (
	"reflect"
	"testing"
)

// TestStageLabel exercises the composite-key parsing and the skim-fallback
// suppression. Any new StageOrder entry must ship with a stageLabels entry —
// TestLabelsCoverStageOrder below enforces that pair.
func TestStageLabel(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		// Plain stage keys.
		{"intent", "Intent"},
		{"triage", "Triage"},
		{"review", "Review"},
		{"file_synthesis", "File synthesis"},
		{"cross_pr", "Cross-PR"},
		// Composite keys with sub-identifier.
		{"review.bug_hunter", "Review · bug_hunter"},
		{"review.security", "Review · security"},
		{"file_synthesis.src/foo.py", "File synthesis · src/foo.py"},
		{"simulation.scenario_3", "Simulation · scenario_3"},
		// Skim fallback — "review.review" is the legacy bucket where the
		// Specialist field was empty and the PR comment aliased it to
		// "review". Rendering "Review · review" is redundant; drop the suffix.
		{"review.review", "Review"},
		// Unknown base key — pass through raw so a new stage Go-side shows up.
		{"novel_stage", "novel_stage"},
		{"novel_stage.detail", "novel_stage · detail"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := StageLabel(tc.key); got != tc.want {
				t.Errorf("StageLabel(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestLabelsCoverStageOrder is the drift guard. Every key in StageOrder must
// have an entry in stageLabels. A future refactor that adds "resolver" to the
// pipeline will fail this test on the first `go test` run until the dev also
// adds the label entry — prevents silent "raw stage key shown in UI" bugs.
func TestLabelsCoverStageOrder(t *testing.T) {
	for _, key := range StageOrder {
		if _, ok := stageLabels[key]; !ok {
			t.Errorf("StageOrder includes %q but stageLabels has no entry — add one in labels.go", key)
		}
	}
}

// TestStageOrderMatchesStruct asserts StageOrder covers every tokenable
// field of RunTokenUsage. Array fields (Review, FileSynthesis, Simulation)
// appear in StageOrder as their base key only — sub-keys like
// "review.bug_hunter" are synthesized at render time from each entry's
// Specialist/File field. mu and Total are excluded by design: mu is a
// mutex, Total is the aggregate sum, neither is a renderable stage.
func TestStageOrderMatchesStruct(t *testing.T) {
	skip := map[string]bool{"mu": true, "total": true}
	got := map[string]bool{}
	for _, k := range StageOrder {
		got[k] = true
	}
	ty := reflect.TypeOf(RunTokenUsage{})
	for i := 0; i < ty.NumField(); i++ {
		field := ty.Field(i)
		tag := field.Tag.Get("json")
		// Strip ",omitempty" etc.
		for j, c := range tag {
			if c == ',' {
				tag = tag[:j]
				break
			}
		}
		if tag == "-" || skip[tag] {
			continue
		}
		if !got[tag] {
			t.Errorf("RunTokenUsage field %s (json:%q) is missing from StageOrder in labels.go", field.Name, tag)
		}
	}
}

// TestSpecialistOrderIncludesCurrentSet pins the current 4-specialist
// taxonomy so a rename on the pipeline side (e.g. renaming bug_hunter) fails
// loudly at test time rather than producing a "Review · <old_name>" row that
// no longer matches any Specialist value written at review time.
//
// Also pins legacy "correctness" (the old name for bug_hunter that persists
// in archived reviews) and the skim-fallback "review" bucket — dropping
// either silently breaks rendering for pre-rename data or for skim-mode
// reviews that leave Specialist empty.
func TestSpecialistOrderIncludesCurrentSet(t *testing.T) {
	wantCurrent := []Specialist{SpecialistBugHunter, SpecialistSecurity, SpecialistArchitecture, SpecialistRegression}
	wantLegacy := []string{"correctness", "review"}
	set := map[string]bool{}
	for _, s := range SpecialistOrder {
		set[s] = true
	}
	for _, s := range wantCurrent {
		if !set[string(s)] {
			t.Errorf("SpecialistOrder missing current specialist %q — update labels.go when specialists rename", s)
		}
	}
	for _, s := range wantLegacy {
		if !set[s] {
			t.Errorf("SpecialistOrder missing legacy/fallback %q — dropping this silently breaks archived or skim-mode review rendering", s)
		}
	}
}
