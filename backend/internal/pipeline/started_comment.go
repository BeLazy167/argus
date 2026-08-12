// Package pipeline: started_comment.go renders the progress comment Argus
// posts when a review begins, and rewrites it when the review reaches a
// terminal state.
//
// The comment used to have exactly one body — "Argus is reviewing this PR —
// watch live" — written once and never revisited except on the success path,
// which minimizes it. A failed or cancelled review therefore left a permanent
// invitation to watch a run that had already stopped, and the only way to
// learn the real outcome was the dashboard.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// StartedOutcome is the terminal state a progress comment is rewritten to.
type StartedOutcome string

const (
	// StartedOutcomeFailed is a pipeline error: the review did not complete.
	StartedOutcomeFailed StartedOutcome = "failed"
	// StartedOutcomeCancelled is a deliberate stop, from the dashboard or API.
	StartedOutcomeCancelled StartedOutcome = "cancelled"
)

// StartedCommentMarker identifies progress comments Argus authored, so a
// rewrite can never target an unrelated comment even if an id is stale.
const StartedCommentMarker = "<!-- argus-progress-v1 -->"

// BuildStartedComment renders the in-progress body.
//
// rows are the pre-rendered "| **Label** | value |" lines describing the run.
func BuildStartedComment(dashboardBaseURL, reviewID string, rows []string) string {
	var b strings.Builder
	b.WriteString(StartedCommentMarker)
	b.WriteString("\n")
	fmt.Fprintf(&b, "> **Argus** is reviewing this PR — [watch live](%s/reviews/%s)\n\n| | |\n|---|---|\n%s",
		dashboardBaseURL, reviewID, strings.Join(rows, "\n"))
	return b.String()
}

// BuildTerminalStartedComment rewrites the progress comment for a review that
// stopped without posting.
//
// The dashboard link is kept, because the run's partial trace is still there
// and is the only place to see how far it got. The "watch live" phrasing is
// not: nothing is live.
//
// detail is the failure cause or the cancel note; actor is the user who
// cancelled, empty when unknown or when the run failed on its own.
func BuildTerminalStartedComment(outcome StartedOutcome, dashboardBaseURL, reviewID, detail, actor string, canRetry bool) string {
	var b strings.Builder
	b.WriteString(StartedCommentMarker)
	b.WriteString("\n")

	switch outcome {
	case StartedOutcomeCancelled:
		if actor != "" {
			fmt.Fprintf(&b, "> **Argus** review cancelled by %s.\n", actor)
		} else {
			// Deliberately unattributed rather than guessing. Dashboard cancels
			// carry a Clerk subject, which is not a GitHub login, and
			// reviews.triggered_by names who STARTED the review — not who
			// stopped it. Rendering either as "@someone" would put a false
			// attribution on a public PR.
			b.WriteString("> **Argus** review was cancelled.\n")
		}
	default:
		b.WriteString("> **Argus** review failed — no review was posted.\n")
	}

	if detail = strings.TrimSpace(detail); detail != "" {
		// Fenced, not inline: a provider error can contain backticks, newlines
		// and a URL, and inline code would break the block or render a link.
		fmt.Fprintf(&b, "\n```\n%s\n```\n", truncateDetail(detail))
	}

	if canRetry {
		b.WriteString("\nRe-run by ticking the box below.\n\n")
		b.WriteString(TriggerCheckboxUnchecked)
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "\n<sub>[Details →](%s/reviews/%s)</sub>", dashboardBaseURL, reviewID)
	return b.String()
}

// maxDetailChars bounds the quoted cause. Provider errors can carry an entire
// response body, and a comment that large is unreadable and can exceed
// GitHub's 65536-character limit on its own.
const maxDetailChars = 500

func truncateDetail(s string) string {
	if len(s) <= maxDetailChars {
		return s
	}
	// Rune-safe: walk back off a continuation byte so the fence never contains
	// a broken UTF-8 sequence.
	cut := maxDetailChars
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// FinalizeStartedComment rewrites a review's progress comment to its terminal
// state. Best effort by design: the review's status is already persisted, and
// failing to edit a comment must never turn a recorded outcome into an error.
//
// Silently does nothing when the review never posted a progress comment —
// auto-run off (the trigger comment owns that flow and has its own failed
// state), or the create call failed at start.
func (o *Orchestrator) FinalizeStartedComment(ctx context.Context, reviewID uuid.UUID, outcome StartedOutcome, detail string) {
	o.logger.InfoContext(ctx, "progress comment finalization started", "event", "pipeline.github.progress_finalize_started", "review_id", reviewID, "status", string(outcome))
	ref, err := o.st.GetStartedCommentRef(ctx, reviewID)
	if err != nil {
		o.logger.Warn("finalize started comment: loading ref", "error", err, "review_id", reviewID)
		return
	}
	if ref == nil {
		o.logger.InfoContext(ctx, "progress comment finalization skipped", "event", "pipeline.github.progress_finalize_skipped", "review_id", reviewID, "reason", "comment_not_recorded")
		return
	}
	owner, repo, err := splitRepoFullName(ref.RepoFullName)
	if err != nil {
		o.logger.Warn("finalize started comment: bad repo name", "error", err, "repo", ref.RepoFullName)
		return
	}
	body := BuildTerminalStartedComment(outcome, o.cfg.DashboardBaseURL, reviewID.String(), detail, "", true)
	if err := o.ghClient.UpdateIssueComment(ctx, ref.InstallationID, owner, repo, ref.CommentID, body); err != nil {
		o.logger.WarnContext(ctx, "finalize started comment: updating comment", "event", "pipeline.github.progress_finalize_failed",
			"error", err, "review_id", reviewID, "comment_id", ref.CommentID, "status", string(outcome))
		return
	}
	o.logger.InfoContext(ctx, "progress comment finalized", "event", "pipeline.github.progress_finalized", "review_id", reviewID, "comment_id", ref.CommentID, "status", string(outcome))
}

// stageModelOrder is the render order for the models listed on the progress
// comment: the sequence a reader watches the pipeline execute in.
var stageModelOrder = []string{"triage", "review", "scoring", "synthesis"}

// formatStageModels renders the per-stage model assignment for the progress
// comment, e.g. "triage, scoring `openai / gpt-5-mini` · review `anthropic / opus`".
//
// Takes every resolved config rather than one model name because the stages
// bill separately and an installation can point each at a different provider.
// Stages sharing a model collapse into one entry, so the common
// "everything on one model" case stays short.
func formatStageModels(configs []store.ModelConfig) string {
	byStage := make(map[string]string, len(configs))
	providers := make(map[string]bool, len(configs))
	for _, c := range configs {
		if c.Model == "" {
			continue
		}
		byStage[c.Stage] = c.Model
		providers[c.Provider] = true
	}
	if len(byStage) == 0 {
		return ""
	}

	// One provider for everything is the common case, and repeating it on every
	// stage was what made this row wrap to three lines in a PR comment. Name it
	// once, or per-model only when they genuinely differ.
	shared := len(providers) == 1
	if !shared {
		for _, c := range configs {
			if c.Model != "" {
				byStage[c.Stage] = c.Provider + "/" + c.Model
			}
		}
	}

	var order []string
	members := make(map[string][]string)
	for _, st := range stageModelOrder {
		name, ok := byStage[st]
		if !ok {
			continue
		}
		if _, seen := members[name]; !seen {
			order = append(order, name)
		}
		members[name] = append(members[name], st)
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s `%s`", strings.Join(members[name], ", "), name))
	}
	line := strings.Join(parts, " · ")
	if shared {
		for p := range providers {
			if p != "" {
				line += " _(via " + p + ")_"
			}
		}
	}
	return line
}

// formatCostEstimate renders the expected spend for a run that is starting,
// from this repo's own recent completed reviews.
//
// It is explicitly an ESTIMATE and says so: the actual figure is unknowable
// until the run finishes, and the posted review carries the real per-stage
// breakdown. Showing nothing was the worse option — the progress comment named
// a model and no price at all, which reads as "free".
//
// Returns "" when there is no sample, rather than inventing a number from one
// unrelated review. Cost is omitted (tokens only) when the provider never
// reported it, which is normal for self-hosted and some OSS endpoints.
func formatCostEstimate(stats store.RepoReviewStats) string {
	if stats.SampleSize == 0 || stats.AvgTokens == 0 {
		return ""
	}
	if stats.CostAvailable && stats.AvgCost > 0 {
		return fmt.Sprintf("~%s tokens · ~$%.2f _(avg of last %d)_",
			humanizeTokens(stats.AvgTokens), stats.AvgCost, stats.SampleSize)
	}
	return fmt.Sprintf("~%s tokens _(avg of last %d)_",
		humanizeTokens(stats.AvgTokens), stats.SampleSize)
}
