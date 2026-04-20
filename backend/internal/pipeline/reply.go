package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// ReplyAnalyzer handles incoming replies to Argus review comments.
type ReplyAnalyzer struct {
	registry    *llm.Registry
	store       *store.Store
	ghClient    *ghpkg.Client
	memRegistry *memory.Registry
	logger      *slog.Logger
}

func NewReplyAnalyzer(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReplyAnalyzer {
	return &ReplyAnalyzer{
		registry:    registry,
		store:       st,
		ghClient:    ghClient,
		memRegistry: memRegistry,
		logger:      logger,
	}
}

type replyDecision struct {
	Action   string `json:"action"`
	Reply    string `json:"reply"`
	Learning string `json:"learning,omitempty"`
}

// Analyze processes a comment reply event: looks up the original Argus comment,
// sends context to LLM, and executes the decided action.
func (ra *ReplyAnalyzer) Analyze(ctx context.Context, event ghpkg.CommentEvent) error {
	if event.InReplyToID == 0 {
		return nil
	}

	// Look up the original comment by GitHub ID
	original, err := ra.store.GetCommentByGithubID(ctx, event.InReplyToID)
	if err != nil {
		// Not an Argus comment — ignore silently
		ra.logger.Debug("reply not to argus comment", "in_reply_to", event.InReplyToID)
		return nil
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		return err
	}

	// Resolve DB IDs (webhook sends GitHub IDs, DB uses serial IDs)
	inst, err := ra.store.GetInstallationByGitHubID(ctx, event.InstallationID)
	if err != nil {
		return fmt.Errorf("resolving installation: %w", err)
	}

	var indexer *memory.Indexer
	if ra.memRegistry != nil {
		indexer = ra.memRegistry.GetIndexer(ctx, inst.ID)
	}

	dbRepo, err := ra.store.GetRepoByFullName(ctx, event.RepoFullName)
	if err != nil {
		return fmt.Errorf("resolving repo: %w", err)
	}

	// Build LLM prompt
	prompt := buildReplyPrompt(original, event)

	provider, cfg, err := ra.registry.ResolveProvider(ctx, storeConfigLister{st: ra.store, installationID: inst.ID}, inst.ID, dbRepo.ID, llm.StageReview)
	if err != nil {
		return fmt.Errorf("reply: %w", err)
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      replySystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   1024,
		Temperature: 0.3,
		Stage:       "reply",
	})
	if err != nil {
		return fmt.Errorf("reply LLM: %w", err)
	}

	var decision replyDecision
	if err := parseReplyDecision(resp.Content, &decision); err != nil {
		return fmt.Errorf("parsing reply decision: %w", err)
	}

	ra.logger.Info("reply decision",
		"action", decision.Action,
		"pr", event.PRNumber,
		"file", event.FilePath,
		"reply_author", event.CommentAuthor,
	)

	// Execute action
	if decision.Reply != "" {
		_, err := ra.ghClient.ReplyToComment(ctx, event.InstallationID, owner, repo, event.PRNumber, event.CommentID, decision.Reply)
		if err != nil {
			return fmt.Errorf("posting reply: %w", err)
		}
	}

	// Resolve the thread when the developer addressed the concern
	if decision.Action == "resolve" && event.NodeID != "" {
		threadID, err := ra.ghClient.FindThreadForComment(ctx, event.InstallationID, owner, repo, event.PRNumber, event.NodeID)
		if err != nil {
			ra.logger.Warn("resolve: find thread", "error", err)
		} else if err := ra.ghClient.ResolveReviewThread(ctx, event.InstallationID, threadID); err != nil {
			ra.logger.Warn("resolve: resolve thread", "error", err)
		}
	}

	// Index learning in Supermemory
	if decision.Learning != "" && indexer != nil {
		_, err := indexer.IndexOwnerPattern(ctx, owner, decision.Learning, "", map[string]string{
			"source": "reply_feedback",
			"repo":   repo,
			"file":   event.FilePath,
		})
		if err != nil {
			ra.logger.Error("indexing learning from reply", "error", err)
		}
	}

	// Determine outcome for the original comment
	var outcome string
	switch decision.Action {
	case "resolve":
		if decision.Learning != "" {
			outcome = "dismissed"
		} else {
			outcome = "confirmed"
		}
	case "stand_firm":
		outcome = "confirmed"
	case "clarify":
		outcome = "ignored"
	}
	if outcome != "" {
		if err := ra.store.RecordCommentOutcome(ctx, original.ID, outcome); err != nil {
			ra.logger.Error("recording comment outcome", "error", err, "outcome", outcome)
		}
	}

	// Index feedback signal for pattern reinforcement/suppression
	if indexer != nil && original.Category != nil {
		var feedbackAction string
		switch decision.Action {
		case "resolve":
			if decision.Learning != "" {
				feedbackAction = "dismissed"
			} else {
				feedbackAction = "confirmed"
			}
		case "stand_firm":
			feedbackAction = "confirmed"
		}

		if feedbackAction != "" {
			err := indexer.IndexFeedbackSignal(ctx, owner, repo, memory.FeedbackMemory{
				FilePath:       original.FilePath,
				Category:       *original.Category,
				OriginalBody:   original.Body,
				Action:         feedbackAction,
				DeveloperReply: event.CommentBody,
				PRNumber:       event.PRNumber,
			})
			if err != nil {
				ra.logger.Error("indexing feedback signal", "error", err, "action", feedbackAction)
			}
		}
	}

	return nil
}

func buildReplyPrompt(original *store.ReviewComment, event ghpkg.CommentEvent) string {
	var sb strings.Builder
	sb.WriteString("A developer replied to your review comment. Analyze their reply and decide how to respond.\n\n")
	sb.WriteString("## Original Argus Comment\n")
	sb.WriteString(fmt.Sprintf("File: %s\n", original.FilePath))
	if original.Severity != nil {
		sb.WriteString(fmt.Sprintf("Severity: %s\n", *original.Severity))
	}
	if original.Category != nil {
		sb.WriteString(fmt.Sprintf("Category: %s\n", *original.Category))
	}
	sb.WriteString(fmt.Sprintf("Comment: %s\n\n", original.Body))

	sb.WriteString("## Developer Reply\n")
	sb.WriteString(fmt.Sprintf("Author: %s\n", event.CommentAuthor))
	sb.WriteString(fmt.Sprintf("Reply: %s\n\n", event.CommentBody))

	if event.DiffHunk != "" {
		sb.WriteString("## Code Context (diff hunk)\n")
		sb.WriteString(event.DiffHunk)
		sb.WriteString("\n\n")
	}

	sb.WriteString(`Respond with JSON only:
{"action": "resolve|clarify|stand_firm", "reply": "your response", "learning": "optional pattern to remember"}`)
	return sb.String()
}

func parseReplyDecision(content string, decision *replyDecision) error {
	// Try direct parse
	if err := json.Unmarshal([]byte(content), decision); err == nil {
		return validateReplyDecision(decision)
	}
	// Try extracting JSON from markdown
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(content[start:end+1]), decision); err != nil {
			return fmt.Errorf("parsing reply JSON: %w", err)
		}
		return validateReplyDecision(decision)
	}
	return fmt.Errorf("no JSON object found in reply response")
}

func validateReplyDecision(d *replyDecision) error {
	switch d.Action {
	case "resolve", "clarify", "stand_firm":
	default:
		d.Action = "clarify"
	}
	return nil
}

const replySystemPrompt = `You are Argus, an AI code reviewer. A developer has replied to one of your review comments.

Analyze their reply and choose one action:

- "resolve": The developer's explanation is valid, they've addressed the concern, or you were wrong. Thank them briefly.
- "clarify": The developer seems confused or partially addressed the issue. Clarify your point with more detail.
- "stand_firm": The issue is real and the developer hasn't addressed it. Politely but firmly explain why the concern stands.

Guidelines:
- Be concise and professional
- If the developer is right and you were wrong, acknowledge it gracefully
- Your action choice affects pattern memory:
  - "resolve" WITHOUT learning = your finding was correct and developer fixed it (reinforces pattern)
  - "resolve" WITH learning = you were wrong and learned something (suppresses this pattern)
  - "stand_firm" = finding is valid, developer hasn't addressed it (reinforces pattern)
- Include "learning" if and only if the developer revealed something about how THIS SPECIFIC REPO works that you couldn't have known from the diff alone. Examples: "this project intentionally uses X pattern", "Y is handled upstream by Z service", "team convention is to do A instead of B"
- Do NOT include general programming knowledge as learning
- If no repo-specific insight was revealed, omit the field

Respond ONLY with JSON: {"action": "resolve|clarify|stand_firm", "reply": "your response", "learning": "optional"}
No other text.`
