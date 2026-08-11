package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	gh "github.com/google/go-github/v68/github"
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
	calls     int
}

func (g *reactionGitHubStub) ListCommentReactions(context.Context, int64, string, string, int64) ([]ghpkg.CommentReaction, error) {
	g.calls++
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

func TestSweepPRReactionsDeletedCommentRetractsReactionFeedback(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{dismissalLive: true}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids: []int64{501},
			comment: &store.ReviewComment{
				ID: uuid.New(), FilePath: "handler.go", Body: "The guard is inverted", Category: &category,
			},
		},
		ghClient:    &reactionGitHubStub{err: fmt.Errorf("listing reactions: %w", ghpkg.ErrReviewCommentNotFound)},
		memRegistry: reactionRegistryStub{indexer: idx},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:   reactionLifecycleStub{},
	}

	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("deleted comment sweep blocked review: %v", err)
	}
	if idx.dismissalLive {
		t.Fatal("deleted comment left reaction-owned dismissal available for suppression")
	}
	if len(idx.actions) != 1 || idx.actions[0] != "" {
		t.Fatalf("reconciled actions = %v, want one neutral reconciliation", idx.actions)
	}

	// Historical github_comment_id values remain in the finding ledger. A later
	// sweep sees the retained ID again; repeated deletion reconciliation is a
	// harmless neutral upsert rather than a permanent launch failure.
	if err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17); err != nil {
		t.Fatalf("repeat deleted comment sweep blocked review: %v", err)
	}
	if gh := ra.ghClient.(*reactionGitHubStub); gh.calls != 2 {
		t.Fatalf("reaction fetch calls = %d, want retained historical ID swept twice", gh.calls)
	}
	if len(idx.actions) != 2 || idx.actions[1] != "" {
		t.Fatalf("repeat reconciled actions = %v, want neutral idempotence", idx.actions)
	}
}

func TestSweepPRReactionsTransientServerErrorStillBlocksReview(t *testing.T) {
	category := "bug_risk"
	idx := &reactionIndexerStub{dismissalLive: true}
	serverErr := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}
	ra := &ReactionAnalyzer{
		store: &reactionStoreStub{
			ids:     []int64{501},
			comment: &store.ReviewComment{ID: uuid.New(), Category: &category},
		},
		ghClient:    &reactionGitHubStub{err: serverErr},
		memRegistry: reactionRegistryStub{indexer: idx},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lifecycle:   reactionLifecycleStub{},
	}

	err := ra.SweepPRReactions(context.Background(), 91, "acme/api", 17)
	if !errors.Is(err, serverErr) {
		t.Fatalf("sweep error = %v, want wrapped transient GitHub failure", err)
	}
	if len(idx.actions) != 0 {
		t.Fatalf("transient error reconciled unobserved state: actions=%v", idx.actions)
	}
	if !idx.dismissalLive {
		t.Fatal("transient error silently retracted reaction state")
	}
}
