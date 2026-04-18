// Package pipeline — acceptance.go runs the issue acceptance check worker.
// Invoked from validateStage as a parallel goroutine alongside SAST / blast /
// simulation / crosspr workers.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// loadFeatureFlags fetches per-installation toggles from the DB. On any error
// (missing row, DB down, malformed JSON) it falls back to DefaultFeatureFlags
// so the pipeline never hard-fails on flag loading. Not-present fields in the
// JSON are filled from defaults.
func loadFeatureFlags(ctx context.Context, st *store.Store, installationDBID int64) FeatureFlags {
	defaults := DefaultFeatureFlags()
	if st == nil || installationDBID == 0 {
		return defaults
	}
	raw, err := st.Q.GetInstallationFeatureFlags(ctx, installationDBID)
	if err != nil {
		slog.Debug("feature flag load failed, using defaults", "error", err, "install_id", installationDBID)
		return defaults
	}
	var flags FeatureFlags
	if len(raw) == 0 || string(raw) == "{}" {
		return defaults
	}
	if err := json.Unmarshal(raw, &flags); err != nil {
		slog.Warn("feature flag unmarshal failed, using defaults", "error", err, "install_id", installationDBID)
		return defaults
	}
	// Fill missing fields from defaults.
	if flags.MaxLinkedPRs <= 0 {
		flags.MaxLinkedPRs = defaults.MaxLinkedPRs
	}
	return flags
}

const acceptanceJudgeSystemPrompt = `You are a strict acceptance criteria judge for code review.

Given a pull request diff and a GitHub issue's acceptance criteria, classify each
criterion against the diff. Be strict — only mark "addressed" if the diff clearly
satisfies the criterion and you can cite specific file:line evidence.

Output a JSON array only, no prose. One object per criterion:
[
  {
    "criterion": "<the criterion text, verbatim>",
    "status": "addressed|partial|unaddressed|ambiguous",
    "reason": "<one sentence why>",
    "evidence": "<file:line if addressed/partial, empty otherwise>"
  }
]

Guidelines:
- addressed: diff provably satisfies the criterion with concrete evidence
- partial: diff addresses part of the criterion but leaves gaps (e.g. no tests)
- unaddressed: diff does not touch this criterion at all
- ambiguous: criterion is too vague, or the diff's intent is unclear
`

// maxIssuesPerRun caps the number of issues the acceptance worker will judge.
// More than this and the LLM cost becomes significant.
const maxIssuesPerRun = 5

// maxCriteriaPerIssue caps how many bullet criteria we pull from an issue body.
const maxCriteriaPerIssue = 15

// maxCriterionLen bounds individual criterion length before sending to LLM.
const maxCriterionLen = 500

// acceptanceHeaderRe matches the structured headers we prefer to extract from.
var acceptanceHeaderRe = regexp.MustCompile(
	`(?im)^#{1,6}\s*(Acceptance\s+Criteria|Definition\s+of\s+Done|Expected\s+Behaviou?r|Steps\s+to\s+Reproduce)\s*$`,
)

// bulletRe matches bullet points and checklist items within a section.
var bulletRe = regexp.MustCompile(`(?m)^[\s]*[-*]\s+(?:\[[ xX]\]\s+)?(.+)$`)

// runIssueAcceptanceWorker fetches each linked issue, extracts acceptance
// criteria from the body, and uses the LLM judge to classify whether the PR
// diff addresses each criterion. Results land on run.IssueAcceptance.
//
// Non-fatal: logs Warn and returns on any error. The caller wires this into
// validateStage inside a goroutine with the standard defer-recover panic guard.
func (o *Orchestrator) runIssueAcceptanceWorker(ctx context.Context, run *PipelineRun) {
	if !run.FeatureFlags.IssueAcceptance {
		o.logger.Info("[validate] issue acceptance skipped — disabled by feature flag", "pr", run.PREvent.PRNumber)
		return
	}
	if len(run.LinkedIssues) == 0 {
		return
	}

	owner, repo, splitErr := splitRepoFullName(run.PREvent.RepoFullName)
	if splitErr != nil {
		o.logger.Warn("[validate] acceptance: bad repo name", "error", splitErr, "pr", run.PREvent.PRNumber)
		return
	}

	// Hydrate issue bodies if GraphQL didn't populate them, cap total count.
	toJudge := make([]IssueLink, 0, len(run.LinkedIssues))
	for i := range run.LinkedIssues {
		if len(toJudge) >= maxIssuesPerRun {
			break
		}
		link := run.LinkedIssues[i]
		if link.Body == "" {
			issue, err := o.ghClient.GetIssue(ctx, run.PREvent.InstallationID, link.Owner, link.Repo, link.Number)
			if err != nil {
				o.logger.Warn("[validate] acceptance: fetch issue failed",
					"issue", fmt.Sprintf("%s/%s#%d", link.Owner, link.Repo, link.Number),
					"error", err)
				link.Accessible = false
				link.FetchError = err.Error()
				// keep the link but mark inaccessible; include in results for visibility
				continue
			}
			link.Accessible = true
			link.Title = issue.Title
			link.Body = issue.Body
		}
		link.Criteria = extractCriteria(link.Body)
		toJudge = append(toJudge, link)
	}

	if len(toJudge) == 0 {
		o.logger.Info("[validate] acceptance: no judgeable issues", "pr", run.PREvent.PRNumber)
		return
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "issueAcceptance")
	if !ok {
		o.logger.Warn("[validate] acceptance: no LLM provider resolved", "pr", run.PREvent.PRNumber)
		return
	}

	results := make([]AcceptanceResult, 0, len(toJudge))
	for _, link := range toJudge {
		result := judgeIssue(ctx, o, run, provider, cfg, link)
		if result != nil {
			results = append(results, *result)
		}
	}

	run.IssueAcceptance = results
	o.logger.Info("[validate] acceptance done",
		"pr", run.PREvent.PRNumber,
		"repo", owner+"/"+repo,
		"judged", len(results),
		"linked", len(run.LinkedIssues))
}

// judgeIssue runs one LLM call against a single issue's criteria and rolls up
// per-criterion verdicts into an AcceptanceResult.
func judgeIssue(
	ctx context.Context,
	o *Orchestrator,
	run *PipelineRun,
	provider llm.Provider,
	cfg llm.ModelConfig,
	link IssueLink,
) *AcceptanceResult {
	if len(link.Criteria) == 0 {
		// No criteria extracted — still return an ambiguous verdict so the
		// reviewer sees the link was considered.
		return &AcceptanceResult{
			IssueNumber: link.Number,
			IssueTitle:  link.Title,
			IssueURL:    link.URL,
			Verdict:     "ambiguous",
			Criteria: []AcceptanceCriterion{{
				Text:   "(no structured criteria in issue body)",
				Status: "ambiguous",
				Reason: "issue has no acceptance criteria section and no bulleted list",
			}},
		}
	}

	var prompt strings.Builder
	prompt.WriteString("Pull request diff:\n")
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	prompt.WriteString(fmt.Sprintf("\nIssue #%d — %s\n",
		link.Number, util.Truncate(link.Title, 200, true)))
	prompt.WriteString("Criteria:\n")
	for i, c := range link.Criteria {
		prompt.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
	}

	// No per-stage timeout — see intent.go for rationale (gpt-5.4 TTFT ~215s).
	// acceptanceMaxTokens leaves room for gpt-5.x reasoning-token burn.
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      acceptanceJudgeSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   acceptanceMaxTokens,
		Temperature: 0.1,
		JSONMode:    true,
	})
	if err != nil {
		o.logger.Warn("[validate] acceptance: LLM call failed",
			"issue", link.Number, "error", err)
		return nil
	}

	// Bucket acceptance tokens under run.Tokens.Acceptance. Lock-guarded via
	// addAcceptance since validateStage fan-outs can run concurrently.
	run.Tokens.addAcceptance(StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	})

	type llmCriterion struct {
		Criterion string `json:"criterion"`
		Status    string `json:"status"`
		Reason    string `json:"reason"`
		Evidence  string `json:"evidence"`
	}
	judged, parseErr := unmarshalLLMArray[llmCriterion](resp.Content)
	if parseErr != nil {
		o.logger.Warn("[validate] acceptance: LLM parse failed",
			"issue", link.Number, "error", parseErr,
			"response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	criteria := make([]AcceptanceCriterion, 0, len(judged))
	for _, j := range judged {
		status := normalizeStatus(j.Status)
		criteria = append(criteria, AcceptanceCriterion{
			Text:     j.Criterion,
			Status:   status,
			Reason:   j.Reason,
			Evidence: j.Evidence,
		})
	}

	verdict := rollupVerdict(criteria)
	if run.EventBus != nil {
		accepted, rejected := 0, 0
		for _, c := range criteria {
			switch c.Status {
			case "addressed":
				accepted++
			case "unaddressed":
				rejected++
			}
		}
		run.EventBus.Publish(run.ReviewID, EventAcceptanceChecked, map[string]any{
			"issue":    link.Number,
			"accepted": accepted,
			"rejected": rejected,
			"verdict":  verdict,
		})
	}

	return &AcceptanceResult{
		IssueNumber: link.Number,
		IssueTitle:  link.Title,
		IssueURL:    link.URL,
		Criteria:    criteria,
		Verdict:     verdict,
	}
}

// extractCriteria pulls bullet-point criteria from an issue body. It prefers
// structured sections (## Acceptance Criteria, ## Definition of Done, etc.)
// and falls back to the full body as a single free-form criterion if no
// section is found.
func extractCriteria(body string) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}

	// Find header positions in order.
	matches := acceptanceHeaderRe.FindAllStringIndex(body, -1)
	if len(matches) == 0 {
		// Fallback: whole body as one criterion.
		trimmed := strings.TrimSpace(body)
		return []string{util.Truncate(trimmed, maxCriterionLen, false)}
	}

	// Walk sections: for each header, grab text from end of header line to the
	// next header or end of body, then extract bullets.
	var out []string
	for i, m := range matches {
		sectionStart := m[1] // end of header match
		sectionEnd := len(body)
		if i+1 < len(matches) {
			sectionEnd = matches[i+1][0]
		}
		section := body[sectionStart:sectionEnd]

		for _, b := range bulletRe.FindAllStringSubmatch(section, -1) {
			text := strings.TrimSpace(b[1])
			text = normalizeWhitespace(text)
			text = util.Truncate(text, maxCriterionLen, false)
			if text == "" {
				continue
			}
			out = append(out, text)
			if len(out) >= maxCriteriaPerIssue {
				return out
			}
		}
	}

	// If a header was found but no bullets under it, fall back to the section
	// text as a single criterion.
	if len(out) == 0 {
		firstSection := body[matches[0][1]:]
		trimmed := normalizeWhitespace(strings.TrimSpace(firstSection))
		if trimmed != "" {
			out = []string{util.Truncate(trimmed, maxCriterionLen, false)}
		}
	}
	return out
}

// normalizeWhitespace collapses runs of whitespace (including newlines) into a
// single space and strips leading/trailing whitespace.
func normalizeWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// normalizeStatus coerces LLM output into one of the four known statuses.
// Anything unrecognized becomes "ambiguous".
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "addressed":
		return "addressed"
	case "partial", "partially_addressed", "partially addressed":
		return "partial"
	case "unaddressed", "not_addressed", "not addressed":
		return "unaddressed"
	default:
		return "ambiguous"
	}
}

// rollupVerdict reduces per-criterion statuses to a single verdict for an issue.
// Rules (in order):
//   - all addressed → "addressed"
//   - any unaddressed + nothing addressed → "unaddressed"
//   - mix of addressed + any other → "partial"
//   - all ambiguous → "ambiguous"
//   - fallback → "partial"
func rollupVerdict(criteria []AcceptanceCriterion) string {
	if len(criteria) == 0 {
		return "ambiguous"
	}
	var addr, part, unaddr, amb int
	for _, c := range criteria {
		switch c.Status {
		case "addressed":
			addr++
		case "partial":
			part++
		case "unaddressed":
			unaddr++
		default:
			amb++
		}
	}
	switch {
	case addr == len(criteria):
		return "addressed"
	case addr == 0 && part == 0 && unaddr > 0:
		return "unaddressed"
	case addr == 0 && part == 0 && unaddr == 0:
		return "ambiguous"
	case addr > 0 && (part > 0 || unaddr > 0 || amb > 0):
		return "partial"
	case part > 0:
		return "partial"
	default:
		return "partial"
	}
}

// formatIssueCoverageSection builds the Markdown block inserted into the
// synthesis summary when run.IssueAcceptance is non-empty.
func formatIssueCoverageSection(results []AcceptanceResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Issue Coverage\n\n")
	for _, r := range results {
		addressed := 0
		for _, c := range r.Criteria {
			if c.Status == "addressed" {
				addressed++
			}
		}
		icon := verdictIcon(r.Verdict)
		sb.WriteString(fmt.Sprintf("- **[#%d](%s)** — *%s* — %s %s (%d/%d)\n",
			r.IssueNumber, r.IssueURL,
			util.Truncate(r.IssueTitle, 80, true),
			icon, r.Verdict, addressed, len(r.Criteria)))
		for _, c := range r.Criteria {
			sb.WriteString(fmt.Sprintf("  - %s %s",
				verdictIcon(c.Status),
				util.Truncate(c.Text, 200, true)))
			if c.Status != "addressed" && c.Reason != "" {
				sb.WriteString(fmt.Sprintf(" — _%s_",
					util.Truncate(c.Reason, 200, true)))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func verdictIcon(status string) string {
	switch status {
	case "addressed":
		return "✅"
	case "partial":
		return "⚠️"
	case "unaddressed":
		return "❌"
	default:
		return "❓"
	}
}
