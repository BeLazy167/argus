package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
