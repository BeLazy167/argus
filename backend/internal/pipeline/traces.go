package pipeline

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// TraceSeed is the intermediate representation of a trace before persistence.
type TraceSeed struct {
	FilePath   string
	SymbolName string
	TraceType  string
	Content    string
	Severity   string
	ReviewID   *uuid.UUID
	PRNumber   int
	Metadata   map[string]any
}

// CollectReviewTraces extracts decision traces from a completed review run.
func CollectReviewTraces(run *PipelineRun) []TraceSeed {
	start := time.Now()
	slog.Info("trace collection started", "operation", "collect_review_traces", "review_id", run.ReviewID, "pr", run.PREvent.PRNumber, "file_review_count", len(run.FileReviews), "semantics", "convert every review finding into a decision-trace seed")
	var seeds []TraceSeed
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			desc := c.What
			if desc == "" {
				desc = c.Body
			}
			seeds = append(seeds, TraceSeed{
				FilePath:  fr.Path,
				TraceType: "review_finding",
				Content:   desc,
				Severity:  string(c.Severity),
				ReviewID:  &run.ReviewID,
				PRNumber:  run.PREvent.PRNumber,
			})
		}
	}
	slog.Info("trace collection completed", "operation", "collect_review_traces", "review_id", run.ReviewID, "input_file_count", len(run.FileReviews), "result_count", len(seeds), "duration_ms", time.Since(start).Milliseconds())
	return seeds
}

// CollectReplyTrace creates a trace from a developer's response to a review comment.
func CollectReplyTrace(repoID int64, filePath string, reviewID uuid.UUID, prNumber int, outcome string, replyContent string) TraceSeed {
	start := time.Now()
	slog.Info("reply trace collection started", "operation", "collect_reply_trace", "repo_id", repoID, "file", filePath, "review_id", reviewID, "pr", prNumber, "outcome", outcome, "reply_content", replyContent, "semantics", "map a developer reply outcome to a durable decision trace")
	traceType := "developer_agreed"
	if outcome == "dismissed" || outcome == "ignored" {
		traceType = "developer_dismissed"
	}
	seed := TraceSeed{
		FilePath:  filePath,
		TraceType: traceType,
		Content:   replyContent,
		ReviewID:  &reviewID,
		PRNumber:  prNumber,
	}
	slog.Info("reply trace collection completed", "operation", "collect_reply_trace", "repo_id", repoID, "review_id", reviewID, "trace_type", traceType, "result", seed, "duration_ms", time.Since(start).Milliseconds())
	return seed
}

// FormatTracesForPrompt formats recent decision traces as context for the review prompt.
func FormatTracesForPrompt(traces []store.DecisionTrace) string {
	start := time.Now()
	slog.Info("trace prompt formatting started", "operation", "format_traces_for_prompt", "trace_count", len(traces), "traces", traces, "semantics", "render recent decision traces as review-history prompt context")
	if len(traces) == 0 {
		slog.Info("trace prompt formatting skipped", "operation", "format_traces_for_prompt", "reason", "no_traces", "duration_ms", time.Since(start).Milliseconds())
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<history>\n")
	sb.WriteString("Recent review history for files in this PR:\n\n")
	for _, t := range traces {
		ago := time.Since(t.CreatedAt).Round(time.Hour * 24)
		days := int(ago.Hours() / 24)
		sb.WriteString(fmt.Sprintf("- %s [%s] %s (%s, %dd ago)\n", t.FilePath, t.TraceType, t.Content, t.Severity, days))
	}
	sb.WriteString("\nUse this history to inform your review — flag recurring issues, note if past concerns were addressed.\n")
	sb.WriteString("</history>\n")
	result := sb.String()
	slog.Info("trace prompt formatting completed", "operation", "format_traces_for_prompt", "trace_count", len(traces), "result_bytes", len(result), "result", result, "duration_ms", time.Since(start).Milliseconds())
	return result
}
