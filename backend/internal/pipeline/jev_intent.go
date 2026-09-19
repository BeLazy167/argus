// jev_intent.go: Jev front-runner for intent verification.
//
// verifyIntent asks the LLM for IntentVerdict{delivers, rationale,
// unmet_criteria[], out_of_scope_finding_ids[]}. Only the all-clear outcome
// is Jev-decidable: a confident delivers=true needs no rationale prose, no
// named unmet criteria, and no demotions. Anything else escalates — a wrong
// delivers=false emits a loud banner the LLM must justify in prose, and a
// wrong demotion drops a real finding a severity rung, so both live behind
// the same "uncertain → LLM" arm.
//
// One batched eval: `delivers`, one noul per acceptance criterion, one noul
// per finding (in-scope check against the declared non-goals). Jev answers
// only when EVERY signal is confident; a single nil/uncertain answer sends
// the whole verdict to the LLM — partial verdicts don't exist downstream.
package pipeline

import (
	"context"
	"fmt"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/util"
)

const (
	// jevIntentConfidentFloor gates the all-clear verdict (delivers and every
	// acceptance criterion met).
	jevIntentConfidentFloor = 0.95
	// jevIntentInScopeCeil gates the per-finding scope check: below it the
	// finding is confidently in scope. Low bar is safe — a missed demotion
	// just posts the finding at full severity, the status quo.
	jevIntentInScopeCeil = 0.05
	// Bounds keep one question per entry from blowing the state cap.
	jevIntentMaxCriteria = 20
	jevIntentMaxFindings = 50
)

// jevIntentState carries the same payload BuildIntentVerificationPrompt
// renders, but structured: the intent block (fields already sanitized by
// RenderPrompt), the file list, and the flat finding listing under the same
// positional ids DemoteOutOfScopeFindings uses.
func jevIntentState(run *PipelineRun) map[string]any {
	files := []string{}
	if run.Diff != nil {
		for _, f := range run.Diff.Files {
			files = append(files, fmt.Sprintf("%s (%s)", f.NewName, f.Status))
		}
	}
	findings := []string{}
	id := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			desc := c.What
			if desc == "" {
				desc = c.Body
			}
			findings = append(findings, fmt.Sprintf("%d: [%s] %s:%d — %s",
				id, c.Severity, fr.Path, c.Line, sanitizeUserInput(util.Truncate(desc, 200, true))))
			id++
		}
	}
	// Numbered criteria: criterion_i questions bind to THIS list. The intent
	// block renders them as unnumbered bullets, so positional references
	// against it would be guesswork.
	criteria := make([]string, len(run.PRIntent.AcceptanceCriteria))
	for i, c := range run.PRIntent.AcceptanceCriteria {
		criteria[i] = fmt.Sprintf("%d: %s", i, safeIntentField(util.Truncate(c, 300, true)))
	}
	return map[string]any{
		"intent":              run.PRIntent.RenderPrompt(),
		"acceptance_criteria": criteria,
		"files_changed":       files,
		"findings":            findings,
	}
}

// jevIntentQuestions builds delivers + criterion_i + out_of_scope_i nouls.
// Instructions reference state keys by name so the model binds them directly.
func jevIntentQuestions(intent *PRIntent, findingCount int) map[string]jev.Question {
	qs := map[string]jev.Question{
		"delivers": jev.NoulQuestion(
			"Based on `intent` (the author's stated goal) and `findings` (what reviewers observed), does the PR's `files_changed` deliver the stated goal?",
			"The changed files accomplish the stated goal; no finding indicates the goal is unmet",
			"The stated goal is not accomplished, or key work is missing"),
	}
	for i := range intent.AcceptanceCriteria {
		qs[fmt.Sprintf("criterion_%d", i)] = jev.NoulQuestion(
			fmt.Sprintf("Is `acceptance_criteria` entry %d satisfied by this PR's changes?", i),
			"The diff clearly satisfies that criterion",
			"That criterion is not satisfied or there is no evidence for it")
	}
	for i := 0; i < findingCount; i++ {
		qs[fmt.Sprintf("out_of_scope_%d", i)] = jev.NoulQuestion(
			fmt.Sprintf("Does `findings` entry %d concern only work the `intent` block declares 'Not in scope'?", i),
			"The finding's subject is entirely within a declared non-goal",
			"The finding is in scope, partially in scope, or unclear")
	}
	return qs
}

// jevIntentVerdict runs the batched eval on the resolved evaluator and
// returns an all-clear verdict only when every answer is confidently clean.
// The StageTokens spend is returned regardless — the caller bills it even
// when the verdict escalates.
func (o *Orchestrator) jevIntentVerdict(ctx context.Context, run *PipelineRun, j jevEvaluator) (*IntentVerdict, StageTokens, bool) {
	nCriteria := len(run.PRIntent.AcceptanceCriteria)
	nFindings := countFlatComments(run)
	if nCriteria > jevIntentMaxCriteria || nFindings > jevIntentMaxFindings {
		return nil, StageTokens{}, false
	}

	jevCtx, cancel := context.WithTimeout(ctx, jevEvalTimeout)
	res, err := j.Evaluate(jevCtx, jevIntentState(run), jevIntentQuestions(run.PRIntent, nFindings), "intent_verify")
	cancel()
	spend := jevStageTokens(res)
	if err != nil {
		o.logger.WarnContext(ctx, "jev intent eval failed, escalating to llm", "error", err.Error())
		return nil, spend, false
	}

	delivers := res.Noul("delivers")
	if delivers == nil || *delivers < jevIntentConfidentFloor {
		return nil, spend, false
	}
	for i := 0; i < nCriteria; i++ {
		if met := res.Noul(fmt.Sprintf("criterion_%d", i)); met == nil || *met < jevIntentConfidentFloor {
			return nil, spend, false
		}
	}
	for i := 0; i < nFindings; i++ {
		if oos := res.Noul(fmt.Sprintf("out_of_scope_%d", i)); oos == nil || *oos > jevIntentInScopeCeil {
			return nil, spend, false
		}
	}
	return &IntentVerdict{
		Delivers:  true,
		Rationale: fmt.Sprintf("jev: delivers p=%.2f, %d criteria met, %d findings in scope", *delivers, nCriteria, nFindings),
	}, spend, true
}
