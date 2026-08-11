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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type patternOutcomeRecorder interface {
	RecordPatternOutcome(context.Context, uuid.UUID, int64, bool) (float64, bool, error)
}

type reactionStore interface {
	patternOutcomeRecorder
	GetCommentByGithubID(context.Context, int64) (*store.ReviewComment, error)
	RecordCommentOutcome(context.Context, uuid.UUID, string) (bool, error)
	GetInstallationByGitHubID(context.Context, int64) (*store.Installation, error)
	GetCommentChangeClass(context.Context, uuid.UUID) (string, error)
	ListPRGithubCommentIDs(context.Context, string, int) ([]int64, error)
}

type reactionGitHubClient interface {
	ListCommentReactions(context.Context, int64, string, string, int64) ([]ghpkg.CommentReaction, error)
}

type reactionMemoryRegistry interface {
	GetIndexer(context.Context, int64) memory.Indexer
}

type reactionLifecycle interface {
	Transition(context.Context, FindingTransition) (TransitionResult, error)
}

// ReactionAnalyzer checks reactions on Argus review comments and indexes
// feedback signals. Thumbs-up = confirmed, thumbs-down = dismissed.
type ReactionAnalyzer struct {
	store           reactionStore
	ghClient        reactionGitHubClient
	repoPermissions repoWriteAccessChecker
	memRegistry     reactionMemoryRegistry
	logger          *slog.Logger
	lifecycle       reactionLifecycle
}

func NewReactionAnalyzer(st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReactionAnalyzer {
	return &ReactionAnalyzer{
		store:           st,
		ghClient:        ghClient,
		repoPermissions: ghClient,
		memRegistry:     memRegistry,
		logger:          logger,
		lifecycle:       NewFindingLifecycle(st, ghClient, logger),
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

const maxReactionPermissionLookupsPerSweep = 100

type reactionPermissionVerdict struct {
	allowed bool
	err     error
}

type reactionPermissionCache struct {
	verdicts map[string]reactionPermissionVerdict
	lookups  int
}

func newReactionPermissionCache() *reactionPermissionCache {
	return &reactionPermissionCache{verdicts: make(map[string]reactionPermissionVerdict)}
}

// HandleCommentReactions fetches reactions for a PR review comment, determines
// the dominant authorized signal, and indexes it as a feedback pattern.
func (ra *ReactionAnalyzer) HandleCommentReactions(ctx context.Context, event ghpkg.CommentEvent) error {
	return ra.handleCommentReactions(ctx, event, newReactionPermissionCache())
}

func (ra *ReactionAnalyzer) handleCommentReactions(ctx context.Context, event ghpkg.CommentEvent, permissions *reactionPermissionCache) error {
	// Only process comments on Argus-posted reviews
	comment, err := ra.store.GetCommentByGithubID(ctx, event.CommentID)
	if errors.Is(err, pgx.ErrNoRows) {
		ra.logger.Debug("reaction: comment not from argus", "comment_id", event.CommentID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("looking up review comment %d: %w", event.CommentID, err)
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reactions, err := ra.ghClient.ListCommentReactions(ctx, event.InstallationID, owner, repo, event.CommentID)
	if err != nil {
		if !errors.Is(err, ghpkg.ErrReviewCommentNotFound) {
			return fmt.Errorf("fetching reactions for comment %d: %w", event.CommentID, err)
		}
		// GitHub keeps no reaction aggregate after a review comment is deleted.
		// Reconcile the reversible reaction-owned memory to neutral, but retain
		// the historical DB comment ID and append-only outcome/lifecycle audit.
		ra.logger.Info("reaction: historical comment deleted; reconciling neutral",
			"comment_id", event.CommentID)
		reactions = nil
	}

	// A reaction can change durable outcome, lifecycle, and suppression memory,
	// so only the effective repository permission of its actual actor can make
	// it authoritative. Cache verdicts by case-insensitive login for the whole
	// sweep and cap unique lookups so a reaction swarm cannot amplify API calls.
	filtered, err := ra.authorizedSignalReactions(ctx, event, owner, repo, reactions, permissions)
	if err != nil {
		return fmt.Errorf("authorizing reactors for comment %d: %w", event.CommentID, err)
	}

	signal := TallyReactions(filtered)
	action := signal.DominantSignal()
	ra.logger.Info("reaction signal",
		"action", action,
		"confirmed", signal.Confirmed,
		"dismissed", signal.Dismissed,
		"comment_id", event.CommentID,
		"file", comment.FilePath,
	)

	if action != "" {
		inserted, recordErr := ra.store.RecordCommentOutcome(ctx, comment.ID, action)
		if recordErr != nil {
			ra.logger.Error("reaction: recording outcome", "error", recordErr, "outcome", action)
		}
		if inserted {
			recordPatternOutcome(ctx, ra.store, ra.logger, comment.ID, comment.MatchedPatternID, action)
		}
	}

	// A 👎-dominant comment is dismissed in the ledger only. Reaction removal
	// retracts suppression below, but cannot safely rewrite this lifecycle row:
	// the legacy ledger does not distinguish a reaction dismissal from a trusted
	// reply dismissal.
	if action == "dismissed" {
		if _, err := ra.lifecycle.Transition(ctx, FindingTransition{
			FindingID: comment.ID,
			Event:     EventReactionDismissed,
		}); err != nil {
			ra.logger.Warn("reaction: finding lifecycle transition", "error", err, "comment_id", comment.ID)
		}
	}

	// Reconcile current reaction feedback on every sweep, including the neutral
	// (tied/removed) state. This retracts a stale dismissal rather than leaving
	// an append-only suppression row behind.
	if ra.memRegistry == nil || comment.Category == nil {
		return nil
	}
	inst, err := ra.store.GetInstallationByGitHubID(ctx, event.InstallationID)
	if err != nil {
		return fmt.Errorf("resolving installation for reaction feedback: %w", err)
	}
	indexer := ra.memRegistry.GetIndexer(ctx, inst.ID)
	if indexer == nil {
		// The same registry supplies review retrieval. No indexer means memory is
		// unavailable, so stale reaction feedback cannot be read by this process.
		return nil
	}
	fb := memory.FeedbackMemory{
		FilePath: comment.FilePath,
		Category: *comment.Category,
		// Store the finding statement because dismissal retrieval queries it.
		OriginalBody: FindingTextFromPostedBody(comment.Body),
		Action:       action,
		PRNumber:     event.PRNumber,
		Source:       memory.SourceReactionFeedback,
	}
	if action == "dismissed" {
		fb.Repo = repo
		if kind, kindErr := ra.store.GetCommentChangeClass(ctx, comment.ID); kindErr != nil {
			ra.logger.Warn("reaction: comment change class lookup", "error", kindErr, "comment_id", comment.ID)
		} else {
			fb.ChangeKind = kind
		}
	}
	if err := indexer.ReconcileFeedbackSignal(ctx, owner, repo, fb); err != nil {
		return fmt.Errorf("reconciling feedback for comment %d: %w", event.CommentID, err)
	}

	return nil
}

func (ra *ReactionAnalyzer) authorizedSignalReactions(
	ctx context.Context,
	event ghpkg.CommentEvent,
	owner, repo string,
	reactions []ghpkg.CommentReaction,
	permissions *reactionPermissionCache,
) ([]ghpkg.CommentReaction, error) {
	if permissions == nil {
		return nil, fmt.Errorf("reaction permission cache is unavailable")
	}
	var authorized []ghpkg.CommentReaction
	for _, reaction := range reactions {
		if reaction.Content != "+1" && reaction.Content != "-1" {
			continue
		}
		login := strings.TrimSpace(reaction.User)
		key := strings.ToLower(login)
		if key == "" || strings.HasSuffix(key, "[bot]") {
			continue
		}
		verdict, ok := permissions.verdicts[key]
		if !ok {
			if ra.repoPermissions == nil {
				return nil, fmt.Errorf("repository permission checker is unavailable")
			}
			if permissions.lookups >= maxReactionPermissionLookupsPerSweep {
				return nil, fmt.Errorf("reaction permission lookup limit %d exceeded", maxReactionPermissionLookupsPerSweep)
			}
			permissions.lookups++
			allowed, err := ra.repoPermissions.HasRepoWriteAccess(
				ctx, event.InstallationID, owner, repo, login,
			)
			verdict = reactionPermissionVerdict{allowed: allowed, err: err}
			permissions.verdicts[key] = verdict
		}
		if verdict.err != nil {
			return nil, fmt.Errorf("checking repository permission for %q: %w", login, verdict.err)
		}
		if verdict.allowed {
			authorized = append(authorized, reaction)
		}
	}
	return authorized, nil
}

// SweepPRReactions enumerates every Argus-posted comment on a PR and runs the
// reaction-handling pipeline on each. Used to work around GitHub's lack of
// webhook events for reactions on PR review comments. Review launches wait for
// this sweep so reaction-owned feedback is current before dismissal retrieval.
//
// Comment-local failures are accumulated while the sweep continues to converge
// the remaining comments. Any failure is still returned: a caller must not run
// a review against potentially stale reaction trust. Systemic auth, permission,
// and rate-limit failures abort immediately to avoid an N-retry storm.
func (ra *ReactionAnalyzer) SweepPRReactions(ctx context.Context, installationID int64, repoFullName string, prNumber int) error {
	if ra == nil || ra.store == nil {
		return fmt.Errorf("reaction sweep is not configured")
	}
	ids, err := ra.store.ListPRGithubCommentIDs(ctx, repoFullName, prNumber)
	if err != nil {
		return fmt.Errorf("listing PR comment ids: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	ra.logger.Debug("reaction sweep", "pr", prNumber, "comment_count", len(ids))
	permissions := newReactionPermissionCache()
	var failures []error
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
		if err := ra.handleCommentReactions(ctx, evt, permissions); err != nil {
			ra.logger.Warn("reaction sweep: comment failed", "error", err, "comment_id", id)
			if isSystemicSweepError(err) {
				// Same error will repeat for every remaining comment. Stop.
				return fmt.Errorf("reaction sweep aborted (systemic): %w", err)
			}
			failures = append(failures, fmt.Errorf("comment %d: %w", id, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("reaction sweep incomplete: %w", err)
	}
	return nil
}

// recordPatternOutcome feeds a confirmed/dismissed outcome on a matched comment
// back into the pattern's empirical quality (pattern_stats) and emits the
// memory.pattern_feedback telemetry event with the quality AFTER the update.
// No-op when the comment matched no pattern (patternID nil) or the signal is
// soft ("ignored"). Shared by the reaction and reply outcome paths. Every
// failure is non-fatal Warn — outcome learning must never break the webhook.
func recordPatternOutcome(ctx context.Context, st patternOutcomeRecorder, logger *slog.Logger, commentID uuid.UUID, patternID *int64, action string) {
	if patternID == nil || (action != "confirmed" && action != "dismissed") {
		return
	}
	quality, ok, err := st.RecordPatternOutcome(ctx, commentID, *patternID, action == "confirmed")
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
