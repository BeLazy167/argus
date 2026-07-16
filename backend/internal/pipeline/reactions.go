package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

// ReactionAnalyzer checks reactions on Argus review comments and indexes
// feedback signals. Thumbs-up = confirmed, thumbs-down = dismissed.
type ReactionAnalyzer struct {
	store       *store.Store
	ghClient    *ghpkg.Client
	memRegistry *memory.Registry
	logger      *slog.Logger
	lifecycle   *FindingLifecycle
}

func NewReactionAnalyzer(st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReactionAnalyzer {
	return &ReactionAnalyzer{
		store:       st,
		ghClient:    ghClient,
		memRegistry: memRegistry,
		logger:      logger,
		lifecycle:   NewFindingLifecycle(st, ghClient, logger),
	}
}

// ReactionSignal summarizes the thumbs-up/down counts on a comment.
type ReactionSignal struct {
	Confirmed int // +1 reactions
	Dismissed int // -1 reactions
}

// TallyReactions counts +1 and -1 reactions, ignoring all other types.
func TallyReactions(reactions []ghpkg.CommentReaction) ReactionSignal {
	var sig ReactionSignal
	for _, r := range reactions {
		switch r.Content {
		case "+1":
			sig.Confirmed++
		case "-1":
			sig.Dismissed++
		}
	}
	return sig
}

// DominantSignal returns "confirmed", "dismissed", or "" based on reaction counts.
// Returns "" when counts are equal or both zero.
func (s ReactionSignal) DominantSignal() string {
	if s.Confirmed == 0 && s.Dismissed == 0 {
		return ""
	}
	if s.Confirmed > s.Dismissed {
		return "confirmed"
	}
	if s.Dismissed > s.Confirmed {
		return "dismissed"
	}
	return "" // tied
}

// HandleCommentReactions fetches reactions for a PR review comment, determines
// the dominant signal, and indexes it as a feedback pattern.
func (ra *ReactionAnalyzer) HandleCommentReactions(ctx context.Context, event ghpkg.CommentEvent) error {
	// Only process comments on Argus-posted reviews
	comment, err := ra.store.GetCommentByGithubID(ctx, event.CommentID)
	if errors.Is(err, pgx.ErrNoRows) {
		ra.logger.Debug("reaction: comment not from argus", "comment_id", event.CommentID)
		return nil
	}
	if err != nil {
		ra.logger.Error("reaction: failed to look up comment", "error", err, "comment_id", event.CommentID)
		return nil
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reactions, err := ra.ghClient.ListCommentReactions(ctx, event.InstallationID, owner, repo, event.CommentID)
	if err != nil {
		ra.logger.Warn("reaction: failed to fetch reactions", "error", err, "comment_id", event.CommentID)
		return nil // non-fatal
	}

	// Filter out bot reactions
	var filtered []ghpkg.CommentReaction
	for _, r := range reactions {
		if !strings.HasSuffix(r.User, "[bot]") {
			filtered = append(filtered, r)
		}
	}

	signal := TallyReactions(filtered)
	action := signal.DominantSignal()
	if action == "" {
		return nil
	}

	ra.logger.Info("reaction signal",
		"action", action,
		"confirmed", signal.Confirmed,
		"dismissed", signal.Dismissed,
		"comment_id", event.CommentID,
		"file", comment.FilePath,
	)

	// Record outcome in DB
	inserted, err := ra.store.RecordCommentOutcome(ctx, comment.ID, action)
	if err != nil {
		ra.logger.Error("reaction: recording outcome", "error", err, "outcome", action)
	}

	// Feed the outcome back into the matched pattern's empirical quality — but
	// ONLY on first record. The sweep replays every PR event; RecordCommentOutcome
	// is idempotent, so bumping stats unconditionally would inflate the counts.
	if inserted {
		recordPatternOutcome(ctx, ra.store, ra.logger, comment.MatchedPatternID, action)
	}

	// Finding lifecycle: a 👎-dominant comment is dismissed in the LEDGER ONLY
	// (EventReactionDismissed → no thread resolution). A reaction is an untrusted,
	// low-effort signal swept from any user on every PR event; letting it drive
	// ResolveReviewThread would let a fork contributor clear a finding from the
	// merge-gate view via the app's write token. state=dismissed still feeds
	// suppression memory — the pre-#165 behavior. Non-fatal.
	if action == "dismissed" {
		if _, err := ra.lifecycle.Transition(ctx, FindingTransition{
			FindingID: comment.ID,
			Event:     EventReactionDismissed,
		}); err != nil {
			ra.logger.Warn("reaction: finding lifecycle transition", "error", err, "comment_id", comment.ID)
		}
	}

	// Index feedback signal for pattern reinforcement/suppression
	var indexer memory.Indexer
	if ra.memRegistry != nil {
		if inst, instErr := ra.store.GetInstallationByGitHubID(ctx, event.InstallationID); instErr == nil {
			indexer = ra.memRegistry.GetIndexer(ctx, inst.ID)
		}
	}
	if indexer != nil && comment.Category != nil {
		fb := memory.FeedbackMemory{
			FilePath:       comment.FilePath,
			Category:       *comment.Category,
			OriginalBody:   comment.Body,
			Action:         action,
			DeveloperReply: "", // no text reply, just a reaction
			PRNumber:       event.PRNumber,
		}
		if action == "dismissed" {
			fb.Repo = repo
			if kind, kerr := ra.store.GetCommentChangeClass(ctx, comment.ID); kerr != nil {
				ra.logger.Warn("reaction: comment change class lookup", "error", kerr, "comment_id", comment.ID)
			} else {
				fb.ChangeKind = kind
			}
		}
		if err := indexer.IndexFeedbackSignal(ctx, owner, repo, fb); err != nil {
			ra.logger.Error("reaction: indexing feedback signal", "error", err, "action", action)
		}
	}

	return nil
}

// SweepPRReactions enumerates every Argus-posted comment on a PR and runs the
// reaction-handling pipeline on each. Used to work around GitHub's lack of
// webhook events for reactions on PR review comments — on each pull_request
// event (synchronize/reopened/etc.) we best-effort re-check reactions.
//
// One bad comment shouldn't abort the sweep, but a systemic failure (auth,
// permissions, rate-limit) will produce the same error for every remaining
// comment, so we classify and short-circuit on fatal errors to avoid an
// N-retry storm. Returns the terminating error (fatal) or nil on clean sweep.
func (ra *ReactionAnalyzer) SweepPRReactions(ctx context.Context, installationID int64, repoFullName string, prNumber int) error {
	if ra == nil || ra.store == nil {
		return nil
	}
	ids, err := ra.store.ListPRGithubCommentIDs(ctx, repoFullName, prNumber)
	if err != nil {
		return fmt.Errorf("listing PR comment ids: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	ra.logger.Debug("reaction sweep", "pr", prNumber, "comment_count", len(ids))
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		evt := ghpkg.CommentEvent{
			InstallationID: installationID,
			RepoFullName:   repoFullName,
			PRNumber:       prNumber,
			CommentID:      id,
		}
		if err := ra.HandleCommentReactions(ctx, evt); err != nil {
			ra.logger.Warn("reaction sweep: comment failed", "error", err, "comment_id", id)
			if isSystemicSweepError(err) {
				// Same error will repeat for every remaining comment. Stop.
				return fmt.Errorf("reaction sweep aborted (systemic): %w", err)
			}
		}
	}
	return nil
}

// recordPatternOutcome feeds a confirmed/dismissed outcome on a matched comment
// back into the pattern's empirical quality (pattern_stats) and emits the
// memory.pattern_feedback telemetry event with the quality AFTER the update.
// No-op when the comment matched no pattern (patternID nil) or the signal is
// soft ("ignored"). Shared by the reaction and reply outcome paths. Every
// failure is non-fatal Warn — outcome learning must never break the webhook.
func recordPatternOutcome(ctx context.Context, st *store.Store, logger *slog.Logger, patternID *int64, action string) {
	if patternID == nil || (action != "confirmed" && action != "dismissed") {
		return
	}
	quality, ok, err := st.RecordPatternOutcome(ctx, *patternID, action == "confirmed")
	if err != nil {
		logger.Warn("pattern outcome", "error", err, "pattern_id", *patternID, "action", action)
		return
	}
	if !ok {
		return // no stats row yet (match predates stats wiring) — nothing to update
	}
	logger.InfoContext(ctx, "pattern feedback",
		slog.String("event", "memory.pattern_feedback"),
		slog.Int64("pattern_id", *patternID),
		slog.String("action", action),
		slog.Float64("quality", quality))
}

// isSystemicSweepError returns true when err is an auth/permission/rate-limit
// failure that will repeat for every remaining comment in a sweep. Used to
// short-circuit the sweep instead of retrying the same wall N times.
func isSystemicSweepError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "permission") ||
		strings.Contains(msg, "rate limit")
}
