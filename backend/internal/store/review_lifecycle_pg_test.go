package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
