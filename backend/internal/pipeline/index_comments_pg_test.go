package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

// commentMemoryRaceBarrier stops attempt 1 after indexComments has prepared
// its batch but before the generation-guarded mutation begins. Both doors use
// the same barrier so this test fails deterministically if indexComments calls
// the indexer directly instead of entering RunIfReviewAttemptCurrent.
type commentMemoryRaceBarrier struct {
	prepared chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newCommentMemoryRaceBarrier() *commentMemoryRaceBarrier {
	return &commentMemoryRaceBarrier{
		prepared: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (b *commentMemoryRaceBarrier) wait() {
	b.once.Do(func() { close(b.prepared) })
	<-b.release
}

type barrierCommentAuthority struct {
	*store.Store
	barrier *commentMemoryRaceBarrier
}

func (a *barrierCommentAuthority) RunIfReviewAttemptCurrent(ctx context.Context, reviewID uuid.UUID, generation int, write func(context.Context) error) (bool, error) {
	if generation == 1 {
		a.barrier.wait()
	}
	return a.Store.RunIfReviewAttemptCurrent(ctx, reviewID, generation, write)
}

type barrierCommentIndexer struct {
	memory.Indexer
	barrier *commentMemoryRaceBarrier
}

func (i *barrierCommentIndexer) IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []memory.ReviewMemory) error {
	if len(comments) > 0 && comments[0].Body == "obsolete" {
		// The pre-fix scalar-check path reaches the mutation directly. Stop it at
		// the same point as the guarded path so the retry always wins the race.
		i.barrier.wait()
	}
	return i.Indexer.IndexReviewCommentsBatch(ctx, owner, repo, comments)
}

func TestIndexCommentsMemoryWriteLinearizesWithReviewRetry(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}

	var installationID int64
	if err := pool.QueryRow(ctx, `
		SELECT r.installation_id
		FROM reviews rv JOIN repos r ON r.id=rv.repo_id
		WHERE rv.id=$1`, reviewID).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pgIndexer := memory.NewPGIndexer(pool, nil, installationID, memory.StorageDimensions, logger).ForReview(reviewID)
	barrier := newCommentMemoryRaceBarrier()
	idx := &barrierCommentIndexer{Indexer: pgIndexer, barrier: barrier}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memories WHERE installation_id=$1`, installationID)
	})
	o := &Orchestrator{
		db:            pool,
		st:            st,
		logger:        logger,
		sinkAuthority: &barrierCommentAuthority{Store: st, barrier: barrier},
	}
	makeRun := func(generation int, body string) *PipelineRun {
		return &PipelineRun{
			ReviewID:          reviewID,
			AttemptGeneration: generation,
			PREvent:           ghpkg.PREvent{PRNumber: 1},
			Indexer:           idx,
			FileReviews: []FileReview{{Path: "race.go", Comments: []FileComment{{
				Line: 7, Body: body, Severity: SeverityWarning, Category: CategoryBug, Score: 9,
			}}}},
		}
	}

	oldResult := make(chan error, 1)
	go func() {
		oldResult <- o.indexComments(ctx, makeRun(1, "obsolete"), 0, "acme", "race", nil)
	}()
	select {
	case <-barrier.prepared:
	case err := <-oldResult:
		t.Fatalf("old attempt returned before memory barrier: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("old attempt did not reach the prepared memory boundary")
	}

	generation, won, err := st.BeginReviewRetry(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if !won || generation != 2 {
		t.Fatalf("retry = generation %d won %v, want generation 2 winner", generation, won)
	}
	close(barrier.release)

	if err := <-oldResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("old indexComments error = %v, want context.Canceled", err)
	}
	var obsolete int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memories WHERE installation_id=$1 AND content='obsolete'`, installationID).Scan(&obsolete); err != nil {
		t.Fatal(err)
	}
	if obsolete != 0 {
		t.Fatalf("obsolete memories = %d, want 0", obsolete)
	}

	if err := o.indexComments(ctx, makeRun(2, "current"), 0, "acme", "race", nil); err != nil {
		t.Fatalf("current indexComments: %v", err)
	}
	var current int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memories WHERE installation_id=$1 AND content='current' AND review_id=$2`, installationID, reviewID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 1 {
		t.Fatalf("current memories = %d, want 1", current)
	}
}
