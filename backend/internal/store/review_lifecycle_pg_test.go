package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReviewRetryGenerationConvergesAcrossMachines(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	repoID, rawID := seedFileMemoryRepo(t, ctx, pool)
	_ = repoID
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status = 'failed' WHERE id = $1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)

	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, won, err := st.BeginReviewRetry(context.Background(), reviewID)
			if err != nil {
				t.Errorf("BeginReviewRetry: %v", err)
				return
			}
			if won {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("winners = %d, want 1", got)
	}
	for _, staleStatus := range []string{"failed", "completed"} {
		applied, err := st.UpdateReviewStatusForAttempt(ctx, reviewID, 1, staleStatus, "stale", nil, []string{"pending", "in_progress"})
		if err != nil {
			t.Fatal(err)
		}
		if applied {
			t.Fatalf("stale generation applied status %s", staleStatus)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM reviews WHERE id=$1`, reviewID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("status=%q want pending", status)
	}

	if _, err := pool.Exec(ctx, `
        INSERT INTO review_comments (review_id, attempt_generation, file_path, body) VALUES
        ($1, 1, 'old.go', 'obsolete'), ($1, 2, 'new.go', 'current')`, reviewID); err != nil {
		t.Fatal(err)
	}
	comments, err := st.GetReviewComments(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Body != "current" || comments[0].AttemptGeneration != 2 {
		t.Fatalf("comments = %+v, want current generation only", comments)
	}

	if err := st.CreateReviewComment(ctx, reviewID, 1, "stale.go", nil, nil, nil, "stale write", nil, nil, nil, nil, nil, nil, nil, nil, nil, true, nil, FindingStatePosted); !errors.Is(err, ErrReviewAttemptStale) {
		t.Fatalf("stale comment write error = %v, want ErrReviewAttemptStale", err)
	}
	if err := st.CreateReviewComment(ctx, reviewID, 2, "current.go", nil, nil, nil, "current write", nil, nil, nil, nil, nil, nil, nil, nil, nil, true, nil, FindingStatePosted); err != nil {
		t.Fatalf("current comment write: %v", err)
	}
	if err := st.ReplaceReviewMinorNotes(ctx, reviewID, 1, []ReviewMinorNote{{FilePath: "stale.go", Title: "stale"}}); !errors.Is(err, ErrReviewAttemptStale) {
		t.Fatalf("stale minor-note write error = %v, want ErrReviewAttemptStale", err)
	}

	var obsoleteID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE review_comments SET github_comment_id=111 WHERE review_id=$1 AND attempt_generation=1 RETURNING id`, reviewID).Scan(&obsoleteID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCommentByGithubID(ctx, 111); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("obsolete github comment lookup error = %v, want no rows", err)
	}
	if inserted, err := st.RecordCommentOutcome(ctx, obsoleteID, "confirmed"); err != nil || inserted {
		t.Fatalf("obsolete outcome inserted=%v err=%v", inserted, err)
	}
	if updated, err := st.UpdateFindingStateFrom(ctx, obsoleteID, FindingStateDismissed, []FindingState{FindingStatePosted}); err != nil || updated {
		t.Fatalf("obsolete state updated=%v err=%v", updated, err)
	}
	if hydrated, err := st.HydrateThreadNodeID(ctx, reviewID, 111, "thread-obsolete"); err != nil || hydrated != 0 {
		t.Fatalf("obsolete thread hydrated=%d err=%v", hydrated, err)
	}

	var installationID, patternID int64
	if err := pool.QueryRow(ctx, `SELECT installation_id FROM repos WHERE id=$1`, repoID).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO patterns (installation_id,repo_id,content,memory_custom_id) VALUES ($1,$2,'retry pattern','retry-pattern') RETURNING id`, installationID, repoID).Scan(&patternID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pattern_stats (installation_id,repo_id,memory_doc_id,content_hash) VALUES ($1,$2,'retry-pattern','hash')`, installationID, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE review_comments SET matched_pattern_id=$2 WHERE review_id=$1`, reviewID, patternID); err != nil {
		t.Fatal(err)
	}
	if _, updated, err := st.RecordPatternOutcome(ctx, obsoleteID, patternID, true); err != nil || updated {
		t.Fatalf("obsolete pattern outcome updated=%v err=%v", updated, err)
	}
	var currentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM review_comments WHERE review_id=$1 AND attempt_generation=2 ORDER BY created_at LIMIT 1`, reviewID).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	if _, updated, err := st.RecordPatternOutcome(ctx, currentID, patternID, true); err != nil || !updated {
		t.Fatalf("current pattern outcome updated=%v err=%v", updated, err)
	}
}

func TestReviewLifecycleStructuredState(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	repoID, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	st := NewWithDB(pool)

	notes := []ReviewMinorNote{{FilePath: "a.go", Line: 7, Severity: "suggestion", Title: "name this timeout"}}
	if err := st.ReplaceReviewMinorNotes(ctx, reviewID, 1, notes); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetReviewMinorNotes(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FilePath != "a.go" || got[0].Title != notes[0].Title {
		t.Fatalf("notes = %+v", got)
	}

	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, won, err := st.ClaimReviewSignal(context.Background(), repoID, 9, "auto_run_disabled", time.Hour)
			if err != nil {
				t.Errorf("ClaimReviewSignal: %v", err)
				return
			}
			if won {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := claims.Load(); got != 1 {
		t.Fatalf("signal winners = %d, want 1", got)
	}
	var oldClaim uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM review_signals WHERE repo_id=$1 AND pr_number=9`, repoID).Scan(&oldClaim); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE review_signals SET claimed_at=NOW()-INTERVAL '2 hours' WHERE id=$1`, oldClaim); err != nil {
		t.Fatal(err)
	}
	newClaim, won, err := st.ClaimReviewSignal(ctx, repoID, 9, "auto_run_disabled", time.Hour)
	if err != nil || !won {
		t.Fatalf("reclaim won=%v err=%v", won, err)
	}
	if completed, err := st.CompleteReviewSignal(ctx, oldClaim); err != nil || completed {
		t.Fatalf("late completion applied=%v err=%v", completed, err)
	}
	if completed, err := st.CompleteReviewSignal(ctx, newClaim); err != nil || !completed {
		t.Fatalf("owner completion applied=%v err=%v", completed, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed', github_review_id=123, error='post write failed' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	ghID, exists, applied, err := st.ConvergePostedReview(ctx, reviewID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || !applied || ghID != 123 {
		t.Fatalf("converge = (%d,%v), want (123,true)", ghID, applied)
	}
	var status string
	var errText *string
	if err := pool.QueryRow(ctx, `SELECT status,error FROM reviews WHERE id=$1`, reviewID).Scan(&status, &errText); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || errText != nil {
		t.Fatalf("status=%q error=%v", status, errText)
	}

	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='cancelled' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	if _, exists, applied, err := st.ConvergePostedReview(ctx, reviewID, 1); err != nil || !exists || applied {
		t.Fatalf("cancelled converge exists=%v applied=%v err=%v", exists, applied, err)
	}
}

func TestReviewAttemptOwnedWriteLinearizesWithRetry(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status = 'failed' WHERE id = $1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	writeEntered := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeResult := make(chan error, 1)
	go func() {
		current, err := st.RunIfReviewAttemptCurrent(testCtx, reviewID, 1, func(writeCtx context.Context) error {
			// This separate-pool FK write proves FOR NO KEY UPDATE is compatible
			// with the KEY SHARE lock taken for a review-owned child row.
			if _, insertErr := pool.Exec(writeCtx, `INSERT INTO review_comments (review_id, attempt_generation, file_path, body) VALUES ($1, 1, 'owned.go', 'owned write')`, reviewID); insertErr != nil {
				return insertErr
			}
			close(writeEntered)
			<-releaseWrite
			return nil
		})
		if err == nil && !current {
			err = errors.New("generation 1 unexpectedly lost ownership before its guarded write")
		}
		writeResult <- err
	}()
	select {
	case <-writeEntered:
	case err := <-writeResult:
		t.Fatalf("guarded write failed before entering: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("guarded write did not enter")
	}

	retryStarted := make(chan struct{})
	retryResult := make(chan error, 1)
	go func() {
		close(retryStarted)
		generation, won, err := st.BeginReviewRetry(testCtx, reviewID)
		if err == nil && (!won || generation != 2) {
			err = fmt.Errorf("retry = generation %d won %v, want generation 2 winner", generation, won)
		}
		retryResult <- err
	}()
	<-retryStarted
	close(releaseWrite)

	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if err := <-retryResult; err != nil {
		t.Fatal(err)
	}

	called := false
	current, err := st.RunIfReviewAttemptCurrent(ctx, reviewID, 1, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if current || called {
		t.Fatalf("stale guard current=%v called=%v, want no obsolete mutation", current, called)
	}
}

func waitForBlockedReviewLifecycleQuery(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, queryFragment string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND pid <> pg_backend_pid()
				  AND query LIKE '%' || $1 || '%'
				  AND cardinality(pg_blocking_pids(pid)) > 0
			)`, queryFragment).Scan(&blocked)
		if err != nil {
			t.Fatalf("checking blocked lifecycle query %q: %v", queryFragment, err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("lifecycle query %q never blocked behind review post: %v", queryFragment, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

func TestReviewPostLinearizesWithCancelAndRetry(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status = 'in_progress' WHERE id = $1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	postEntered := make(chan struct{})
	releasePost := make(chan struct{})
	var releasePostOnce sync.Once
	release := func() { releasePostOnce.Do(func() { close(releasePost) }) }
	t.Cleanup(release)
	postResult := make(chan error, 1)
	var postCalls atomic.Int32
	go func() {
		githubReviewID, outcome, err := st.PostReviewForAttempt(testCtx, reviewID, 1, func(context.Context) (int64, error) {
			postCalls.Add(1)
			close(postEntered)
			<-releasePost
			return 9876, nil
		})
		if err == nil && (githubReviewID != 9876 || outcome != ReviewPostRecorded) {
			err = fmt.Errorf("post = (%d,%q), want (9876,%q)", githubReviewID, outcome, ReviewPostRecorded)
		}
		postResult <- err
	}()
	select {
	case <-postEntered:
	case err := <-postResult:
		t.Fatalf("guarded post failed before entering GitHub: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("guarded post did not enter GitHub")
	}

	cancelResult := make(chan error, 1)
	go func() {
		applied, err := st.UpdateReviewStatusForAttempt(testCtx, reviewID, 1, "cancelled", "cancelled by user", nil, []string{"pending", "in_progress"})
		if err == nil && !applied {
			err = errors.New("cancel did not apply after waiting for post authority")
		}
		cancelResult <- err
	}()
	waitForBlockedReviewLifecycleQuery(t, testCtx, pool, `UPDATE reviews SET status=$3`)

	release()
	if err := <-postResult; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelResult; err != nil {
		t.Fatal(err)
	}

	// Retry is admissible only after cancel commits; it must see the id that was
	// durably recorded before cancel acquired the row and must not allow either
	// generation to issue another external mutation.
	generation, won, err := st.BeginReviewRetry(testCtx, reviewID)
	if err != nil || !won || generation != 2 {
		t.Fatalf("retry = generation %d won %v err %v, want generation 2 winner", generation, won, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status = 'in_progress' WHERE id = $1 AND attempt_generation = 2`, reviewID); err != nil {
		t.Fatal(err)
	}

	staleCalled := false
	if githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
		staleCalled = true
		return 111, nil
	}); err != nil || githubReviewID != 0 || outcome != ReviewPostRejected {
		t.Fatalf("stale post = (%d,%q,%v), want (0,%q,nil)", githubReviewID, outcome, err, ReviewPostRejected)
	}
	if staleCalled {
		t.Fatal("stale generation called GitHub")
	}

	currentCalled := false
	githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 2, func(context.Context) (int64, error) {
		currentCalled = true
		return 222, nil
	})
	if err != nil || githubReviewID != 9876 || outcome != ReviewPostAlreadyRecorded {
		t.Fatalf("current retry post = (%d,%q,%v), want existing (9876,%q,nil)", githubReviewID, outcome, err, ReviewPostAlreadyRecorded)
	}
	if currentCalled {
		t.Fatal("retry called GitHub even though the prior linearized post id was durable")
	}
	if got := postCalls.Load(); got != 1 {
		t.Fatalf("GitHub calls = %d, want exactly 1", got)
	}

	var storedID int64
	var storedGeneration int
	if err := pool.QueryRow(ctx, `SELECT github_review_id, attempt_generation FROM reviews WHERE id = $1`, reviewID).Scan(&storedID, &storedGeneration); err != nil {
		t.Fatal(err)
	}
	if storedID != 9876 || storedGeneration != 2 {
		t.Fatalf("review row = id %d generation %d, want id 9876 generation 2", storedID, storedGeneration)
	}
}

func TestReviewPostRejectsDisallowedStatusWithoutCallingGitHub(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	st := NewWithDB(pool)

	called := false
	githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
		called = true
		return 123, nil
	})
	if err != nil || githubReviewID != 0 || outcome != ReviewPostRejected {
		t.Fatalf("pending post = (%d,%q,%v), want rejected", githubReviewID, outcome, err)
	}
	if called {
		t.Fatal("pending review called GitHub")
	}

	if _, err := pool.Exec(ctx, `UPDATE reviews SET status = 'in_progress' WHERE id = $1`, reviewID); err != nil {
		t.Fatal(err)
	}
	ambiguous := errors.New("response lost after request")
	githubReviewID, outcome, err = st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
		return 0, ambiguous
	})
	if !errors.Is(err, ambiguous) || githubReviewID != 0 || outcome != ReviewPostAttempted {
		t.Fatalf("ambiguous post = (%d,%q,%v), want attempted with original error", githubReviewID, outcome, err)
	}
	calledAgain := false
	if _, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
		calledAgain = true
		return 999, nil
	}); !errors.Is(err, ErrReviewPostPersistenceAmbiguous) || outcome != ReviewPostRejected || calledAgain {
		t.Fatalf("repeat ambiguous post = (%q,%v), called=%v; want blocked", outcome, err, calledAgain)
	}
	var persisted bool
	if err := pool.QueryRow(ctx, `SELECT github_review_id IS NOT NULL FROM reviews WHERE id = $1`, reviewID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted {
		t.Fatal("post callback error invented durable GitHub evidence")
	}
}

type reviewPostFaultTx struct {
	pgx.Tx
	execErr       error
	commitErr     error
	afterRollback func()
	rollbackOnce  sync.Once
}

func (tx *reviewPostFaultTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if tx.execErr != nil && strings.Contains(sql, "UPDATE reviews SET github_review_id") {
		err := tx.execErr
		tx.execErr = nil
		return pgconn.CommandTag{}, err
	}
	return tx.Tx.Exec(ctx, sql, arguments...)
}

func (tx *reviewPostFaultTx) Commit(ctx context.Context) error {
	if tx.commitErr == nil {
		return tx.Tx.Commit(ctx)
	}
	injected := tx.commitErr
	tx.commitErr = nil
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	return injected
}

func (tx *reviewPostFaultTx) Rollback(ctx context.Context) error {
	err := tx.Tx.Rollback(ctx)
	tx.rollbackOnce.Do(func() {
		if tx.afterRollback != nil {
			tx.afterRollback()
		}
	})
	return err
}

type reviewPostDeadlineTx struct {
	pgx.Tx
	blockExec     bool
	blockCommit   bool
	blockRollback bool
}

func (tx *reviewPostDeadlineTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if tx.blockExec && strings.Contains(sql, "UPDATE reviews SET github_review_id") {
		<-ctx.Done()
		return pgconn.CommandTag{}, ctx.Err()
	}
	return tx.Tx.Exec(ctx, sql, arguments...)
}

func (tx *reviewPostDeadlineTx) Commit(ctx context.Context) error {
	if tx.blockCommit {
		<-ctx.Done()
		return ctx.Err()
	}
	return tx.Tx.Commit(ctx)
}

func (tx *reviewPostDeadlineTx) Rollback(ctx context.Context) error {
	if tx.blockRollback {
		<-ctx.Done()
		return ctx.Err()
	}
	return tx.Tx.Rollback(ctx)
}

func TestReviewPostDetachedOperationsAreDeadlineBoundedAndReleasePool(t *testing.T) {
	for _, tc := range []struct {
		name          string
		blockExec     bool
		blockCommit   bool
		blockRollback bool
		execErr       error
	}{
		{name: "id update", blockExec: true},
		{name: "commit", blockCommit: true},
		{name: "rollback", blockRollback: true, execErr: errors.New("force rollback")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, seedCtx := fileMemoryTestPool(t)
			_, rawID := seedFileMemoryRepo(t, seedCtx, pool)
			reviewID := uuid.MustParse(rawID)
			if _, err := pool.Exec(seedCtx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
				t.Fatal(err)
			}
			st := NewWithDB(pool)
			st.beginReviewPostTx = func(ctx context.Context, conn *pgxpool.Conn) (pgx.Tx, error) {
				tx, err := conn.Begin(ctx)
				if err != nil {
					return nil, err
				}
				var wrapped pgx.Tx = &reviewPostDeadlineTx{Tx: tx, blockExec: tc.blockExec, blockCommit: tc.blockCommit, blockRollback: tc.blockRollback}
				if tc.execErr != nil {
					wrapped = &reviewPostFaultTx{Tx: wrapped, execErr: tc.execErr}
				}
				return wrapped, nil
			}

			postCtx, cancelPost := context.WithCancel(context.Background())
			started := time.Now()
			githubReviewID, outcome, err := st.PostReviewForAttempt(postCtx, reviewID, 1, func(context.Context) (int64, error) {
				cancelPost() // every persistence/cleanup operation below is detached
				return 9753, nil
			})
			elapsed := time.Since(started)
			if err != nil || githubReviewID != 9753 || outcome != ReviewPostRecorded {
				t.Fatalf("deadline-bounded post = (%d,%q,%v), want repaired id", githubReviewID, outcome, err)
			}
			if elapsed < postedReviewOperationTimeout-250*time.Millisecond || elapsed > 2*postedReviewOperationTimeout+2*time.Second {
				t.Fatalf("blocked %s elapsed %v, want one bounded operation timeout", tc.name, elapsed)
			}

			// A timed-out rollback or unlock must not return its suspect session to
			// the pool. A fresh operation proves cancellation/retry capacity recovers.
			queryCtx, cancelQuery := context.WithTimeout(context.Background(), time.Second)
			defer cancelQuery()
			var storedID int64
			if err := pool.QueryRow(queryCtx, `SELECT github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&storedID); err != nil {
				t.Fatalf("pool remained blocked after timed-out %s: %v", tc.name, err)
			}
			if storedID != 9753 {
				t.Fatalf("stored id after timed-out %s = %d, want 9753", tc.name, storedID)
			}
			if generation, won, err := st.BeginReviewRetry(queryCtx, reviewID); err != nil || won || generation != 0 {
				t.Fatalf("retry after timed-out %s = (%d,%v,%v), want unblocked durable-id loser", tc.name, generation, won, err)
			}
		})
	}
}

func TestReviewPostUnconfirmedUnlockQuarantinesSession(t *testing.T) {
	pool, seedCtx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, seedCtx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(seedCtx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	st.unlockReviewPostSession = func(ctx context.Context, _ *pgxpool.Conn, _ string) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}

	postCtx, cancelPost := context.WithCancel(context.Background())
	started := time.Now()
	githubReviewID, outcome, err := st.PostReviewForAttempt(postCtx, reviewID, 1, func(context.Context) (int64, error) {
		cancelPost()
		return 5319, nil
	})
	elapsed := time.Since(started)
	if githubReviewID != 5319 || outcome != ReviewPostRecorded || err == nil || !strings.Contains(err.Error(), "releasing review post authority") {
		t.Fatalf("unconfirmed unlock = (%d,%q,%v), want recorded id plus cleanup evidence", githubReviewID, outcome, err)
	}
	if elapsed < postedReviewOperationTimeout-250*time.Millisecond || elapsed > postedReviewOperationTimeout+2*time.Second {
		t.Fatalf("blocked unlock elapsed %v, want bounded timeout", elapsed)
	}

	queryCtx, cancelQuery := context.WithTimeout(context.Background(), time.Second)
	defer cancelQuery()
	var storedID int64
	if err := pool.QueryRow(queryCtx, `SELECT github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&storedID); err != nil {
		t.Fatalf("quarantined session blocked pool: %v", err)
	}
	if storedID != 5319 {
		t.Fatalf("stored id = %d, want 5319", storedID)
	}
	if generation, won, err := st.BeginReviewRetry(queryCtx, reviewID); err != nil || won || generation != 0 {
		t.Fatalf("retry after unconfirmed unlock = (%d,%v,%v), want unblocked durable-id loser", generation, won, err)
	}
}

func TestReviewPostRepairsPositiveIDAfterPersistenceAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		execErr   error
		commitErr error
	}{
		{name: "id update failed", execErr: errors.New("injected id update failure")},
		{name: "commit response lost", commitErr: errors.New("injected ambiguous commit response")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, ctx := fileMemoryTestPool(t)
			_, rawID := seedFileMemoryRepo(t, ctx, pool)
			reviewID := uuid.MustParse(rawID)
			if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
				t.Fatal(err)
			}

			st := NewWithDB(pool)
			var begins atomic.Int32
			st.beginReviewPostTx = func(ctx context.Context, conn *pgxpool.Conn) (pgx.Tx, error) {
				tx, err := conn.Begin(ctx)
				if err != nil {
					return nil, err
				}
				if begins.Add(1) == 1 {
					return &reviewPostFaultTx{Tx: tx, execErr: tc.execErr, commitErr: tc.commitErr}, nil
				}
				return tx, nil
			}

			var postCalls atomic.Int32
			githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
				postCalls.Add(1)
				return 7654, nil
			})
			if err != nil || githubReviewID != 7654 || outcome != ReviewPostRecorded {
				t.Fatalf("post with repaired persistence = (%d,%q,%v), want (7654,%q,nil)", githubReviewID, outcome, err, ReviewPostRecorded)
			}

			calledAgain := false
			githubReviewID, outcome, err = st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
				calledAgain = true
				return 9999, nil
			})
			if err != nil || githubReviewID != 7654 || outcome != ReviewPostAlreadyRecorded {
				t.Fatalf("post after repair = (%d,%q,%v), want (7654,%q,nil)", githubReviewID, outcome, err, ReviewPostAlreadyRecorded)
			}
			if calledAgain || postCalls.Load() != 1 {
				t.Fatalf("GitHub callback calledAgain=%v calls=%d, want one non-idempotent call", calledAgain, postCalls.Load())
			}

			var storedID int64
			if err := pool.QueryRow(ctx, `SELECT github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&storedID); err != nil {
				t.Fatal(err)
			}
			if storedID != 7654 {
				t.Fatalf("stored GitHub review id = %d, want 7654", storedID)
			}
		})
	}
}

func TestCompletePostedReviewElectsOneFollowupWinner(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress',github_review_id=2468 WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)

	start := make(chan struct{})
	outcomes := make(chan ReviewCompletionOutcome, 2)
	var linkedRefs, events, backfills, hydrations, sinks atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcome, err := st.CompletePostedReview(ctx, reviewID, 1, 2468)
			if err != nil {
				t.Errorf("CompletePostedReview: %v", err)
				return
			}
			outcomes <- outcome
			if outcome != ReviewCompletionWon {
				return
			}
			// These counters model the orchestrator's linked-ref, lifecycle event,
			// comment backfill, thread hydration, and post-review sink block. The
			// store CAS is the sole election seam guarding that whole block.
			linkedRefs.Add(1)
			events.Add(1)
			backfills.Add(1)
			hydrations.Add(1)
			sinks.Add(1)
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	counts := map[ReviewCompletionOutcome]int{}
	for outcome := range outcomes {
		counts[outcome]++
	}
	if counts[ReviewCompletionWon] != 1 || counts[ReviewCompletionAlreadyCompleted] != 1 {
		t.Fatalf("completion outcomes = %#v, want one winner and one already-completed loser", counts)
	}
	if linkedRefs.Load() != 1 || events.Load() != 1 || backfills.Load() != 1 || hydrations.Load() != 1 || sinks.Load() != 1 {
		t.Fatalf("winner followups refs=%d events=%d backfills=%d hydrations=%d sinks=%d, want each exactly once",
			linkedRefs.Load(), events.Load(), backfills.Load(), hydrations.Load(), sinks.Load())
	}
}

func TestRepairPostedReviewIDRejectsGenerationAndIDConflicts(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	st := NewWithDB(pool)

	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress',github_review_id=111 WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	if err := st.RepairPostedReviewID(ctx, reviewID, 1, 222); !errors.Is(err, ErrReviewPostRepairConflict) {
		t.Fatalf("conflicting id repair error = %v, want ErrReviewPostRepairConflict", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE reviews SET github_review_id=NULL,attempt_generation=2 WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	if err := st.RepairPostedReviewID(ctx, reviewID, 1, 222); !errors.Is(err, ErrReviewPostRepairConflict) {
		t.Fatalf("stale generation repair error = %v, want ErrReviewPostRepairConflict", err)
	}

	var generation int
	var storedID *int64
	if err := pool.QueryRow(ctx, `SELECT attempt_generation,github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&generation, &storedID); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || storedID != nil {
		t.Fatalf("conflict repair mutated row: generation=%d github_review_id=%v", generation, storedID)
	}
}

func TestReviewPostRepairBlocksCancelRetryUntilPositiveIDIsDurable(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)

	rollbackReached := make(chan struct{})
	allowRepair := make(chan struct{})
	st.beginReviewPostTx = func(ctx context.Context, conn *pgxpool.Conn) (pgx.Tx, error) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return &reviewPostFaultTx{
			Tx:      tx,
			execErr: errors.New("injected persistence outage"),
			afterRollback: func() {
				close(rollbackReached)
				<-allowRepair
			},
		}, nil
	}

	var calls atomic.Int32
	cancelDone := make(chan error, 1)
	postDone := make(chan error, 1)
	go func() {
		githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
			calls.Add(1)
			// This is the production cancellation status writer. It blocks on the
			// post transaction's row lock until the injected persistence failure
			// rolls that transaction back.
			go func() {
				updated, updateErr := st.UpdateReviewStatusForAttempt(ctx, reviewID, 1, "cancelled", "cancelled by user", nil, []string{"pending", "in_progress"})
				if updateErr == nil && !updated {
					updateErr = errors.New("production cancel did not update the guarded attempt")
				}
				cancelDone <- updateErr
			}()
			return 8642, nil
		})
		if err != nil || githubReviewID != 8642 || outcome != ReviewPostRecorded {
			postDone <- fmt.Errorf("post = (%d,%q,%v), want repaired positive id", githubReviewID, outcome, err)
			return
		}
		postDone <- nil
	}()

	select {
	case <-rollbackReached:
	case <-time.After(5 * time.Second):
		t.Fatal("post did not reach failed-transaction rollback")
	}
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("production cancel did not finish after rollback")
	}

	type retryResult struct {
		generation int
		won        bool
		err        error
	}
	retryDone := make(chan retryResult, 1)
	go func() {
		generation, won, err := st.BeginReviewRetry(ctx, reviewID)
		retryDone <- retryResult{generation: generation, won: won, err: err}
	}()

	// BeginReviewRetry must be waiting on the post session's matching advisory
	// lock. The row is already cancelled and unlocked, so without that authority
	// it would bump generation here before repair can attach id 8642.
	select {
	case result := <-retryDone:
		close(allowRepair)
		<-postDone
		t.Fatalf("retry returned before positive-id repair: %+v", result)
	case <-time.After(150 * time.Millisecond):
	}
	var generation int
	var status string
	var stateError *string
	if err := pool.QueryRow(ctx, `SELECT attempt_generation,status,error FROM reviews WHERE id=$1`, reviewID).Scan(&generation, &status, &stateError); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || status != "cancelled" {
		t.Fatalf("while repair blocked: generation=%d status=%q, want generation 1 cancelled", generation, status)
	}
	if stateError == nil || !strings.HasPrefix(*stateError, ErrReviewPostPersistenceAmbiguous.Error()) {
		t.Fatalf("production cancel erased durable posting claim: error=%v", stateError)
	}

	close(allowRepair)
	if err := <-postDone; err != nil {
		t.Fatal(err)
	}
	result := <-retryDone
	if result.err != nil || !result.won || result.generation != 2 {
		t.Fatalf("retry after repair = %+v, want generation 2 only after id is durable", result)
	}

	var storedID int64
	if err := pool.QueryRow(ctx, `SELECT attempt_generation,github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&generation, &storedID); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || storedID != 8642 {
		t.Fatalf("durable state generation=%d id=%d, want generation 2 retaining id 8642", generation, storedID)
	}
	if applied, err := st.UpdateReviewStatusForAttempt(ctx, reviewID, 1, "failed", "stale generation state error", nil, nil); err != nil || applied {
		t.Fatalf("generation-fenced stale state error applied=%v err=%v, want false,nil", applied, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1 AND attempt_generation=2`, reviewID); err != nil {
		t.Fatal(err)
	}

	githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 2, func(context.Context) (int64, error) {
		calls.Add(1)
		return 9999, nil
	})
	if err != nil || githubReviewID != 8642 || outcome != ReviewPostAlreadyRecorded {
		t.Fatalf("repeat post = (%d,%q,%v), want recorded id without callback", githubReviewID, outcome, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("GitHub callback calls = %d, want exactly one", calls.Load())
	}
}

func TestRecoverStaleReviewsPreservesDurablePostAmbiguity(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	claim := reviewPostClaimMarker()
	if _, err := pool.Exec(ctx, `
		UPDATE reviews
		SET status='in_progress',error=$2,created_at=NOW()-INTERVAL '1 hour'
		WHERE id=$1
	`, reviewID, claim); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)

	recovered, err := st.RecoverStaleReviews(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recovered < 1 {
		t.Fatalf("recovered = %d, want claim row included", recovered)
	}
	var status string
	var storedError string
	if err := pool.QueryRow(ctx, `SELECT status,error FROM reviews WHERE id=$1`, reviewID).Scan(&status, &storedError); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || storedError != claim {
		t.Fatalf("stale recovery status=%q error=%q, want failed with preserved claim %q", status, storedError, claim)
	}

	// The legacy unconditional status writer is another raw reviews.error path.
	// It must preserve the same sentinel if invoked by an older state-machine seam.
	if err := st.UpdateReviewStatus(ctx, reviewID, "failed", "generic terminal error", nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&storedError); err != nil {
		t.Fatal(err)
	}
	if storedError != claim {
		t.Fatalf("unconditional status writer replaced durable claim with %q", storedError)
	}
	if generation, won, err := st.BeginReviewRetry(ctx, reviewID); err != nil || won || generation != 0 {
		t.Fatalf("retry after stale recovery = (%d,%v,%v), want ambiguity-blocked loser", generation, won, err)
	}
}

type definiteReviewPostFailure struct{ err error }

func (e definiteReviewPostFailure) Error() string              { return e.err.Error() }
func (e definiteReviewPostFailure) Unwrap() error              { return e.err }
func (e definiteReviewPostFailure) DefinitelyNotCreated() bool { return true }

func TestReviewPostDefiniteFailureClearsExactClaimAndCanRetry(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)

	remoteErr := definiteReviewPostFailure{err: errors.New("GitHub rejected request")}
	githubReviewID, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) {
		return 0, remoteErr
	})
	if !errors.Is(err, remoteErr.err) || githubReviewID != 0 || outcome != ReviewPostDefinitelyNotCreated {
		t.Fatalf("definite post = (%d,%q,%v), want definitely-not-created", githubReviewID, outcome, err)
	}
	var stateError *string
	if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil {
		t.Fatal(err)
	}
	if stateError != nil {
		t.Fatalf("definite failure retained claim %q", *stateError)
	}

	githubReviewID, outcome, err = st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) { return 7788, nil })
	if err != nil || githubReviewID != 7788 || outcome != ReviewPostRecorded {
		t.Fatalf("retry post = (%d,%q,%v), want recorded", githubReviewID, outcome, err)
	}
}

func TestReviewPostPreCallbackGuardFailureClearsExactClaim(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	injected := errors.New("second transaction unavailable")
	st.beginReviewPostTx = func(context.Context, *pgxpool.Conn) (pgx.Tx, error) { return nil, injected }
	called := false
	_, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) { called = true; return 1, nil })
	if !errors.Is(err, injected) || outcome != ReviewPostRejected || called {
		t.Fatalf("post = (%q,%v), called=%v", outcome, err, called)
	}
	var stateError *string
	if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil {
		t.Fatal(err)
	}
	if stateError != nil {
		t.Fatalf("pre-callback failure retained claim %q", *stateError)
	}
}

func TestClearReconciledReviewClaimUsesDatabaseClockAndExactCAS(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	st := NewWithDB(pool)

	// The marker timestamp is observability only. A machine one day ahead must
	// not keep a database-aged claim blocked.
	futureMarker := reviewPostClaimMarkerFor(reviewID, 1, time.Now().Add(24*time.Hour))
	if _, err := pool.Exec(ctx, `
		UPDATE reviews
		SET status='failed', error=$2,
		    review_post_claimed_at=NOW()-make_interval(secs => $3)
		WHERE id=$1
	`, reviewID, futureMarker, ReviewPostReconciliationMinAge.Seconds()); err != nil {
		t.Fatal(err)
	}
	if cleared, err := st.ClearReconciledReviewClaim(ctx, reviewID, 1, futureMarker); err != nil || !cleared {
		t.Fatalf("database-aged future-marker clear = %v,%v, want true,nil", cleared, err)
	}

	// Conversely, a machine one day behind must not authorize a recent claim.
	pastMarker := reviewPostClaimMarkerFor(reviewID, 1, time.Now().Add(-24*time.Hour))
	if _, err := pool.Exec(ctx, `
		UPDATE reviews SET error=$2, review_post_claimed_at=NOW() WHERE id=$1
	`, reviewID, pastMarker); err != nil {
		t.Fatal(err)
	}
	if cleared, err := st.ClearReconciledReviewClaim(ctx, reviewID, 1, pastMarker); !errors.Is(err, ErrReviewPostClaimTooRecent) || cleared {
		t.Fatalf("database-recent past-marker clear = %v,%v, want false,too-recent", cleared, err)
	}

	// The SQL boundary is inclusive: exactly five database minutes is old enough.
	boundaryMarker := reviewPostClaimMarkerFor(reviewID, 1, time.Now())
	if _, err := pool.Exec(ctx, `
		UPDATE reviews
		SET error=$2, review_post_claimed_at=NOW()-make_interval(secs => $3)
		WHERE id=$1
	`, reviewID, boundaryMarker, ReviewPostReconciliationMinAge.Seconds()); err != nil {
		t.Fatal(err)
	}
	if cleared, err := st.ClearReconciledReviewClaim(ctx, reviewID, 1, boundaryMarker); err != nil || !cleared {
		t.Fatalf("exact database-age boundary clear = %v,%v, want true,nil", cleared, err)
	}

	// A stale machine can never clear a replacement claim, even when both are old.
	replacement := boundaryMarker + " changed"
	if _, err := pool.Exec(ctx, `
		UPDATE reviews
		SET error=$2, review_post_claimed_at=NOW()-INTERVAL '1 hour'
		WHERE id=$1
	`, reviewID, replacement); err != nil {
		t.Fatal(err)
	}
	if cleared, err := st.ClearReconciledReviewClaim(ctx, reviewID, 1, boundaryMarker); err != nil || cleared {
		t.Fatalf("stale exact clear = %v,%v, want false,nil", cleared, err)
	}
}

func TestCompleteReconciledReviewElectsOneSameGenerationWinner(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	st := NewWithDB(pool)
	claim := reviewPostClaimMarkerFor(reviewID, 1, time.Now().Add(24*time.Hour))
	if _, err := pool.Exec(ctx, `
		UPDATE reviews
		SET status='failed', error=$2, review_post_claimed_at=NOW()
		WHERE id=$1
	`, reviewID, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_comments(review_id,attempt_generation,file_path,end_line,body)
		VALUES($1,1,'current.go',7,'original generation comment')
	`, reviewID); err != nil {
		t.Fatal(err)
	}

	const githubReviewID int64 = 4455
	if attached, err := st.AttachReconciledReviewID(ctx, reviewID, 1, claim, githubReviewID); err != nil || !attached {
		t.Fatalf("AttachReconciledReviewID = %v,%v", attached, err)
	}
	var winners atomic.Int32
	var already atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := st.CompleteReconciledReview(context.Background(), reviewID, 1, claim, githubReviewID)
			if err != nil {
				t.Errorf("CompleteReconciledReview: %v", err)
				return
			}
			switch outcome {
			case ReviewCompletionWon:
				winners.Add(1)
			case ReviewCompletionAlreadyCompleted:
				already.Add(1)
			default:
				t.Errorf("outcome = %q", outcome)
			}
		}()
	}
	wg.Wait()
	if got := winners.Load(); got != 1 {
		t.Fatalf("completion winners = %d, want 1", got)
	}
	if got := already.Load(); got != 11 {
		t.Fatalf("already-completed losers = %d, want 11", got)
	}

	var storedID int64
	var status string
	var generation int
	var stateError *string
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT github_review_id,status,attempt_generation,error,review_post_claimed_at
		FROM reviews WHERE id=$1
	`, reviewID).Scan(&storedID, &status, &generation, &stateError, &claimedAt); err != nil {
		t.Fatal(err)
	}
	if storedID != githubReviewID || status != "completed" || generation != 1 || stateError != nil || claimedAt != nil {
		t.Fatalf("review = id %d status %q generation %d error %v claimed_at %v", storedID, status, generation, stateError, claimedAt)
	}
	comments, err := st.GetReviewComments(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Body != "original generation comment" {
		t.Fatalf("current comments = %+v", comments)
	}
}

func TestReviewPostClaimCommitAmbiguityClearsBeforeExternalCall(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	st.beginReviewPostClaimTx = func(ctx context.Context, conn *pgxpool.Conn) (pgx.Tx, error) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return &reviewPostFaultTx{Tx: tx, commitErr: errors.New("claim commit response lost")}, nil
	}
	called := false
	_, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) { called = true; return 12, nil })
	if err == nil || outcome != ReviewPostRejected || called {
		t.Fatalf("post=(%q,%v) called=%v", outcome, err, called)
	}
	var stateError *string
	if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil {
		t.Fatal(err)
	}
	if stateError != nil {
		t.Fatalf("ambiguous local commit retained claim %q", *stateError)
	}
	st.beginReviewPostClaimTx = nil
	id, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) { return 13, nil })
	if err != nil || id != 13 || outcome != ReviewPostRecorded {
		t.Fatalf("retry=(%d,%q,%v)", id, outcome, err)
	}
}

type reviewPostErrorRow struct{ err error }

func (r reviewPostErrorRow) Scan(...any) error { return r.err }

type reviewPostQueryFaultTx struct {
	pgx.Tx
	err error
}

func (tx *reviewPostQueryFaultTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "SELECT attempt_generation,status,github_review_id FROM reviews") {
		return reviewPostErrorRow{err: tx.err}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

func TestReviewPostSecondGuardQueryFailureClearsClaim(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='in_progress' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	st := NewWithDB(pool)
	injected := errors.New("guard query failed")
	st.beginReviewPostTx = func(ctx context.Context, conn *pgxpool.Conn) (pgx.Tx, error) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return &reviewPostQueryFaultTx{Tx: tx, err: injected}, nil
	}
	called := false
	_, outcome, err := st.PostReviewForAttempt(ctx, reviewID, 1, func(context.Context) (int64, error) { called = true; return 1, nil })
	if !errors.Is(err, injected) || outcome != ReviewPostRejected || called {
		t.Fatalf("post=(%q,%v), called=%v", outcome, err, called)
	}
	var stateError *string
	if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil {
		t.Fatal(err)
	}
	if stateError != nil {
		t.Fatalf("guard failure retained claim %q", *stateError)
	}
}

func TestCompleteReconciledReviewWithEventsIsAtomicAndIdempotent(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	repoID, rawID := seedFileMemoryRepo(t, ctx, pool)
	reviewID := uuid.MustParse(rawID)
	claim := ErrReviewPostPersistenceAmbiguous.Error() + ": exact claim"
	const generation = 3
	const githubReviewID int64 = 991
	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed',error=$2,attempt_generation=$3,github_review_id=$4 WHERE id=$1`, reviewID, claim, generation, githubReviewID); err != nil {
		t.Fatal(err)
	}
	meta := ReconciledCompletionMetadata{RepoID: repoID, PRNumber: 1, InstallationID: 42}
	outcome, err := NewWithDB(pool).CompleteReconciledReviewWithEvents(ctx, reviewID, generation, claim, githubReviewID, meta)
	if err != nil || outcome != ReviewCompletionWon {
		t.Fatalf("first completion = %q, %v", outcome, err)
	}
	outcome, err = NewWithDB(pool).CompleteReconciledReviewWithEvents(ctx, reviewID, generation, claim, githubReviewID, meta)
	if err != nil || outcome != ReviewCompletionAlreadyCompleted {
		t.Fatalf("repeat completion = %q, %v", outcome, err)
	}
	var status string
	var total, distinctSemantic, distinctDelivery int
	if err := pool.QueryRow(ctx, `SELECT status FROM reviews WHERE id=$1`, reviewID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT semantic_key),count(DISTINCT data->>'delivery_id') FROM review_events WHERE review_id=$1 AND semantic_key LIKE 'recovered.%'`, reviewID).Scan(&total, &distinctSemantic, &distinctDelivery); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || total != 3 || distinctSemantic != 3 || distinctDelivery != 3 {
		t.Fatalf("status=%s events=%d semantic=%d delivery=%d", status, total, distinctSemantic, distinctDelivery)
	}
}
