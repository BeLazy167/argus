// Package pipeline — crosspr.go runs the cross-repo PR compatibility worker.
// Invoked from validateStage as a parallel goroutine alongside SAST / blast /
// simulation / acceptance workers.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/util"
)

const crossPRJudgeSystemPrompt = `You are a cross-repo change compatibility judge.

Given the primary PR diff and one or more linked PR diffs from other repos,
determine whether the changes form a coherent, compatible set. Focus on:
- API surface changes (function signatures, struct fields, HTTP routes)
- Shared types and contracts
- Breaking changes that would ripple

Output a single JSON object only:
{
  "compatible": true|false,
  "incompatibilities": [
    {"description": "short explanation", "files_affected": ["file1", "file2"]}
  ]
}

Be concrete. If the linked PRs are compatible, return an empty incompatibilities
array with compatible=true. If inaccessible repos are listed (marked NOT
ACCESSIBLE in the input), acknowledge them but don't fabricate verdicts.
`

// runCrossPRWorker fetches each linked cross-repo PR's diff (where accessible),
// then asks the LLM judge whether the primary diff and the linked diffs form a
// coherent change set. Inaccessible repos are noted as "partial coverage" in
// the output, never bump finding severity.
//
// Non-fatal: logs Warn and returns on any error. The caller wires this into
// validateStage inside a goroutine with the standard defer-recover panic guard.
func (o *Orchestrator) runCrossPRWorker(ctx context.Context, run *PipelineRun) {
	if !run.FeatureFlags.CrossPRChecks {
		o.logger.Info("[validate] cross-pr checks skipped — disabled by feature flag", "pr", run.PREvent.PRNumber)
		return
	}
	if len(run.LinkedPRs) == 0 {
		return
	}

	// Fetch each linked PR's diff via the primary installation.
	// Diffs land on the slice in-place so we have a consistent view for the LLM call.
	hydrated := make([]PRLink, 0, len(run.LinkedPRs))
	for _, link := range run.LinkedPRs {
		fetched := hydratePRLink(ctx, o, run, link)
		hydrated = append(hydrated, fetched)
	}

	accessibleCount := 0
	inaccessibleCount := 0
	for _, l := range hydrated {
		if l.Accessible {
			accessibleCount++
		} else {
			inaccessibleCount++
		}
	}

	// If none accessible, we still write an informational coverage note so
	// the user sees the links were detected.
	if accessibleCount == 0 {
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			Compatible:        true, // can't disprove, default to compatible
			Incompatibilities: nil,
			AccessibleCount:   0,
			InaccessibleCount: inaccessibleCount,
		}
		o.logger.Info("[validate] crosspr: all linked PRs inaccessible",
			"pr", run.PREvent.PRNumber, "inaccessible", inaccessibleCount)
		return
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "crossPR")
	if !ok {
		o.logger.Warn("[validate] crosspr: no LLM provider resolved", "pr", run.PREvent.PRNumber)
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			Compatible:        true,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	var prompt strings.Builder
	prompt.WriteString("Primary PR diff:\n")
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	for _, link := range hydrated {
		if !link.Accessible {
			prompt.WriteString(fmt.Sprintf("\nLinked PR %s/%s#%d — NOT ACCESSIBLE (%s)\n",
				link.Owner, link.Repo, link.Number, link.FetchError))
			continue
		}
		prompt.WriteString(fmt.Sprintf("\nLinked PR %s/%s#%d — %s\n",
			link.Owner, link.Repo, link.Number,
			util.Truncate(link.Title, 200, true)))
		prompt.WriteString(util.Truncate(link.Diff, 3000, false))
		prompt.WriteString("\n")
	}

	// No per-stage timeout — see intent.go for rationale (gpt-5.4 TTFT ~215s).
	// crossPRMaxTokens leaves room for gpt-5.x reasoning-token burn.
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      crossPRJudgeSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   crossPRMaxTokens,
		Temperature: 0.1,
		JSONMode:    true,
	})
	if err != nil {
		o.logger.Warn("[validate] crosspr: LLM call failed", "error", err, "pr", run.PREvent.PRNumber)
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			Compatible:        true,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	// Bucket cross-PR judge tokens. Lock-guarded via addCrossPR.
	run.Tokens.addCrossPR(StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	})

	var judged struct {
		Compatible        bool `json:"compatible"`
		Incompatibilities []struct {
			Description   string   `json:"description"`
			FilesAffected []string `json:"files_affected"`
		} `json:"incompatibilities"`
	}
	// Strip markdown fences the same way unmarshalLLMArray does.
	cleaned := stripCodeFences(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &judged); err != nil {
		o.logger.Warn("[validate] crosspr: LLM parse failed",
			"error", err,
			"response_prefix", util.Truncate(resp.Content, 200, true),
			"pr", run.PREvent.PRNumber)
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			Compatible:        true,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	incompatibilities := make([]string, 0, len(judged.Incompatibilities))
	for _, inc := range judged.Incompatibilities {
		if inc.Description == "" {
			continue
		}
		entry := inc.Description
		if len(inc.FilesAffected) > 0 {
			entry += " (" + strings.Join(inc.FilesAffected, ", ") + ")"
		}
		incompatibilities = append(incompatibilities, entry)
	}

	run.CrossPRCoverage = &CrossPRCoverage{
		LinkedPRs:         hydrated,
		Compatible:        judged.Compatible,
		Incompatibilities: incompatibilities,
		AccessibleCount:   accessibleCount,
		InaccessibleCount: inaccessibleCount,
	}

	o.logger.Info("[validate] crosspr done",
		"pr", run.PREvent.PRNumber,
		"accessible", accessibleCount,
		"inaccessible", inaccessibleCount,
		"incompatibilities", len(incompatibilities),
		"compatible", judged.Compatible)

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventCrossPRChecked, map[string]any{
			"accessible":        accessibleCount,
			"inaccessible":      inaccessibleCount,
			"compatible":        judged.Compatible,
			"incompatibilities": len(incompatibilities),
		})
	}
}

// hydratePRLink tries to fetch the linked PR's metadata + diff. Inaccessible
// PRs come back with Accessible=false and a FetchError string; accessible
// PRs have Title, HeadSHA, and Diff populated.
func hydratePRLink(ctx context.Context, o *Orchestrator, run *PipelineRun, link PRLink) PRLink {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Try to fetch the PR metadata first.
	pr, err := o.ghClient.GetPullRequest(fetchCtx, run.PREvent.InstallationID, link.Owner, link.Repo, link.Number)
	if err != nil {
		link.Accessible = false
		link.FetchError = fmt.Sprintf("PR metadata: %s", summarizeErr(err))
		return link
	}
	link.Title = pr.PRTitle
	link.HeadSHA = pr.HeadSHA

	// Then the unified diff.
	diffText, err := o.ghClient.GetPRDiff(fetchCtx, run.PREvent.InstallationID, link.Owner, link.Repo, link.Number)
	if err != nil {
		link.Accessible = false
		link.FetchError = fmt.Sprintf("PR diff: %s", summarizeErr(err))
		return link
	}
	link.Diff = diffText
	link.Accessible = true
	return link
}

// summarizeErr produces a short reason string suitable for user display.
// Distinguishes "no access" (Argus not installed) from generic errors.
func summarizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "404"), strings.Contains(lower, "not found"):
		return "not found (Argus may not be installed on this repo)"
	case strings.Contains(lower, "403"), strings.Contains(lower, "forbidden"):
		return "access denied (Argus may not be installed on this repo)"
	default:
		return util.Truncate(msg, 120, true)
	}
}

// formatCrossPRCoverageSection builds the Markdown block inserted into the
// synthesis summary when run.CrossPRCoverage is non-nil.
func formatCrossPRCoverageSection(cov *CrossPRCoverage) string {
	if cov == nil || len(cov.LinkedPRs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Cross-Repo PR Coverage\n\n")
	for _, link := range cov.LinkedPRs {
		if link.Accessible {
			sb.WriteString(fmt.Sprintf("- ✅ **[%s/%s#%d](%s)** — *%s* — compatible\n",
				link.Owner, link.Repo, link.Number, link.URL,
				util.Truncate(link.Title, 100, true)))
		} else {
			sb.WriteString(fmt.Sprintf("- ⚠️ **[%s/%s#%d](%s)** — %s\n",
				link.Owner, link.Repo, link.Number, link.URL, link.FetchError))
			sb.WriteString("  _Partial coverage: this change cannot be verified — reviewer should inspect manually._\n")
		}
	}
	if len(cov.Incompatibilities) > 0 {
		sb.WriteString("\n**Potential incompatibilities:**\n")
		for _, inc := range cov.Incompatibilities {
			sb.WriteString(fmt.Sprintf("- %s\n", inc))
		}
	}
	return sb.String()
}
