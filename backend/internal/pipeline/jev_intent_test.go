// jev_intent_test.go: the intent cascade contract — Jev returns a verdict only
// on a confident all-clear; every uncertain, missing, or failed answer
// escalates to the LLM path with spend still billed.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

func f64(v float64) *float64 { return &v }

// intentRun builds a run with one goal, two criteria, one non-goal, and n
// findings (one comment per file, so the flat enumeration has n entries).
func intentRun(nFindings int) *PipelineRun {
	run := &PipelineRun{
		PRIntent: &PRIntent{
			Goal:               "add rate limiting",
			AcceptanceCriteria: []string{"limits requests", "returns 429"},
			NonGoals:           []string{"billing changes"},
			Source:             IntentSourceAuthor,
		},
		PREvent: github.PREvent{PRNumber: 7, PRTitle: "rate limit", PRAuthor: "dev"},
		Diff: &diff.PatchSet{Files: []diff.FileDiff{
			{NewName: "mw/limit.go", Status: diff.FileModified},
		}},
	}
	for i := 0; i < nFindings; i++ {
		run.FileReviews = append(run.FileReviews, FileReview{
			Path:     fmt.Sprintf("f%d.go", i),
			Comments: []FileComment{{Line: i + 1, What: "finding", Severity: SeverityWarning}},
		})
	}
	return run
}

func allClearIntentResult(nCriteria, nFindings int) jev.Result {
	answers := map[string]jev.Answer{
		"delivers": {Type: jev.TypeNoul, Noul: f64(0.99)},
	}
	for i := 0; i < nCriteria; i++ {
		answers[fmt.Sprintf("criterion_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.98)}
	}
	for i := 0; i < nFindings; i++ {
		answers[fmt.Sprintf("out_of_scope_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.01)}
	}
	return jev.Result{Model: jev.DefaultModel, Answers: answers,
		Usage: jev.Usage{InputTokens: 400, OutputTokens: 8}, Cost: 0.0001}
}

func TestJevIntentVerdict_AllClearConfident(t *testing.T) {
	run := intentRun(2)
	fj := &fakeJev{result: allClearIntentResult(2, 2)}
	o := &Orchestrator{jev: fj, logger: discardLogger()}

	v, spend, confident := o.jevIntentVerdict(context.Background(), run, fj)
	if !confident || v == nil || !v.Delivers {
		t.Fatalf("expected confident all-clear, got %+v confident=%v", v, confident)
	}
	// The verdict must carry a real rationale naming what was verified —
	// downstream renders it, so an empty string would ship a bare banner.
	if v.Rationale == "" || !strings.Contains(v.Rationale, "2 criteria met") {
		t.Fatalf("rationale = %q, want populated summary", v.Rationale)
	}
	if spend.Provider != "typesafe" || spend.Model != jev.DefaultModel ||
		spend.PromptTokens != 400 || spend.TotalTokens != 408 || spend.Cost != 0.0001 {
		t.Fatalf("spend not recorded: %+v", spend)
	}
	if fj.calls != 1 {
		t.Fatalf("jev calls = %d, want 1", fj.calls)
	}
	// SF5: criterion_i questions bind to a NUMBERED criteria list, not the
	// unnumbered bullets inside the intent block.
	criteria, ok := fj.state.(map[string]any)["acceptance_criteria"].([]string)
	if !ok || len(criteria) != 2 || criteria[0] != "0: limits requests" || criteria[1] != "1: returns 429" {
		t.Fatalf("acceptance_criteria state = %v, want numbered entries", fj.state)
	}
	// files_changed must enumerate the diff — an empty list means the
	// all-clear answered without seeing what the PR touched.
	files, ok := fj.state.(map[string]any)["files_changed"].([]string)
	if !ok || len(files) != 1 || !strings.Contains(files[0], "mw/limit.go") {
		t.Fatalf("files_changed state = %v, want the diff's files", fj.state)
	}
}

func TestJevIntentVerdict_Escalates(t *testing.T) {
	cases := map[string]func(jev.Result) jev.Result{
		"delivers_uncertain": func(r jev.Result) jev.Result {
			r.Answers["delivers"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.6)}
			return r
		},
		"delivers_confident_no": func(r jev.Result) jev.Result {
			// Confident delivers=false needs LLM rationale prose — escalate.
			r.Answers["delivers"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.02)}
			return r
		},
		"criterion_unmet": func(r jev.Result) jev.Result {
			r.Answers["criterion_0"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.10)}
			return r
		},
		"finding_out_of_scope": func(r jev.Result) jev.Result {
			r.Answers["out_of_scope_1"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.9)}
			return r
		},
		"answer_missing": func(r jev.Result) jev.Result {
			delete(r.Answers, "criterion_1")
			return r
		},
		"answer_out_of_range": func(r jev.Result) jev.Result {
			r.Answers["delivers"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(1.7)}
			return r
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			run := intentRun(2)
			fj := &fakeJev{result: mutate(allClearIntentResult(2, 2))}
			o := &Orchestrator{jev: fj, logger: discardLogger()}
			v, spend, confident := o.jevIntentVerdict(context.Background(), run, fj)
			if confident || v != nil {
				t.Fatalf("expected escalation, got %+v confident=%v", v, confident)
			}
			if spend.PromptTokens != 400 {
				t.Fatalf("jev spend must still be billed: %+v", spend)
			}
		})
	}
}

func TestJevIntentVerdict_ErrorEscalates(t *testing.T) {
	run := intentRun(1)
	fj := &fakeJev{err: errors.New("jev 529")}
	o := &Orchestrator{jev: fj, logger: discardLogger()}

	v, _, confident := o.jevIntentVerdict(context.Background(), run, fj)
	if confident || v != nil {
		t.Fatal("jev error must escalate")
	}
}

func TestJevIntentVerdict_TooManyFindingsSkipsEval(t *testing.T) {
	run := intentRun(jevIntentMaxFindings + 1)
	fj := &fakeJev{result: allClearIntentResult(2, jevIntentMaxFindings+1)}
	o := &Orchestrator{jev: fj, logger: discardLogger()}

	if _, _, confident := o.jevIntentVerdict(context.Background(), run, fj); confident {
		t.Fatal("over-cap findings must escalate")
	}
	if fj.calls != 0 {
		t.Fatal("over-cap run must not spend an eval")
	}
}

func TestJevIntentVerdict_TooManyCriteriaSkipsEval(t *testing.T) {
	run := intentRun(1)
	run.PRIntent.AcceptanceCriteria = make([]string, jevIntentMaxCriteria+1)
	for i := range run.PRIntent.AcceptanceCriteria {
		run.PRIntent.AcceptanceCriteria[i] = fmt.Sprintf("c%d", i)
	}
	fj := &fakeJev{result: allClearIntentResult(jevIntentMaxCriteria+1, 1)}
	o := &Orchestrator{jev: fj, logger: discardLogger()}

	if _, _, confident := o.jevIntentVerdict(context.Background(), run, fj); confident {
		t.Fatal("over-cap criteria must escalate")
	}
	if fj.calls != 0 {
		t.Fatal("over-cap criteria run must not spend an eval")
	}
}
