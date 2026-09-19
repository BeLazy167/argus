// jev_scoring_test.go: the FP pre-filter contract — a finding leaves the judge
// prompt only on confident-fp AND not-a-defect; every other outcome keeps it,
// and survivor re-indexing maps prompt positions back to flat indices.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/llm"
)

// scoringRun builds a run whose FileReviews have the given per-file comment
// counts; flat index order is f0 comments, f1 comments, ...
func scoringRun(commentsPerFile ...int) *PipelineRun {
	run := &PipelineRun{
		PREvent:      github.PREvent{PRNumber: 7, PRTitle: "t", PRAuthor: "dev"},
		FeatureFlags: FeatureFlags{JevClassifier: true},
	}
	for fi, n := range commentsPerFile {
		fr := FileReview{Path: fmt.Sprintf("f%d.go", fi)}
		for ci := 0; ci < n; ci++ {
			fr.Comments = append(fr.Comments, FileComment{
				Line: ci + 1, What: fmt.Sprintf("f%d c%d", fi, ci), Severity: SeverityWarning,
			})
		}
		run.FileReviews = append(run.FileReviews, fr)
	}
	return run
}

func flatIndex(run *PipelineRun) []indexedComment {
	var out []indexedComment
	for fi, fr := range run.FileReviews {
		for ci := range fr.Comments {
			out = append(out, indexedComment{fileIdx: fi, commentIdx: ci})
		}
	}
	return out
}

// scoringJevResult marks the given flat indices as confident FPs and the rest
// as definite defects (kept).
func scoringJevResult(n int, dropIdx ...int) jev.Result {
	dropped := map[int]bool{}
	for _, i := range dropIdx {
		dropped[i] = true
	}
	answers := map[string]jev.Answer{}
	for i := 0; i < n; i++ {
		if dropped[i] {
			answers[fmt.Sprintf("fp_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.99)}
			answers[fmt.Sprintf("defect_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.1)}
		} else {
			answers[fmt.Sprintf("fp_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.2)}
			answers[fmt.Sprintf("defect_%d", i)] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.95)}
		}
	}
	return jev.Result{Model: jev.DefaultModel, Answers: answers,
		Usage: jev.Usage{InputTokens: 900, OutputTokens: 20}, Cost: 0.0002}
}

func TestJevScoringPreFilter_DropsOnlyConfidentFPs(t *testing.T) {
	run := scoringRun(2, 2) // 4 findings: idx 0..3
	ss := &ScoringStage{jev: &fakeJev{result: scoringJevResult(4, 1, 3)}}

	dropped, spend := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 2 || dropped[1] == "" || dropped[3] == "" {
		t.Fatalf("expected idx 1,3 dropped, got %v", dropped)
	}
	if spend.PromptTokens != 900 || spend.Provider != "typesafe" {
		t.Fatalf("spend not recorded: %+v", spend)
	}
}

func TestJevScoringPreFilter_DefectProbVetosDrop(t *testing.T) {
	run := scoringRun(1)
	res := scoringJevResult(1, 0)
	// FP-confidence alone is not enough: defect p=0.7 keeps the finding.
	res.Answers["defect_0"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(0.7)}
	ss := &ScoringStage{jev: &fakeJev{result: res}}

	dropped, _ := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 0 {
		t.Fatalf("finding with defect p=0.7 must survive, dropped %v", dropped)
	}
}

func TestJevScoringPreFilter_MissingAndBadAnswersKeep(t *testing.T) {
	run := scoringRun(3)
	res := scoringJevResult(3)
	delete(res.Answers, "fp_1")                                          // missing fp → keep
	res.Answers["fp_2"] = jev.Answer{Type: jev.TypeNoul, Noul: f64(1.7)} // out of range → keep
	ss := &ScoringStage{jev: &fakeJev{result: res}}

	dropped, _ := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 0 {
		t.Fatalf("missing/out-of-range answers must keep findings, dropped %v", dropped)
	}
}

func TestJevScoringPreFilter_ErrorDropsNothing(t *testing.T) {
	run := scoringRun(2)
	ss := &ScoringStage{jev: &fakeJev{err: errors.New("jev 429")}}

	dropped, spend := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 0 {
		t.Fatalf("jev error must drop nothing, dropped %v", dropped)
	}
	_ = spend // spend still returned for billing
}

func TestJevScoringPreFilter_OptedOutSkipsEval(t *testing.T) {
	run := scoringRun(2)
	run.FeatureFlags.JevClassifier = false
	fj := &fakeJev{result: scoringJevResult(2, 0, 1)}
	ss := &ScoringStage{jev: fj}

	dropped, spend := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 0 || fj.calls != 0 {
		t.Fatal("opted-out installation must skip the eval entirely")
	}
	if spend.PromptTokens != 0 || spend.Provider != "" {
		t.Fatalf("skipped eval must book zero spend, got %+v", spend)
	}
}

func TestJevScoringPreFilter_OverCapSkipsEval(t *testing.T) {
	run := scoringRun(jevScoringMaxFindings + 1)
	fj := &fakeJev{result: scoringJevResult(jevScoringMaxFindings + 1)}
	ss := &ScoringStage{jev: fj}

	dropped, _ := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
	if len(dropped) != 0 || fj.calls != 0 {
		t.Fatal("over-cap run must skip the eval and drop nothing")
	}
}

// Boundary table: the drop requires BOTH the fp floor AND the defect ceiling
// — at/inside drops, just outside keeps.
func TestJevScoringPreFilter_FloorBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		fp     float64
		defect float64
		drop   bool
	}{
		{"exactly at floor and ceiling", 0.97, 0.5, true},
		{"fp just below floor", 0.9699, 0.1, false},
		{"defect just above ceiling", 0.99, 0.51, false},
		{"well inside both", 0.99, 0.1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := scoringRun(1)
			res := jev.Result{Model: jev.DefaultModel, Answers: map[string]jev.Answer{
				"fp_0":     {Type: jev.TypeNoul, Noul: f64(tc.fp)},
				"defect_0": {Type: jev.TypeNoul, Noul: f64(tc.defect)},
			}}
			ss := &ScoringStage{jev: &fakeJev{result: res}}
			dropped, _ := ss.jevScoringPreFilter(context.Background(), run, flatIndex(run))
			if got := len(dropped) == 1; got != tc.drop {
				t.Fatalf("fp=%.4f defect=%.4f dropped=%v, want %v", tc.fp, tc.defect, got, tc.drop)
			}
		})
	}
}

// Execute-level: a confident Jev FP drop must not reach the judge prompt and
// must not be resurrected by adjustScores floors — the dropped finding is
// SAST-corroborated, which would floor it at 75 (inline) if synthetic groups
// were appended before adjustScores instead of after.
func TestScoringStage_JevDropCannotResurrect(t *testing.T) {
	server, llmCalls := judgeStubServer(t) // judges representative 0 → survivor 0 → flat 0
	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{baseURL: server.URL})
	ss := &ScoringStage{
		registry: registry,
		cfgLister: staticConfigLister{configs: []llm.ModelConfig{
			{Stage: llm.StageScoring, Provider: "openrouter", Model: "test-model", MaxTokens: 1000, Temperature: 0.1},
		}},
		jev: &fakeJev{result: scoringJevResult(2, 1)}, // drop flat 1
	}
	run := scoringTestRun()
	run.FeatureFlags.JevClassifier = true
	run.FileReviews[0].Comments = append(run.FileReviews[0].Comments, FileComment{
		Line: 2, What: "SAST false alarm", Severity: SeverityWarning, SastCorroborated: true,
	})

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fj := ss.jev.(*fakeJev); fj.calls != 1 {
		t.Fatalf("jev calls = %d, want 1", fj.calls)
	}
	if llmCalls.Load() != 1 {
		t.Fatalf("llm calls = %d, want 1", llmCalls.Load())
	}
	// The SAST-corroborated drop stays dropped: one inline comment, scored 90.
	kept := run.FileReviews[0].Comments
	if len(kept) != 1 || kept[0].What != "nil deref on error path" || kept[0].Score != 90 {
		t.Fatalf("inline comments = %+v, want only the real finding at 90", kept)
	}
	// Audit trail: AllFileReviews keeps both, the dropped one at the synthetic score.
	if len(run.AllFileReviews) != 1 || len(run.AllFileReviews[0].Comments) != 2 {
		t.Fatalf("AllFileReviews = %+v, want both findings", run.AllFileReviews)
	}
	if got := run.AllFileReviews[0].Comments[1].Score; got != jevScoringDropScore {
		t.Fatalf("dropped score = %d, want %d (SAST floor must not apply)", got, jevScoringDropScore)
	}
	// Spend: Jev leg + LLM leg summed; headline stamps the deciding LLM call.
	if run.Tokens.Scoring.PromptTokens != 900+10 || run.Tokens.Scoring.Model != "test-model" {
		t.Fatalf("scoring tokens = %+v, want jev+llm summed with llm headline", run.Tokens.Scoring)
	}
}

func TestSurvivingFileReviews_Reindexes(t *testing.T) {
	run := scoringRun(2, 1, 2) // flat: 0,1 in f0; 2 in f1; 3,4 in f2
	all := flatIndex(run)
	dropped := map[int]string{1: "fp", 3: "fp"}

	reviews, survivors := survivingFileReviews(run, all, dropped)

	wantSurv := []int{0, 2, 4}
	if len(survivors) != len(wantSurv) {
		t.Fatalf("survivors = %v, want %v", survivors, wantSurv)
	}
	for i, w := range wantSurv {
		if survivors[i] != w {
			t.Fatalf("survivors[%d] = %d, want %d", i, survivors[i], w)
		}
	}
	// Survivor view keeps per-file order minus dropped comments.
	if len(reviews[0].Comments) != 1 || reviews[0].Comments[0].What != "f0 c0" {
		t.Fatalf("f0 survivor view wrong: %+v", reviews[0].Comments)
	}
	if len(reviews[1].Comments) != 1 {
		t.Fatalf("f1 should keep its only comment: %+v", reviews[1].Comments)
	}
	if len(reviews[2].Comments) != 1 || reviews[2].Comments[0].What != "f2 c1" {
		t.Fatalf("f2 survivor view wrong: %+v", reviews[2].Comments)
	}
	// Prompt built over the survivor view must index [0..len(survivors)) —
	// finding lines are "[idx] path:line", distinct from the "[3]," template.
	prompt := buildScoringPromptFor(run, reviews, "")
	if strings.Contains(prompt, "[3] ") || strings.Contains(prompt, "[4] ") {
		t.Fatalf("prompt indices must be re-compacted, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[2] f2.go:2") {
		t.Fatalf("prompt missing survivor at compacted index 2:\n%s", prompt)
	}
}

func TestSurvivingFileReviews_NoDrops(t *testing.T) {
	run := scoringRun(2)
	reviews, survivors := survivingFileReviews(run, flatIndex(run), nil)
	if len(survivors) != 2 || len(reviews[0].Comments) != 2 {
		t.Fatal("no drops must preserve the full view")
	}
}
