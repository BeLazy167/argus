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

// BuildStartedComment renders the compact in-progress body.
func BuildStartedComment(dashboardBaseURL, reviewID, mode, scope, estimate, models string) string {
	var b strings.Builder
	b.WriteString(StartedCommentMarker)
	b.WriteString("\n")
	fmt.Fprintf(&b, "**Argus** is reviewing this PR · [watch live](%s/reviews/%s)\n", dashboardBaseURL, reviewID)

	parts := make([]string, 0, 3)
	if mode != "" {
		parts = append(parts, mode)
	}
	if scope != "" {
		parts = append(parts, scope)
	}
	if estimate != "" {
		parts = append(parts, estimate)
	}
	if len(parts) > 0 {
		b.WriteString("\n<sub>")
		b.WriteString(strings.Join(parts, " · "))
		b.WriteString("</sub>\n")
	}
	if models != "" {
		b.WriteString("\n")
		b.WriteString(models)
	}
	return strings.TrimRight(b.String(), "\n")
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
		b.WriteString("> **Argus** review failed. Argus did not post a review.\n")
	}

	if detail = strings.TrimSpace(detail); detail != "" {
		// Fenced, not inline: a provider error can contain backticks, newlines
		// and a URL, and inline code would break the block or render a link.
		fmt.Fprintf(&b, "\n```\n%s\n```\n", truncateDetail(detail))
	}

	if canRetry {
		b.WriteString("\nSelect the box to run the review again.\n\n")
		b.WriteString(TriggerCheckboxUnchecked)
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "\n<sub>[Details](%s/reviews/%s)</sub>", dashboardBaseURL, reviewID)
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

// stageModelOrder is the order in the compact model line.
var stageModelOrder = []string{"triage", "review", "scoring", "synthesis"}

func shortModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	parts := strings.Split(model, "/")
	name := parts[len(parts)-1]
	if index := strings.LastIndex(name, "-"); index >= 0 && index+1 < len(name) {
		return name[index+1:]
	}
	return name
}

// formatStageModels shows short model names and keeps the full IDs in a tooltip.
func formatStageModels(configs []store.ModelConfig) string {
	byStage := make(map[string]string, len(configs))
	for _, c := range configs {
		if c.Model == "" {
			continue
		}
		full := c.Model
		if c.Provider != "" && !strings.HasPrefix(full, c.Provider+"/") {
			full = c.Provider + "/" + full
		}
		byStage[c.Stage] = full
	}
	if len(byStage) == 0 {
		return ""
	}

	var visible []string
	var fullIDs []string
	lastModel := ""
	var stages []string
	flush := func() {
		if lastModel == "" || len(stages) == 0 {
			return
		}
		labels := make([]string, len(stages))
		for i, stage := range stages {
			if stage == "scoring" {
				labels[i] = "score"
			} else {
				labels[i] = stage
			}
		}
		label := strings.Join(labels, "/")
		visible = append(visible, fmt.Sprintf("%s (%s)", shortModelName(lastModel), label))
		fullIDs = append(fullIDs, label+": "+lastModel)
	}
	for _, stage := range stageModelOrder {
		model := byStage[stage]
		if model == "" {
			continue
		}
		if lastModel != "" && model != lastModel {
			flush()
			stages = nil
		}
		lastModel = model
		stages = append(stages, stage)
	}
	flush()
	if len(visible) == 0 {
		return ""
	}
	return fmt.Sprintf("<sub title=%q>models: %s</sub>", strings.Join(fullIDs, " · "), strings.Join(visible, " · "))
}

// formatTokenEstimate renders the expected token use from recent reviews.
func formatTokenEstimate(stats store.RepoReviewStats) string {
	if stats.SampleSize == 0 || stats.AvgTokens == 0 {
		return ""
	}
	if stats.AvgTokens >= 1000 {
		return fmt.Sprintf("est ~%.0fk tokens", float64(stats.AvgTokens)/1000)
	}
	return fmt.Sprintf("est ~%d tokens", stats.AvgTokens)
}
