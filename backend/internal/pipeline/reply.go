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

type repoWriteAccessChecker interface {
	HasRepoWriteAccess(context.Context, int64, string, string, string) (bool, error)
}

// ReplyAnalyzer handles incoming replies to Argus review comments.
type ReplyAnalyzer struct {
	registry        *llm.Registry
	store           *store.Store
	ghClient        *ghpkg.Client
	repoPermissions repoWriteAccessChecker
	memRegistry     *memory.Registry
	logger          *slog.Logger
	lifecycle       *FindingLifecycle
}

func NewReplyAnalyzer(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReplyAnalyzer {
	return &ReplyAnalyzer{
		registry:        registry,
		store:           st,
		ghClient:        ghClient,
		repoPermissions: ghClient,
		memRegistry:     memRegistry,
		logger:          logger,
		lifecycle:       NewFindingLifecycle(st, ghClient, logger),
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

	// Authorize before resolving an LLM provider or posting a reply. A transient
	// permission failure must not spend tokens or create a response that a later
	// delivery could duplicate; denial still permits the intended observation-only
	// response, but no reply-derived persistent effect.
	allowWrites, err := authorizeReplyWrites(ctx, ra.repoPermissions, event, owner, repo)
	if err != nil {
		return fmt.Errorf("authorizing reply-derived writes: %w", err)
	}

	// Resolve DB IDs (webhook sends GitHub IDs, DB uses serial IDs)
	inst, err := ra.store.GetInstallationByGitHubID(ctx, event.InstallationID)
	if err != nil {
		return fmt.Errorf("resolving installation: %w", err)
	}

	var indexer memory.Indexer
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

	plan := planReplyEffects(decision, allowWrites)
	if !plan.AllowWrites {
		ra.logger.Info("reply: author lacks repository write permission; skipping all derived writes",
			"association", event.AuthorAssociation, "author", event.CommentAuthor, "comment_id", original.ID)
		return nil
	}

	// Authorized reply learnings use a distinct source from the legacy rows that
	// lack author provenance. Readers quarantine that legacy source rather than
	// guessing whether its author was trusted or deleting audit evidence.
	if decision.Learning != "" && indexer != nil {
		_, err := indexer.IndexSharedPattern(ctx, memory.PatternMemory{
			Content:  decision.Learning,
			CustomID: memory.SharedPatternCustomID(memory.SourceTrustedReplyFeedback, decision.Learning),
			Source:   memory.SourceTrustedReplyFeedback,
			FilePath: event.FilePath,
			Extra: map[string]string{
				"repo":               repo,
				"author_association": event.AuthorAssociation,
			},
		})
		if err != nil {
			ra.logger.Error("indexing learning from reply", "error", err)
		}
	}

	if plan.Outcome != "" {
		inserted, err := ra.store.RecordCommentOutcome(ctx, original.ID, plan.Outcome)
		if err != nil {
			ra.logger.Error("recording comment outcome", "error", err, "outcome", plan.Outcome)
		}
		if inserted {
			recordPatternOutcome(ctx, ra.store, ra.logger, original.ID, original.MatchedPatternID, plan.Outcome)
		}
	}

	if plan.LifecycleEvent != "" {
		if _, err := ra.lifecycle.Transition(ctx, FindingTransition{
			FindingID:      original.ID,
			Event:          plan.LifecycleEvent,
			InstallationID: event.InstallationID,
			Owner:          owner,
			Repo:           repo,
			PRNumber:       event.PRNumber,
			CommentNodeID:  event.NodeID,
		}); err != nil {
			ra.logger.Warn("reply: finding lifecycle transition", "error", err, "comment_id", original.ID)
		}
	}

	// Clarification is deliberately neutral: it records an ignored outcome for
	// telemetry, but creates no reinforcement or suppression memory.
	if indexer != nil && original.Category != nil && plan.FeedbackAction != "" {
		fb := memory.FeedbackMemory{
			FilePath: original.FilePath,
			Category: *original.Category,
			// Store the finding statement, not the rendered GitHub wrapper, so
			// dismissal retrieval compares the same semantic text.
			OriginalBody:   FindingTextFromPostedBody(original.Body),
			Action:         plan.FeedbackAction,
			DeveloperReply: event.CommentBody,
			PRNumber:       event.PRNumber,
			Source:         memory.SourceTrustedReplyFeedback,
		}
		if plan.FeedbackAction == "dismissed" {
			fb.Repo = repo
			fb.Reason = decision.Learning
			if kind, kerr := ra.store.GetCommentChangeClass(ctx, original.ID); kerr != nil {
				ra.logger.Warn("comment change class lookup", "error", kerr, "comment_id", original.ID)
			} else {
				fb.ChangeKind = kind
			}
		}
		if err := indexer.IndexFeedbackSignal(ctx, owner, repo, fb); err != nil {
			ra.logger.Error("indexing feedback signal", "error", err, "action", plan.FeedbackAction)
		}
	}

	return nil
}

// replyEffectPlan is the complete set of persistent effects derived from a
// reply. A zero plan means observation only: no repo/shared memory, outcome,
// pattern-quality, or lifecycle write is authorized.
type replyEffectPlan struct {
	AllowWrites    bool
	Outcome        string
	FeedbackAction string
	LifecycleEvent LifecycleEvent
}

func authorizeReplyWrites(ctx context.Context, checker repoWriteAccessChecker, event ghpkg.CommentEvent, owner, repo string) (bool, error) {
	if checker == nil {
		return false, fmt.Errorf("repository permission checker is unavailable")
	}
	allowed, err := checker.HasRepoWriteAccess(ctx, event.InstallationID, owner, repo, event.CommentAuthor)
	if err != nil {
		return false, fmt.Errorf("checking repository permission for %q: %w", event.CommentAuthor, err)
	}
	return allowed, nil
}

func planReplyEffects(decision replyDecision, allowWrites bool) replyEffectPlan {
	if !allowWrites {
		return replyEffectPlan{}
	}

	plan := replyEffectPlan{AllowWrites: true}
	switch decision.Action {
	case "resolve":
		if decision.Learning != "" {
			plan.Outcome = "dismissed"
			plan.FeedbackAction = "dismissed"
		} else {
			plan.Outcome = "confirmed"
			plan.FeedbackAction = "confirmed"
		}
	case "stand_firm":
		plan.Outcome = "confirmed"
		plan.FeedbackAction = "confirmed"
	case "clarify":
		plan.Outcome = "ignored"
	case "not_applicable_change_kind":
		plan.Outcome = "not_applicable_change_kind"
		plan.FeedbackAction = "dismissed"
	}
	plan.LifecycleEvent = replyLifecycleEvent(decision.Action, plan.Outcome)
	return plan
}

// replyLifecycleEvent maps an authorized reply decision to the lifecycle event
// it raises. Authorization is intentionally absent here: Analyze obtains the
// effective repository-permission verdict before LLM completion, and
// planReplyEffects applies it to the complete set of derived writes.
func replyLifecycleEvent(action, outcome string) LifecycleEvent {
	switch {
	case outcome == "dismissed" || outcome == "not_applicable_change_kind":
		return EventDismissed
	case action == "resolve": // confirmed finding, developer fixed it
		return EventAddressedByReply
	default:
		return ""
	}
}

func buildReplyPrompt(original *store.ReviewComment, event ghpkg.CommentEvent) string {
	var sb strings.Builder
	sb.WriteString("A developer replied to your review comment. Analyze their reply and decide how to respond.\n\n")
	sb.WriteString("## Original Argus Comment\n")
	sb.WriteString(replyPromptField("original_file", original.FilePath) + "\n")
	if original.Severity != nil {
		sb.WriteString(replyPromptField("original_severity", *original.Severity) + "\n")
	}
	if original.Category != nil {
		sb.WriteString(replyPromptField("original_category", *original.Category) + "\n")
	}
	sb.WriteString(replyPromptField("original_comment", original.Body) + "\n\n")

	sb.WriteString("## Developer Reply\n")
	sb.WriteString(replyPromptField("reply_author", event.CommentAuthor) + "\n")
	sb.WriteString(replyPromptField("developer_reply", event.CommentBody) + "\n\n")

	if event.DiffHunk != "" {
		sb.WriteString("## Code Context (diff hunk)\n")
		sb.WriteString(replyPromptField("diff_hunk", event.DiffHunk) + "\n\n")
	}

	sb.WriteString(`Respond with JSON only:
{"action": "resolve|clarify|stand_firm|not_applicable_change_kind", "reply": "your response", "learning": "optional pattern to remember"}`)
	return sb.String()
}

var replyPromptTags = []string{
	"original_file",
	"original_severity",
	"original_category",
	"original_comment",
	"reply_author",
	"developer_reply",
	"diff_hunk",
}

// replyPromptField applies the repository's full prompt-safety idiom to one
// field. Every known tag is scrubbed, not only the field's own tag, so content
// cannot synthesize a misleading sibling boundary either.
func replyPromptField(tag, value string) string {
	value = strings.ReplaceAll(sanitizeUserInput(value), "\x00", "")
	for _, promptTag := range replyPromptTags {
		value = scrubDelimiterToken(promptTag, value)
	}
	return wrapInDelimiters(tag, value)
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
	case "resolve", "clarify", "stand_firm", "not_applicable_change_kind":
	default:
		d.Action = "clarify"
	}
	return nil
}

const replySystemPrompt = `You are Argus, an AI code reviewer. A developer has replied to one of your review comments.

Analyze their reply and choose one action:

- "resolve": The developer's explanation is valid, they've addressed the concern, or you were wrong. Thank them briefly.
- "clarify": The developer seems confused or partially addressed the issue. Clarify your point with more detail. This is neutral feedback and creates no pattern memory.
- "stand_firm": The issue is real and the developer hasn't addressed it. Politely but firmly explain why the concern stands.
- "not_applicable_change_kind": The finding is technically VALID but does not apply to this kind of change — e.g. the developer explains this is a one-off script, prototype, or throwaway tooling where the flagged rigor is intentionally skipped. Acknowledge briefly and step back.

Guidelines:
- Be concise and professional
- If the developer is right and you were wrong, acknowledge it gracefully
- Your action choice affects pattern memory:
  - "resolve" WITHOUT learning = your finding was correct and developer fixed it (reinforces pattern)
  - "resolve" WITH learning = you were wrong and learned something (suppresses this pattern)
  - "stand_firm" = finding is valid, developer hasn't addressed it (reinforces pattern)
  - "not_applicable_change_kind" = valid finding, wrong change kind (records a change-kind-scoped dismissal — it will NOT silence the same finding on production code)
- Include "learning" if and only if the developer revealed something about how THIS SPECIFIC REPO works that you couldn't have known from the diff alone. Examples: "this project intentionally uses X pattern", "Y is handled upstream by Z service", "team convention is to do A instead of B"
- Change-kind learnings ARE welcome: "team treats missing retries as acceptable in one-off scripts", "prototypes under spike/ skip test coverage by convention"
- Do NOT include general programming knowledge or generic non-convention lessons as learning
- If no repo-specific insight was revealed, omit the field

Respond ONLY with JSON: {"action": "resolve|clarify|stand_firm|not_applicable_change_kind", "reply": "your response", "learning": "optional"}
No other text.`
