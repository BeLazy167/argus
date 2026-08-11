package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

type reactionStoreStub struct {
	comment *store.ReviewComment
	ids     []int64
}

func (s *reactionStoreStub) GetCommentByGithubID(context.Context, int64) (*store.ReviewComment, error) {
	return s.comment, nil
}
func (*reactionStoreStub) RecordCommentOutcome(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}
func (*reactionStoreStub) GetInstallationByGitHubID(context.Context, int64) (*store.Installation, error) {
	return &store.Installation{ID: 7, InstallationID: 91}, nil
}
func (*reactionStoreStub) GetCommentChangeClass(context.Context, uuid.UUID) (string, error) {
	return "production", nil
}
func (s *reactionStoreStub) ListPRGithubCommentIDs(context.Context, string, int) ([]int64, error) {
	return s.ids, nil
}
func (*reactionStoreStub) RecordPatternOutcome(context.Context, uuid.UUID, int64, bool) (float64, bool, error) {
	return 0, false, nil
}

type reactionGitHubStub struct {
	reactions []ghpkg.CommentReaction
	err       error
}

func (g *reactionGitHubStub) ListCommentReactions(context.Context, int64, string, string, int64) ([]ghpkg.CommentReaction, error) {
	return g.reactions, g.err
}

type reactionRegistryStub struct{ indexer memory.Indexer }

func (r reactionRegistryStub) GetIndexer(context.Context, int64) memory.Indexer { return r.indexer }

type reactionLifecycleStub struct{}

func (reactionLifecycleStub) Transition(context.Context, FindingTransition) (TransitionResult, error) {
	return TransitionResult{}, nil
}

type reactionIndexerStub struct {
	memory.Indexer
	actions       []string
	dismissalLive bool
}

func (i *reactionIndexerStub) ReconcileFeedbackSignal(_ context.Context, _, _ string, feedback memory.FeedbackMemory) error {
	i.actions = append(i.actions, feedback.Action)
	i.dismissalLive = feedback.Action == "dismissed"
	return nil
}

func TestSweepPRReactionsRemovedThumbsDownRetractsDismissal(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{}
	gh := &reactionGitHubStub{reactions: []ghpkg.CommentReaction{{Content: "-1", User: "maintainer"}}}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids: []int64{501},
			comment: &store.ReviewComment{
				ID: uuid.New(), FilePath: "handler.go", Body: "The guard is inverted", Category: &category,
			},
		},
		ghClient:    gh,
		memRegistry: reactionRegistryStub{indexer: idx},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:   reactionLifecycleStub{},
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("initial thumbs-down sweep: %v", err)
	}
	if !idx.dismissalLive {
		t.Fatal("thumbs-down did not install reaction-owned dismissal")
	}

	gh.reactions = nil // GitHub's current aggregate after reaction removal.
	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("removal sweep: %v", err)
	}
	if idx.dismissalLive {
		t.Fatal("removed thumbs-down remains available for suppression")
	}
	if got := strings.Join(idx.actions, ","); got != "dismissed," {
		t.Fatalf("reconciled actions = %q, want dismissed then neutral", got)
	}
}

func TestSweepPRReactionsReturnsIncompleteReconciliationError(t *testing.T) {
	category := "bug_risk"
	githubErr := errors.New("upstream unavailable")
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids:     []int64{501},
			comment: &store.ReviewComment{ID: uuid.New(), Category: &category},
		},
		ghClient: &reactionGitHubStub{err: githubErr},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17)
	if !errors.Is(err, githubErr) {
		t.Fatalf("sweep error = %v, want wrapped GitHub failure", err)
	}
}
