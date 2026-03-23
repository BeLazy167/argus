package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/internal/store"
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
	return seeds
}

// CollectReplyTrace creates a trace from a developer's response to a review comment.
func CollectReplyTrace(repoID int64, filePath string, reviewID uuid.UUID, prNumber int, outcome string, replyContent string) TraceSeed {
	traceType := "developer_agreed"
	if outcome == "dismissed" || outcome == "ignored" {
		traceType = "developer_dismissed"
	}
	return TraceSeed{
		FilePath:  filePath,
		TraceType: traceType,
		Content:   replyContent,
		ReviewID:  &reviewID,
		PRNumber:  prNumber,
	}
}

// FormatTracesForPrompt formats recent decision traces as context for the review prompt.
func FormatTracesForPrompt(traces []store.DecisionTrace) string {
	if len(traces) == 0 {
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
	return sb.String()
}
