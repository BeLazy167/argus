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
	lifecycle   *FindingLifecycle
}

func NewReplyAnalyzer(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReplyAnalyzer {
	return &ReplyAnalyzer{
		registry:    registry,
		store:       st,
		ghClient:    ghClient,
		memRegistry: memRegistry,
		logger:      logger,
		lifecycle:   NewFindingLifecycle(st, ghClient, logger),
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

	// Index learning in Supermemory. Derive a deterministic customID from the
	// normalized Learning text (SharedPatternCustomID idiom) so re-stating the
	// same insight upserts one doc instead of accreting a new _shared doc per
	// reply forever.
	if decision.Learning != "" && indexer != nil {
		_, err := indexer.IndexSharedPattern(ctx, memory.PatternMemory{
			Content:  decision.Learning,
			CustomID: memory.SharedPatternCustomID("reply_feedback", decision.Learning),
			Source:   "reply_feedback",
			FilePath: event.FilePath,
			Extra:    map[string]string{"repo": repo},
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
	case "not_applicable_change_kind":
		// Valid finding, wrong change kind — pattern quality is untouched
		// (recordPatternOutcome only reacts to confirmed/dismissed).
		outcome = "not_applicable_change_kind"
	}
	if outcome != "" {
		inserted, err := ra.store.RecordCommentOutcome(ctx, original.ID, outcome)
		if err != nil {
			ra.logger.Error("recording comment outcome", "error", err, "outcome", outcome)
		}
		// Feed hard outcomes (confirmed/dismissed; "ignored" is filtered inside)
		// back into pattern quality, but only on first record to avoid
		// double-counting replayed outcomes.
		if inserted {
			recordPatternOutcome(ctx, ra.store, ra.logger, original.MatchedPatternID, outcome)
		}
	}
	// Finding lifecycle: one Transition owns BOTH the ledger state and the thread
	// resolution. A rejection (Argus was wrong, or N/A for this change kind) →
	// dismissed; a plain resolve where the developer confirmed and fixed it →
	// addressed-by-reply (human evidence that may override a prior dismissal).
	//
	// AUTHORIZATION: resolving the thread + writing terminal ledger state is a
	// privileged shortcut — a review-comment replier is the same untrusted
	// population as a reactor, and EventAddressedByReply could otherwise let a
	// fork contributor's "good catch, fixed it" flip a maintainer's dismissal to
	// addressed and clear the finding from the require-resolution merge gate. So
	// only a trusted replier (owner/member/collaborator) drives the transition;
	// an untrusted reply keeps its non-terminal learning (comment_outcomes +
	// pattern/feedback signals, above and below) but does NOT resolve the thread
	// or write terminal state. The push→auto-resolve path (judge-verified, #166)
	// remains the resolution mechanism for everyone, including fork PRs.
	lcEvent, authorized := replyLifecycleEvent(decision.Action, outcome, event.AuthorAssociation)
	switch {
	case lcEvent == "":
		// stand_firm / clarify — no lifecycle transition.
	case !authorized:
		ra.logger.Info("reply: non-privileged replier — recording learning only, skipping thread resolution + terminal state",
			"would_be_event", lcEvent, "association", event.AuthorAssociation, "author", event.CommentAuthor, "comment_id", original.ID)
	default:
		if _, err := ra.lifecycle.Transition(ctx, FindingTransition{
			FindingID:      original.ID,
			Event:          lcEvent,
			InstallationID: event.InstallationID,
			Owner:          owner,
			Repo:           repo,
			PRNumber:       event.PRNumber,
			CommentNodeID:  event.NodeID,
		}); err != nil {
			ra.logger.Warn("reply: finding lifecycle transition", "error", err, "comment_id", original.ID)
		}
	}

	// Index feedback signal for pattern reinforcement/suppression. "clarify"
	// (outcome=ignored) is recorded as a WEAK negative signal: the developer
	// engaged but neither confirmed nor dismissed. FeedbackCustomID hashes the
	// action, so ignored coexists with confirmed/dismissed on the same finding.
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
		case "clarify":
			feedbackAction = "ignored"
		case "not_applicable_change_kind":
			// Change-kind-scoped dismissal: the change_kind stamp below lets
			// retrieval ignore it when reviewing production-grade PRs.
			feedbackAction = "dismissed"
		}

		if feedbackAction != "" {
			fb := memory.FeedbackMemory{
				FilePath:       original.FilePath,
				Category:       *original.Category,
				OriginalBody:   original.Body,
				Action:         feedbackAction,
				DeveloperReply: event.CommentBody,
				PRNumber:       event.PRNumber,
			}
			if feedbackAction == "dismissed" {
				fb.Repo = repo
				fb.Reason = decision.Learning
				if kind, kerr := ra.store.GetCommentChangeClass(ctx, original.ID); kerr != nil {
					ra.logger.Warn("comment change class lookup", "error", kerr, "comment_id", original.ID)
				} else {
					fb.ChangeKind = kind
				}
			}
			if err := indexer.IndexFeedbackSignal(ctx, owner, repo, fb); err != nil {
				ra.logger.Error("indexing feedback signal", "error", err, "action", feedbackAction)
			}
		}
	}

	return nil
}

// replyLifecycleEvent maps a reply decision to the FindingLifecycle event it
// should raise, and whether the replier is AUTHORIZED to raise it. A rejection
// (outcome dismissed / not-applicable) → EventDismissed; a plain confirm-and-fix
// resolve → EventAddressedByReply; stand_firm / clarify raise nothing (event="").
//
// Both events resolve the thread and write terminal ledger state, so they are
// gated on the replier's privilege: for a non-privileged replier authorized is
// false and the caller MUST skip the transition (keeping only non-terminal
// learning). Pure — unit-tested without the LLM/DB path.
func replyLifecycleEvent(action, outcome, authorAssociation string) (event LifecycleEvent, authorized bool) {
	switch {
	case outcome == "dismissed" || outcome == "not_applicable_change_kind":
		event = EventDismissed
	case action == "resolve": // confirmed finding, developer fixed it
		event = EventAddressedByReply
	default:
		return "", false
	}
	return event, ghpkg.IsPrivilegedAssociation(authorAssociation)
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
{"action": "resolve|clarify|stand_firm|not_applicable_change_kind", "reply": "your response", "learning": "optional pattern to remember"}`)
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
	case "resolve", "clarify", "stand_firm", "not_applicable_change_kind":
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
