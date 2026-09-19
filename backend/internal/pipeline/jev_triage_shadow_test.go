// jev_triage_shadow_test.go: the shadow contract — observe-only (results are
// never read from Jev answers), spend is billed, agreement is logged, and the
// goroutine always joins inside Execute.
package pipeline

import (
	"context"
	"errors"
	"fmt"
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
	for i := 0; i < n; i++ {
		answers[fmt.Sprintf("triage_%d", i)] = jev.Answer{
			Type:   jev.TypeChoice,
			Choice: pick,
			Probabilities: map[string]float64{
				pick: 0.9, "skim": 0.1,
			},
		}
	}
	return jev.Result{Model: jev.DefaultModel, Answers: answers,
		Usage: jev.Usage{InputTokens: 300, OutputTokens: 5}, Cost: 0.0001}
}

func TestJevTriageShadow_NilJevNoOp(t *testing.T) {
	ts := &TriageStage{}
	run := triageShadowRun(2)
	if ch := ts.startJevTriageShadow(context.Background(), run); ch != nil {
		t.Fatal("nil jev must not spawn a shadow")
	}
	// join on nil channel is a no-op — must not block or panic
	ts.finishJevTriageShadow(context.Background(), run, nil, map[string]TriageResult{})
}

func TestJevTriageShadow_OptedOutSkips(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(2, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(2)
	run.FeatureFlags.JevClassifier = false
	if ch := ts.startJevTriageShadow(context.Background(), run); ch != nil || fj.calls != 0 {
		t.Fatal("opted-out installation must not spawn a shadow")
	}
}

func TestJevTriageShadow_OverCapSkips(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(jevTriageShadowMaxFiles+1, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(jevTriageShadowMaxFiles + 1)
	if ch := ts.startJevTriageShadow(context.Background(), run); ch != nil || fj.calls != 0 {
		t.Fatal("over-cap diff must skip the shadow")
	}
}

func TestJevTriageShadow_JoinsBillsNeverRoutes(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(2, "deep")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(2)

	ch := ts.startJevTriageShadow(context.Background(), run)
	// Pipeline's final decision disagrees with Jev on purpose — routing must
	// come from `results`, not the shadow.
	results := map[string]TriageResult{
		"f0.go": {File: "f0.go", Action: TriageSkip},
		"f1.go": {File: "f1.go", Action: TriageDeep},
	}
	ts.finishJevTriageShadow(context.Background(), run, ch, results)

	if fj.calls != 1 {
		t.Fatal("shadow eval did not run")
	}
	if run.Tokens.Triage.PromptTokens != 300 || run.Tokens.Total.PromptTokens != 300 {
		t.Fatalf("shadow spend not billed: triage=%+v total=%+v",
			run.Tokens.Triage, run.Tokens.Total)
	}
	// The results map is untouched by the shadow.
	if results["f0.go"].Action != TriageSkip {
		t.Fatal("shadow must never mutate routing")
	}
}

func TestJevTriageShadow_ErrorStillJoins(t *testing.T) {
	fj := &fakeJev{err: errors.New("jev 529")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	ch := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, ch,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	// reaching here proves the join didn't hang on the error path
}

// A zero-token error result must not stamp the bucket "typesafe" — only
// spend that actually happened brands the headline.
func TestJevTriageShadow_ErrorStampsNoProvider(t *testing.T) {
	fj := &fakeJev{err: errors.New("jev 529")}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	ch := ts.startJevTriageShadow(context.Background(), run)
	ts.finishJevTriageShadow(context.Background(), run, ch,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})

	if run.Tokens.Triage.Provider != "" || run.Tokens.Triage.Model != "" {
		t.Fatalf("zero-spend error branded the bucket: %+v", run.Tokens.Triage)
	}
	if run.Tokens.Triage.TotalTokens != 0 {
		t.Fatalf("error path billed tokens: %+v", run.Tokens.Triage)
	}
}

// A shadow slower than the join budget is abandoned — Execute must not wait
// the full eval timeout when the real leg finished fast.
func TestJevTriageShadow_SlowShadowAbandoned(t *testing.T) {
	fj := &fakeJev{result: triageChoiceResult(1, "deep"), delay: 2 * jevShadowJoinBudget}
	ts := &TriageStage{jev: fj}
	run := triageShadowRun(1)

	ch := ts.startJevTriageShadow(context.Background(), run)
	start := time.Now()
	ts.finishJevTriageShadow(context.Background(), run, ch,
		map[string]TriageResult{"f0.go": {File: "f0.go", Action: TriageDeep}})
	if elapsed := time.Since(start); elapsed >= 2*jevShadowJoinBudget {
		t.Fatalf("join blocked %v, want <= join budget", elapsed)
	}
	if run.Tokens.Triage.TotalTokens != 0 {
		t.Fatalf("abandoned shadow billed tokens: %+v", run.Tokens.Triage)
	}
}
