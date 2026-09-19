// jev_addressed_judge.go: Jev front-runner for the AddressedJudge port.
//
// Jev (TypeSafe System One) is a typed classifier: state in, probabilities
// out, no prose. It is a front-runner, never the only judge — public
// benchmarks show confident-wrong answers as high as ~0.87, so the cascade
// keeps the LLM for everything in the uncertain band:
//
//	p >= resolveThreshold AND evidence >= evidenceFloor -> resolve, no LLM
//	p <= keepOpenThreshold                              -> keep open, no LLM
//	anything else / Jev error                           -> llmAddressedJudge
//
// The asymmetry is deliberate. A false "addressed" silently resolves a live
// finding, so the resolve bar sits above the usual 0.95 and additionally
// requires the evidence question — Jev's known failure mode is inferring a
// fact the diff never states. A false "not addressed" only leaves a thread
// open, which is the pre-#166 status quo and costs nothing, so the keep-open
// bar can be aggressive. Jev failure escalates to the LLM rather than
// erroring: the fallback IS the retry.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/jev"
)

const (
	// jevAddressedResolveThreshold gates auto-resolve. Above the usual 0.95
	// act bar because the downside (silently closing a live finding) is real
	// and confidently-wrong answers exist up to ~0.87.
	jevAddressedResolveThreshold = 0.97
	// jevAddressedKeepOpenThreshold skips the LLM when Jev is confident the
	// diff did NOT fix the finding. Keep-open is the degrade-safe outcome, so
	// this bar is aggressive.
	jevAddressedKeepOpenThreshold = 0.05
	// jevAddressedEvidenceFloor pairs the verdict question with a presence
	// question: a high "addressed" probability inferred from file/symbol
	// names rather than shown in the diff escalates instead of resolving.
	jevAddressedEvidenceFloor = 0.5
)

// jevAddressedQuestions are asked in one batched Evaluate call — batching is
// ~12x cheaper and ~10x faster than separate calls and does not shift the
// answers. Criteria are concrete on both branches; a soft "or can't tell"
// branch is an escape hatch Jev takes.
// jevEvalTimeout bounds the Jev leg inside Judge. The caller wraps Judge in a
// 12s judgeCallTimeout shared with the LLM fallback — an unbounded Jev call
// could consume all of it and leave the fallback dead on arrival.
const jevEvalTimeout = 4 * time.Second

var jevAddressedQuestions = map[string]jev.Question{
	"addressed": jev.NoulQuestion(
		"State key `finding` holds a prior code-review finding on a file; `diff` holds what changed in that file since the review. Does the diff actually address the finding — is the underlying problem the finding describes fixed by this change?",
		"The diff contains a genuine fix for the specific problem the finding raises",
		"The diff merely touches, moves, or reformats nearby lines, or does not fix the problem",
	),
	"evidence": jev.NoulQuestion(
		"Does state key `diff` explicitly show the change that would fix the finding, rather than leaving the fix to be inferred from file, symbol, or function names?",
		"The fixing change is visible in the diff text",
		"The diff does not show a related change, or a fix can only be guessed at",
	),
}

// jevEvaluator is the slice of jev.Client the judge needs — a seam so tests
// drive the cascade with fixed probabilities instead of HTTP.
type jevEvaluator interface {
	Evaluate(ctx context.Context, state any, questions map[string]jev.Question, stage string) (jev.Result, error)
}

// jevAddressedJudge wraps the LLM AddressedJudge with a Jev pre-filter. The
// orchestrator builds it per push when an evaluator resolves — a stored
// 'typesafe' provider key (BYOK) or the env key + jev_classifier opt-in;
// otherwise threads go straight to the LLM judge.
type jevAddressedJudge struct {
	jev      jevEvaluator
	fallback AddressedJudge
	logger   *slog.Logger
}

// NewJevAddressedJudge returns an AddressedJudge that consults Jev first and
// escalates the uncertain band to fallback (the LLM judge).
func NewJevAddressedJudge(eval jevEvaluator, fallback AddressedJudge, logger *slog.Logger) AddressedJudge {
	if logger == nil {
		logger = slog.Default()
	}
	return &jevAddressedJudge{jev: eval, fallback: fallback, logger: logger}
}

// Judge asks Jev first. Confident verdicts return without an LLM call; the
// uncertain band and any Jev error escalate to the LLM judge with the Jev
// spend folded into the verdict's Tokens so /stats does not lose it.
func (j *jevAddressedJudge) Judge(ctx context.Context, finding JudgeFinding, interDiffPatch string) (verdict JudgeVerdict, err error) {
	opID, started := pipelineOperationStart(ctx, j.logger, "addressed_judge", "jev front-runner: decide fix vs proximity cheaply, escalate the uncertain band to the LLM judge", map[string]any{"finding": finding})
	defer func() {
		if err != nil {
			pipelineOperationFailure(ctx, j.logger, opID, "addressed_judge", started, err)
			return
		}
		pipelineOperationResult(ctx, j.logger, opID, "addressed_judge", fmt.Sprintf("addressed=%t", verdict.Addressed), started, verdict)
	}()

	if j.jev == nil {
		return j.escalate(ctx, finding, interDiffPatch, StageTokens{})
	}
	jevCtx, cancel := context.WithTimeout(ctx, jevEvalTimeout)
	res, err := j.jev.Evaluate(jevCtx, jevAddressedState(finding, interDiffPatch), jevAddressedQuestions, "addressed_judge")
	cancel()
	if err != nil {
		j.logger.WarnContext(ctx, "jev eval failed, escalating to llm judge", "error", err.Error())
		return j.escalate(ctx, finding, interDiffPatch, StageTokens{})
	}
	spend := jevStageTokens(res)

	p, evidence := res.Noul("addressed"), res.Noul("evidence")
	switch {
	case p != nil && evidence != nil && *p >= jevAddressedResolveThreshold && *evidence >= jevAddressedEvidenceFloor:
		verdict = JudgeVerdict{
			Addressed: true,
			Reason:    fmt.Sprintf("jev: addressed p=%.2f evidence=%.2f", *p, *evidence),
			Tokens:    spend,
		}
		return verdict, nil
	case p != nil && *p <= jevAddressedKeepOpenThreshold:
		verdict = JudgeVerdict{
			Addressed: false,
			Reason:    fmt.Sprintf("jev: not addressed p=%.2f", *p),
			Tokens:    spend,
		}
		return verdict, nil
	default:
		return j.escalate(ctx, finding, interDiffPatch, spend)
	}
}

// escalate runs the LLM judge and merges the Jev spend into its verdict so
// the auto_resolve token bucket bills both calls. A nil fallback degrades
// safe — keep the thread open rather than panic.
func (j *jevAddressedJudge) escalate(ctx context.Context, finding JudgeFinding, interDiffPatch string, jevSpend StageTokens) (JudgeVerdict, error) {
	if j.fallback == nil {
		return JudgeVerdict{Addressed: false, Reason: "no judge available", Tokens: jevSpend}, nil
	}
	verdict, err := j.fallback.Judge(ctx, finding, interDiffPatch)
	verdict.Tokens.PromptTokens += jevSpend.PromptTokens
	verdict.Tokens.CompletionTokens += jevSpend.CompletionTokens
	verdict.Tokens.TotalTokens += jevSpend.TotalTokens
	verdict.Tokens.Cost += jevSpend.Cost
	return verdict, err
}

// jevAddressedState builds the eval state with the same untrusted-data
// handling as buildAddressedJudgePrompt: the finding text gets injection
// redaction + delimiter wrap, the diff gets the wrap only (it is source code —
// redaction would corrupt legitimate lines), each tag-scrubbed so the content
// cannot close its own delimiter.
func jevAddressedState(f JudgeFinding, interDiff string) map[string]any {
	return map[string]any{
		"finding": wrapSafeDelimiters("finding", sanitizeUserInput(
			fmt.Sprintf("File: %s\nLine: %d\n\n%s", f.Path, f.Line, f.Body))),
		"diff": wrapSafeDelimiters("diff", truncateLines(interDiff, addressedJudgeMaxDiffLines)),
	}
}
