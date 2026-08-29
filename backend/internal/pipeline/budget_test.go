package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/admission"

	"github.com/BeLazy167/argus/backend/pkg/diff"
)

func capRun(maxFiles int, names ...string) (*PipelineRun, map[string]TriageAction) {
	files := make([]diff.FileDiff, 0, len(names))
	for _, n := range names {
		files = append(files, diff.FileDiff{NewName: n})
	}
	return &PipelineRun{Diff: &diff.PatchSet{Files: files}, BudgetMaxFiles: maxFiles}, map[string]TriageAction{}
}

func namesOf(files []diff.FileDiff) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.NewName)
	}
	return out
}

// The ordinary case: no cap, the diff is untouched.
func TestBudgetCapFiles_NoCapIsPassThrough(t *testing.T) {
	run, tri := capRun(0, "a", "b", "c")
	if got := len(budgetCapFiles(run, tri)); got != 3 {
		t.Errorf("got %d files, want all 3 when no cap is set", got)
	}
}

// A nil diff must not panic. checkBudget guards this too, but the helper is
// exported to the package and must stand on its own.
func TestBudgetCapFiles_NilDiff(t *testing.T) {
	run := &PipelineRun{BudgetMaxFiles: 5}
	if got := budgetCapFiles(run, nil); got != nil {
		t.Errorf("nil diff must yield nil, got %v", got)
	}
}

// The cap keeps the highest-risk files, not an arbitrary prefix of the diff.
func TestBudgetCapFiles_KeepsHighestRiskFirst(t *testing.T) {
	run, _ := capRun(2, "skim1", "deep1", "skim2", "deep2")
	tri := map[string]TriageAction{
		"skim1": TriageSkim, "deep1": TriageDeep,
		"skim2": TriageSkim, "deep2": TriageDeep,
	}

	got := namesOf(budgetCapFiles(run, tri))
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2: %v", len(got), got)
	}
	for _, name := range got {
		if tri[name] != TriageDeep {
			t.Errorf("cap kept %q (%v); deep files must win the slots: %v", name, tri[name], got)
		}
	}
}

// Skipped files cost nothing to review, so they must not consume a slot.
func TestBudgetCapFiles_SkippedFilesDoNotConsumeTheCap(t *testing.T) {
	run, _ := capRun(2, "skip1", "skip2", "deep1", "deep2")
	tri := map[string]TriageAction{
		"skip1": TriageSkip, "skip2": TriageSkip,
		"deep1": TriageDeep, "deep2": TriageDeep,
	}

	got := namesOf(budgetCapFiles(run, tri))
	if len(got) != 2 {
		t.Fatalf("got %v, want both deep files", got)
	}
	for _, name := range got {
		if tri[name] == TriageSkip {
			t.Errorf("a skipped file took a slot: %v", got)
		}
	}
}

// Under the cap, nothing is reordered — the original diff order survives.
func TestBudgetCapFiles_UnderCapKeepsOriginalOrder(t *testing.T) {
	run, _ := capRun(10, "a", "b", "c")
	tri := map[string]TriageAction{"a": TriageSkim, "b": TriageDeep, "c": TriageSkim}

	got := namesOf(budgetCapFiles(run, tri))
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order changed under the cap: got %v, want %v", got, want)
		}
	}
}

// PipelineRun is marshalled into pipeline_states and unmarshalled back by
// crash recovery. The Budget runs once at entry, not on recovery, so a cap
// that does not survive the round trip is a cap that vanishes on restart —
// and budgetCapFiles reads the resulting zero as "uncapped".
func TestPipelineRun_BudgetSurvivesPersistence(t *testing.T) {
	before := &PipelineRun{BudgetMaxFiles: 25, BudgetNote: "reduced for size"}

	payload, err := json.Marshal(before)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var after PipelineRun
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if after.BudgetMaxFiles != 25 {
		t.Errorf("BudgetMaxFiles = %d after a round trip, want 25 — a recovered review would process the whole diff", after.BudgetMaxFiles)
	}
	if after.BudgetNote != "reduced for size" {
		t.Errorf("BudgetNote = %q after a round trip, want it preserved", after.BudgetNote)
	}
}

// A reduced review returns fewer findings over fewer files. Without the note
// the reader cannot tell a quiet review from a narrowed one — the same silent
// outcome the refusal path exists to prevent.
func TestApplyReduce_RecordsTheNoteForTheSummary(t *testing.T) {
	o := &Orchestrator{logger: discardLogger()}
	run := &PipelineRun{
		Diff:       &diff.PatchSet{},
		DeepReview: true,
	}

	o.applyReduce(context.Background(), run, admission.Reduce("120 files changed — reviewing at reduced depth", 40, true))

	if run.BudgetNote == "" {
		t.Error("a reduced review must record why, or the summary cannot say it")
	}
	if run.BudgetMaxFiles != 40 {
		t.Errorf("BudgetMaxFiles = %d, want 40", run.BudgetMaxFiles)
	}
	if run.DeepReview {
		t.Error("ForceShallow must drop the multi-specialist path")
	}
}
