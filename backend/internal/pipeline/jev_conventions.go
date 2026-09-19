// jev_conventions.go: Jev front-runner for the convention-relation classifier.
//
// classifyConventionRelations decides what an extracted convention means for
// stored memory — duplicate (record evidence, skip insert), refines
// (supersede the old doc), contradicts (file a user-visible conflict), or
// unrelated (insert active). That is a closed 4-way choice per existing
// convention, exactly Jev-shaped.
//
// Cascade policy: Jev answers every neighbor in ONE batched eval (shared
// state = candidate + all neighbors, one choice question per neighbor). If
// every answer's top probability clears jevConventionConfidenceFloor the LLM
// call is skipped; ANY uncertain or missing answer escalates the whole batch
// to the LLM — the LLM judges all neighbors in one call anyway, so partial
// application buys nothing. Jev error escalates: the fallback is the retry.
// "contradicts" files a user-facing conflict, which is why the floor is 0.95
// rather than the consumer's looser 0.80 — that downstream gate still applies
// unchanged to whatever confidence we return.
package pipeline

import (
	"context"
	"fmt"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// jevConventionConfidenceFloor is the per-neighbor top-probability required
// to skip the LLM. Below it the whole batch escalates — see file header.
const jevConventionConfidenceFloor = 0.95

// jevConventionQuestions builds one choice question per neighbor, all sharing
// one state. Question ids are caller-chosen keys (never sent to the model);
// the instructions carry the state key the question judges.
func jevConventionQuestions(neighbors []memory.PatternMatch) map[string]jev.Question {
	criteria := map[string]string{
		string(conventionDuplicate):   "the existing convention states the same rule as the candidate",
		string(conventionRefines):     "the candidate is a more specific or newer compatible replacement for the existing convention",
		string(conventionContradicts): "the candidate and the existing convention cannot both be enforced together",
		string(conventionUnrelated):   "the conventions cover different concerns",
	}
	questions := make(map[string]jev.Question, len(neighbors))
	for i := range neighbors {
		questions[fmt.Sprintf("rel_%d", i)] = jev.Question{
			Type: jev.TypeChoice,
			Instructions: fmt.Sprintf(
				"Classify the relation between candidate_convention and existing_convention_%d.", i),
			Criteria: criteria,
		}
	}
	return questions
}

// jevConventionFieldCap bounds each convention in the Jev state (System One's
// per-eval state limit) — Jev-only; the LLM classifier prompt stays unbounded
// so enabling Jev never shrinks the existing prompt.
const jevConventionFieldCap = 2000

// jevConventionState packs the candidate plus each neighbor under stable keys.
// conventionPromptField applies the repo's prompt-safety idiom (injection
// redaction + NUL strip + tag-scrub + delimiter wrap) to both fields — stored
// conventions are user-controlled text persisted across reviews.
func jevConventionState(candidate string, neighbors []memory.PatternMatch) map[string]any {
	state := map[string]any{
		"candidate_convention": conventionPromptField("candidate_convention", util.Truncate(candidate, jevConventionFieldCap, false)),
	}
	for i, n := range neighbors {
		state[fmt.Sprintf("existing_convention_%d", i)] =
			conventionPromptField("existing_convention", util.Truncate(n.Content, jevConventionFieldCap, false))
	}
	return state
}

// jevConventionRelations runs the batched eval and maps answers back onto
// neighbors. Returns ok=false when anything is uncertain, missing, or
// malformed — the caller then runs the LLM classifier exactly as before.
func (o *Orchestrator) jevConventionRelations(ctx context.Context, j jevEvaluator, candidate string, neighbors []memory.PatternMatch) ([]conventionRelationResult, StageTokens, bool) {
	jevCtx, cancel := context.WithTimeout(ctx, jevEvalTimeout)
	res, err := j.Evaluate(jevCtx, jevConventionState(candidate, neighbors), jevConventionQuestions(neighbors), "convention_conflicts")
	cancel()
	spend := jevStageTokens(res)
	if err != nil {
		o.logger.WarnContext(ctx, "jev convention eval failed, escalating to llm", "error", err.Error())
		return nil, spend, false
	}
	out := make([]conventionRelationResult, 0, len(neighbors))
	for i, n := range neighbors {
		choice, prob, ok := res.Choice(fmt.Sprintf("rel_%d", i))
		if !ok || prob < jevConventionConfidenceFloor {
			return nil, spend, false
		}
		rel := conventionRelation(choice)
		switch rel {
		case conventionDuplicate, conventionRefines, conventionContradicts, conventionUnrelated:
		default:
			return nil, spend, false
		}
		out = append(out, conventionRelationResult{ExistingID: n.ID, Relation: rel, Confidence: prob})
	}
	return out, spend, true
}
