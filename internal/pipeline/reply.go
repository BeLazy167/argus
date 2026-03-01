package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
)

// ReplyAnalyzer handles incoming replies to Argus review comments.
type ReplyAnalyzer struct {
	registry *llm.Registry
	store    *store.Store
	ghClient *ghpkg.Client
	indexer  *memory.Indexer
	logger   *slog.Logger
}

func NewReplyAnalyzer(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, indexer *memory.Indexer, logger *slog.Logger) *ReplyAnalyzer {
	return &ReplyAnalyzer{
		registry: registry,
		store:    st,
		ghClient: ghClient,
		indexer:  indexer,
		logger:   logger,
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

	// Build LLM prompt
	prompt := buildReplyPrompt(original, event)

	var repoConfigs []llm.ModelConfig
	if dbConfigs, err := ra.store.ListModelConfigs(ctx, event.RepoID); err == nil {
		repoConfigs = storeToLLMConfigs(dbConfigs)
	}
	cfg, err := ra.registry.GetConfig(event.RepoID, llm.StageReview, repoConfigs)
	if err != nil {
		return fmt.Errorf("reply config: %w", err)
	}
	provider, err := ra.registry.GetProviderForRepo(ctx, event.InstallationID, &event.RepoID, cfg.Provider)
	if err != nil {
		return fmt.Errorf("reply provider: %w", err)
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      replySystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   1024,
		Temperature: 0.3,
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

	// Index learning in Supermemory
	if decision.Learning != "" && ra.indexer != nil {
		_, err := ra.indexer.IndexOwnerPattern(ctx, owner, decision.Learning, map[string]string{
			"source": "reply_feedback",
			"repo":   repo,
			"file":   event.FilePath,
		})
		if err != nil {
			ra.logger.Error("indexing learning from reply", "error", err)
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
- If you learn a project-specific pattern from the reply, include it in "learning"
- Learning should capture reusable patterns like "this project uses X pattern for Y" or "team prefers approach A over B"
- If no learning, omit the field or leave empty

Respond ONLY with JSON: {"action": "resolve|clarify|stand_firm", "reply": "your response", "learning": "optional"}
No other text.`
