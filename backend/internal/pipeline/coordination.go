package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// This file holds the surviving lead-agent coordination functions relocated
// from orchestrator.go (issue #130): leadBriefStage — the StateMachine's
// StateBriefing entrypoint — and leadBrief on the live briefing path, plus
// analyzeBlastRadius, which validateStage calls on the live validation path.
// The broadcast/cross-check stages and their helpers were dormant (no callers,
// never registered) and were deleted after the relocation exposed them.

// Lead-agent LLM token budgets. Pinned together so the next model-tuning pass
// touches one place, not two.
//
// gpt-5.x reasoning tokens count against max_completion_tokens, so these are
// sized with headroom for invisible reasoning ON TOP of the visible JSON
// output: 4000 is the baseline (2000 output + 2000 reasoning); 8000 applies to
// the briefing stage whose JSON output naturally runs larger (leadBrief).
const (
	leadBriefMaxTokens   = 8000
	blastRadiusMaxTokens = 4000
)

// ─── Lead Agent Stage Wrappers ───────────────────────────────────────────────

// leadBriefStage runs the Lead Agent's briefing phase (Phase 1).
func (o *Orchestrator) leadBriefStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		o.logger.Info("[briefing] skipped — deep review not enabled", "pr", run.PREvent.PRNumber)
		return nil
	}
	start := time.Now()
	brief, err := o.leadBrief(ctx, run)
	dur := time.Since(start)
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBriefStage: %s", err)
		o.logger.Warn("[briefing] FAILED", "error", err, "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
		return nil
	}
	if brief == nil {
		o.logger.Warn("[briefing] returned nil — specialists run without brief", "lead_agent_error", run.LeadAgentError, "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
	} else {
		var fileCount int
		for _, item := range brief.Items {
			if item.File != "" {
				fileCount++
			}
		}
		o.logger.Info("[briefing] OK", "files_briefed", fileCount, "cross_cutting", len(brief.CrossCuttingConcerns()), "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
	}
	run.LeadBrief = brief
	return nil
}

// ─── Lead Agent Functions ────────────────────────────────────────────────────

const leadBriefSystemPrompt = `You coordinate 4 specialist reviewers: Bug Hunter, Security Auditor, Architecture Reviewer, Regression Reviewer.

Read the PR and produce a briefing as a JSON array. Each element is either a file brief or a cross-cutting concern.

File brief: {"file": "path", "summary": "what changed", "bug": "focus for bug hunter", "security": "focus for security", "arch": "focus for architecture", "regression": "focus for regression"}
Cross-cutting: {"cross_cutting": "concern spanning multiple files"}

Keep each focus field to 1 sentence. Output JSON array only.

Example:
[{"file": "src/auth.ts", "summary": "Session management with token refresh", "bug": "Token expiry edge cases, race in concurrent refresh", "security": "Session fixation, CSRF, token storage", "arch": "Error propagation from refresh to callers", "regression": "Return type change affects authenticated endpoints"}, {"cross_cutting": "auth.ts and api.ts share a global session cache"}]`

// leadBrief produces focus areas for each specialist by reading the whole PR.
// Non-fatal: returns nil on error so specialists run without briefs.
func (o *Orchestrator) leadBrief(ctx context.Context, run *PipelineRun) (*LeadBrief, error) {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return nil, nil
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "leadBrief")
	if !ok {
		run.LeadAgentError = "leadBrief: no LLM provider available"
		return nil, nil
	}

	var prompt strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	prompt.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n\nChanged files:\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	// No per-stage timeout. Azure gpt-5.4 TTFT benchmarks at ~215s
	// (artificialanalysis.ai/models/gpt-5-4), so the previous 20s wrapper
	// guaranteed false timeouts. The outer pipeline ctx remains the only
	// bound. leadBriefMaxTokens (8000) leaves room for gpt-5.x reasoning
	// tokens that count against the same budget — prior 1500 cap was fully
	// consumed by reasoning on acmeorg-account#335, leaving 5 tokens for the
	// JSON output (observed as `completion_tokens=5, response="[]"`).
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      leadBriefSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   leadBriefMaxTokens,
		Temperature: 0.2,
		Stage:       "lead_brief",
	})
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBrief LLM failed: %s", err)
		o.logger.Warn("leadBrief LLM failed", "error", err)
		return nil, nil
	}
	run.Tokens.addLeadAgent(StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	})

	items, err := unmarshalLLMArray[BriefItem](resp.Content)
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBrief parse failed: %s | response: %s", err, util.Truncate(resp.Content, 300, true))
		o.logger.Warn("leadBrief parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil, nil
	}
	if len(items) == 0 {
		run.LeadAgentError = "leadBrief: LLM returned empty array"
		o.logger.Warn("leadBrief returned empty array")
		return nil, nil
	}

	brief := &LeadBrief{Items: items}
	var fileCount int
	for _, item := range items {
		if item.File != "" {
			fileCount++
		}
	}
	o.logger.Info("lead brief produced", "files", fileCount, "cross_cutting", len(brief.CrossCuttingConcerns()))

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventLeadBrief, map[string]any{
			"files":         fileCount,
			"cross_cutting": len(brief.CrossCuttingConcerns()),
		})
	}
	return brief, nil
}

const blastRadiusAgentPrompt = `You analyze dependency impact. Given changed code and dependent file source, identify concrete breaking changes.

For each dependent:
1. What does it assume about the changed code? (return type, error behavior, side effects)
2. Do the changes violate those assumptions?
3. What's the concrete failure mode?

Only report with evidence from BOTH changed code AND dependent code.

Output JSON array:
[{"dependent_file": "...", "dependent_symbol": "...", "assumption_violated": "...", "failure_mode": "...", "severity": "critical|warning"}]
Return [] if nothing breaks.`

// analyzeBlastRadius checks if dependent code breaks due to PR changes.
// Non-fatal: returns nil on error.
func (o *Orchestrator) analyzeBlastRadius(ctx context.Context, run *PipelineRun, owner, repo string, depContents map[string]string) []BlastRadiusImpact {
	if run.Diff == nil || len(run.Diff.Files) == 0 || len(depContents) == 0 {
		return nil
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "analyzeBlastRadius")
	if !ok {
		return nil
	}

	var prompt strings.Builder
	prompt.WriteString("Changed files:\n")
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	prompt.WriteString("\n\nDependent files:\n")
	for fp, content := range depContents {
		prompt.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", fp, util.Truncate(content, 1500, false)))
	}

	// See leadBrief for rationale (Azure gpt-5.4 TTFT + reasoning-token budget).
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      blastRadiusAgentPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   blastRadiusMaxTokens,
		Temperature: 0.2,
		Stage:       "blast_radius",
	})
	if err != nil {
		o.logger.Warn("analyzeBlastRadius LLM failed", "error", err)
		return nil
	}
	run.Tokens.addLeadAgent(StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	})

	impacts, err := unmarshalLLMArray[BlastRadiusImpact](resp.Content)
	if err != nil {
		o.logger.Warn("analyzeBlastRadius parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	o.logger.Info("blast radius analysis", "impacts", len(impacts), "repo", fmt.Sprintf("%s/%s", owner, repo))
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventBlastRadius, map[string]any{
			"impacts": len(impacts),
		})
	}
	return impacts
}
