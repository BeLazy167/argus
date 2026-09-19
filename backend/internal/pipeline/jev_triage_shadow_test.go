// jev_triage_shadow_test.go: the shadow contract — observe-only (results are
// never read from Jev answers), spend is billed, agreement is logged, and the
// goroutine always joins inside Execute.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

func triageShadowRun(nFiles int) *PipelineRun {
	run := &PipelineRun{Diff: &diff.PatchSet{}, FeatureFlags: FeatureFlags{JevClassifier: true}}
	for i := 0; i < nFiles; i++ {
		run.Diff.Files = append(run.Diff.Files, diff.FileDiff{
			NewName: fmt.Sprintf("f%d.go", i),
			Status:  diff.FileModified,
			RawDiff: "+x\n",
		})
	}
	return run
}

func triageChoiceResult(n int, pick string) jev.Result {
	answers := map[string]jev.Answer{}
	// A fixed second key that can't collide with pick — a literal "skim"
	// fallback silently overwrote pick's 0.9 when pick was itself "skim".
	other := "skim"
	if pick == other {
		other = "deep"
	}
	for i := 0; i < n; i++ {
		answers[fmt.Sprintf("triage_%d", i)] = jev.Answer{
			Type:   jev.TypeChoice,
			Choice: pick,
			Probabilities: map[string]float64{
				pick: 0.9, other: 0.1,
			},
		}
	}
	return jev.Result{Model: jev.DefaultModel, Answers: answers,
		Usage: jev.Usage{InputTokens: 300, OutputTokens: 5}, Cost: 0.0001}
}

func TestJevTriageShadow_NilJevNoOp(t *testing.T) {
	ts := &TriageStage{}
	run := triageShadowRun(2)
	if s := ts.startJevTriageShadow(context.Background(), run); s != nil {
		t.Fatal("no evaluator path (nil jev + nil store) must not spawn a shadow")
	}
	// join on nil shadow is a no-op — must not block or panic
	ts.finishJevTriageShadow(context.Background(), run, nil, map[string]TriageResult{})
}

func TestJevTriageShadow_OptedOutSkips(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(2, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(2)
	run.FeatureFlags.JevClassifier = false
	s := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	if fj.calls != 0 {
		t.Fatal("opted-out installation must resolve to no evaluator — eval must not run")
	}
	if run.Tokens.Triage.TotalTokens != 0 {
		t.Fatalf("skipped shadow billed tokens: %+v", run.Tokens.Triage)
	}
}

func TestJevTriageShadow_OverCapSkips(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(jevTriageShadowMaxFiles+1, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(jevTriageShadowMaxFiles + 1)
	if s := ts.startJevTriageShadow(context.Background(), run); s != nil || fj.calls != 0 {
		t.Fatal("over-cap diff must skip the shadow")
	}
}

func TestJevTriageShadow_JoinsBillsNeverRoutes(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(2, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(2)

	s := ts.startJevTriageShadow(context.Background(), run)
	// Pipeline's final decision disagrees with Jev on purpose — routing must
	// come from `results`, not the shadow.
	results := map[string]TriageResult{
		"f0.go": {File: "f0.go", Action: TriageSkip},
		"f1.go": {File: "f1.go", Action: TriageDeep},
	}
	ts.finishJevTriageShadow(context.Background(), run, s, results)

	if fj.calls != 1 {
		t.Fatal("shadow eval did not run")
	}
	if run.Tokens.Triage.PromptTokens != 300 || run.Tokens.Total.PromptTokens != 300 {
		t.Fatalf("shadow spend not billed: triage=%+v total=%+v",
			run.Tokens.Triage, run.Tokens.Total)
	}
	// Spend keeps per-leg provenance: the shadow leg lands in the aux ledger
	// with its own model, not anonymously folded under the headline.
	if len(run.Tokens.Triage.Aux) != 1 || run.Tokens.Triage.Aux[0].Model != jev.DefaultModel {
		t.Fatalf("aux ledger = %+v, want the jev leg recorded", run.Tokens.Triage.Aux)
	}
	// The results map is untouched by the shadow.
	if results["f0.go"].Action != TriageSkip {
		t.Fatal("shadow must never mutate routing")
	}
}

// The state Jev classifies must carry the same per-file view the triage
// prompt sees — path, status, added-line count, and a wrapped diff excerpt —
// or agreement comparisons are meaningless.
func TestJevTriageShadow_StateMirrorsTriageView(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(1, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	s := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})

	files, ok := fj.state.(map[string]any)["files"].([]map[string]any)
	if !ok || len(files) != 1 {
		t.Fatalf("state files = %v, want one entry", fj.state)
	}
	f := files[0]
	// added_lines counts parsed hunks — the fixture carries RawDiff only, so
	// 0 is the honest value here; the key contract is the field exists.
	if f["path"] != "f0.go" || f["status"] != "modified" || f["added_lines"] != 0 {
		t.Fatalf("file entry = %+v, want path/status/added_lines", f)
	}
	diff, _ := f["diff"].(string)
	if diff == "" || !strings.Contains(diff, "+x") || !strings.Contains(diff, "<diff>") {
		t.Fatalf("diff excerpt = %q, want wrapped diff content", diff)
	}
}

func TestJevTriageShadow_ErrorStillJoins(t *testing.T) {
	fj := &fakeJev{err: errors.New("jev 529")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	s := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	// reaching here proves the join didn't hang on the error path
}

// A zero-token error result must not stamp the bucket "typesafe" — only
// spend that actually happened brands the headline.
func TestJevTriageShadow_ErrorStampsNoProvider(t *testing.T) {
	fj := &fakeJev{err: errors.New("jev 529")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	s := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})

	if run.Tokens.Triage.Provider != "" || run.Tokens.Triage.Model != "" {
		t.Fatalf("zero-spend error branded the bucket: %+v", run.Tokens.Triage)
	}
	if run.Tokens.Triage.TotalTokens != 0 {
		t.Fatalf("error path billed tokens: %+v", run.Tokens.Triage)
	}
}

// A shadow slower than the join budget is abandoned — Execute must not wait
// the full eval timeout when the real leg finished fast. The in-flight eval
// is cancelled, so a result that never landed can't bill spend later.
func TestJevTriageShadow_SlowShadowAbandoned(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(1, "deep"), delay: 2 * jevShadowJoinBudget}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	s := ts.startJevTriageShadow(context.Background(), run)
	start := time.Now()
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	if elapsed := time.Since(start); elapsed >= 2*jevShadowJoinBudget {
		t.Fatalf("join blocked %v, want <= join budget", elapsed)
	}
	if run.Tokens.Triage.TotalTokens != 0 {
		t.Fatalf("abandoned shadow billed tokens: %+v", run.Tokens.Triage)
	}
}

// An answer landing in the drain window after the join budget expired still
// bills and still compares — the cancel stops the request, but a result that
// was already mid-flight must not silently lose its spend.
// The shadow is constructed directly so the channel timing is deterministic.
func TestJevTriageShadow_LateResultStillBilled(t *testing.T) {
	ts := &TriageStage{}
	run := triageShadowRun(1)
	s := &jevTriageShadow{ch: make(chan jevTriageShadowResult, 1), cancel: func() {}}

	// Deliver just past the join budget: finish's first select times out,
	// the cancel fires, and the drain window catches the landed answer.
	go func() {
		time.Sleep(jevShadowJoinBudget + 20*time.Millisecond)
		s.ch <- jevTriageShadowResult{res: triageChoiceResult(1, "deep")}
	}()
	ts.finishJevTriageShadow(context.Background(), run, s,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	if run.Tokens.Triage.TotalTokens == 0 {
		t.Fatal("answer that raced the abandon must still bill its spend")
	}
}
