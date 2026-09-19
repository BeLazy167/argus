// jev_triage_shadow.go: observe-only Jev eval alongside triage.
//
// Triage's per-file depth call (skip/skim/security_skim/deep) is Jev-shaped,
// but routing coverage is safety-critical — a wrong "skip" removes review
// entirely. So Jev classifies the same files in parallel with the real
// pipeline and its answers are only logged and compared, never used. The
// `jev.shadow.triage` agreement log is the calibration data needed before
// triage can become a real cascade.
//
// The eval runs on a goroutine spawned before the LLM leg and joined inside
// Execute with a bounded wait (jevShadowJoinBudget), so all run access stays
// on Execute's goroutine and added latency is ~0: Jev answers in ~300ms, and
// a slow shadow is abandoned rather than blocking a heuristic-only triage.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// jevTriageShadowMaxFiles bounds question count and state size. Larger diffs
// skip the shadow entirely (scoring pre-filter precedent).
const jevTriageShadowMaxFiles = 40

// jevShadowJoinBudget caps how long Execute waits on a shadow that hasn't
// answered by the time the real leg finished. Jev evals answer in ~300ms, so
// the typical join is instant; the budget keeps a slow eval from adding
// seconds to a heuristic-only triage — a missed comparison is just less
// calibration data, never wrong behavior.
const jevShadowJoinBudget = 750 * time.Millisecond

type jevTriageShadowResult struct {
	res jev.Result
	err error
}

// startJevTriageShadow builds the eval synchronously (pure) and fires it on a
// goroutine. Returns nil when the shadow doesn't run — the join is a no-op.
func (ts *TriageStage) startJevTriageShadow(ctx context.Context, run *PipelineRun) chan jevTriageShadowResult {
	if run.Diff == nil || len(run.Diff.Files) == 0 || len(run.Diff.Files) > jevTriageShadowMaxFiles {
		return nil
	}
	j := resolveJevEvaluator(ctx, jevKeyReaderFor(ts.store), ts.jev, run.DBInstallationID, &run.DBRepoID, run.FeatureFlags)
	if j == nil {
		return nil
	}
	state := jevTriageShadowState(run)
	questions := jevTriageShadowQuestions(len(run.Diff.Files))
	ch := make(chan jevTriageShadowResult, 1)
	go func() {
		jevCtx, cancel := context.WithTimeout(ctx, jevEvalTimeout)
		defer cancel()
		res, err := j.Evaluate(jevCtx, state, questions, "triage_shadow")
		ch <- jevTriageShadowResult{res: res, err: err}
	}()
	return ch
}

// finishJevTriageShadow joins the shadow goroutine with a bounded wait, bills
// spend into the Triage bucket, and logs the per-file agreement comparison.
// An unanswered shadow within jevShadowJoinBudget is abandoned — the goroutine
// still drains into its buffered channel, nothing leaks.
func (ts *TriageStage) finishJevTriageShadow(ctx context.Context, run *PipelineRun, ch chan jevTriageShadowResult, results map[string]TriageResult) {
	if ch == nil {
		return
	}
	var out jevTriageShadowResult
	select {
	case out = <-ch:
	case <-time.After(jevShadowJoinBudget):
		slog.InfoContext(ctx, "jev triage shadow abandoned",
			slog.String("event", "jev.shadow.triage_abandoned"),
			slog.Int("file_count", len(run.Diff.Files)),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
		return
	}
	spend := jevStageTokens(out.res)
	run.Tokens.Triage.PromptTokens += spend.PromptTokens
	run.Tokens.Triage.CompletionTokens += spend.CompletionTokens
	run.Tokens.Triage.TotalTokens += spend.TotalTokens
	run.Tokens.Triage.Cost += spend.Cost
	// Stamp only spend that actually happened — a zero-token error result
	// must not brand the bucket "typesafe".
	if run.Tokens.Triage.Model == "" && spend.TotalTokens > 0 {
		run.Tokens.Triage.Model = out.res.Model
		run.Tokens.Triage.Provider = "typesafe"
	}
	run.Tokens.addToTotal(spend)

	if out.err != nil {
		slog.WarnContext(ctx, "jev triage shadow failed", "error", out.err, "pr", run.PREvent.PRNumber)
		return
	}

	var agreed, disagreed, unanswered int
	type disagreement struct {
		File     string  `json:"file"`
		Jev      string  `json:"jev"`
		JevProb  float64 `json:"jev_prob"`
		Pipeline string  `json:"pipeline"`
	}
	var diffs []disagreement
	for i, f := range run.Diff.Files {
		pipeline := string(results[f.NewName].Action)
		choice, prob, ok := out.res.Choice(fmt.Sprintf("triage_%d", i))
		switch {
		case !ok:
			unanswered++
		case choice == pipeline:
			agreed++
		default:
			disagreed++
			diffs = append(diffs, disagreement{File: f.NewName, Jev: choice, JevProb: prob, Pipeline: pipeline})
		}
	}
	slog.InfoContext(ctx, "jev triage shadow comparison",
		slog.String("event", "jev.shadow.triage"),
		slog.String("model", out.res.Model),
		slog.Int("file_count", len(run.Diff.Files)),
		slog.Int("agreed", agreed),
		slog.Int("disagreed", disagreed),
		slog.Int("unanswered", unanswered),
		slog.Float64("cost_usd", out.res.Cost),
		slog.String("trace_id", obs.TraceID(ctx)),
	)
	if len(diffs) > 0 {
		if payload, err := json.Marshal(diffs); err == nil {
			obs.LogPayload(ctx, slog.Default(), "Jev triage shadow disagreements", obs.NewLogID(), "result", "application/json", payload)
		}
	}
}

// jevTriageShadowState mirrors the triage prompt's per-file view: path, status,
// added-line count, and a bounded wrapped diff excerpt, plus the resolved
// review contract so agreement is judged against the same routing context.
func jevTriageShadowState(run *PipelineRun) map[string]any {
	files := make([]map[string]any, 0, len(run.Diff.Files))
	for _, f := range run.Diff.Files {
		files = append(files, map[string]any{
			"path":        f.NewName,
			"status":      string(f.Status),
			"added_lines": countAddedLines(f),
			"diff":        wrapSafeDelimiters("diff", util.Truncate(truncateLines(f.RawDiff, 50), 2400, true)),
		})
	}
	state := map[string]any{"files": files}
	if run.Contract != nil {
		state["review_contract"] = wrapInDelimiters("review_contract", run.Contract.SummaryLine())
	}
	return state
}

// jevTriageShadowQuestions emits one choice per file; criteria keys equal the
// TriageAction strings so res.Choice maps directly onto the pipeline's verbs.
func jevTriageShadowQuestions(n int) map[string]jev.Question {
	criteria := map[string]string{
		string(TriageSkip):         "generated, vendored, lockfile, binary, or asset; pure rename; config with no logic",
		string(TriageSkim):         "small change under ~20 lines, typo, style-only, docs, or minor no-behavior refactor",
		string(TriageSecuritySkim): "auth, session, crypto, permissions, or input validation needing a security pass rather than full review",
		string(TriageDeep):         "business logic, public API surface, error handling, DB/network/concurrency, control flow, or a large change",
	}
	qs := make(map[string]jev.Question, n)
	for i := 0; i < n; i++ {
		qs[fmt.Sprintf("triage_%d", i)] = jev.Question{
			Type: jev.TypeChoice,
			Instructions: fmt.Sprintf(
				"Classify `files` entry %d (path, status, added_lines, diff) into the review depth it should get.", i),
			Criteria: criteria,
		}
	}
	return qs
}
