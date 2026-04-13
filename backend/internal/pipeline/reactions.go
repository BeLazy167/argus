package pipeline

import (
	"context"
	"errors"
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
}

func NewReactionAnalyzer(st *store.Store, ghClient *ghpkg.Client, memRegistry *memory.Registry, logger *slog.Logger) *ReactionAnalyzer {
	return &ReactionAnalyzer{
		store:       st,
		ghClient:    ghClient,
		memRegistry: memRegistry,
		logger:      logger,
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
	if err := ra.store.RecordCommentOutcome(ctx, comment.ID, action); err != nil {
		ra.logger.Error("reaction: recording outcome", "error", err, "outcome", action)
	}

	// Index feedback signal for pattern reinforcement/suppression
	var indexer *memory.Indexer
	if ra.memRegistry != nil {
		if inst, instErr := ra.store.GetInstallationByGitHubID(ctx, event.InstallationID); instErr == nil {
			indexer = ra.memRegistry.GetIndexer(ctx, inst.ID)
		}
	}
	if indexer != nil && comment.Category != nil {
		err := indexer.IndexFeedbackSignal(ctx, owner, repo, memory.FeedbackMemory{
			FilePath:       comment.FilePath,
			Category:       *comment.Category,
			OriginalBody:   comment.Body,
			Action:         action,
			DeveloperReply: "", // no text reply, just a reaction
			PRNumber:       event.PRNumber,
		})
		if err != nil {
			ra.logger.Error("reaction: indexing feedback signal", "error", err, "action", action)
		}
	}

	return nil
}
