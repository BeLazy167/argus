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
	st := &Store{Pool: pool}

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
	st := &Store{Pool: pool}

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

	if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed', github_review_id=123, error='post write failed' WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	ghID, applied, err := st.ConvergePostedReview(ctx, reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if !applied || ghID != 123 {
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
	if _, applied, err := st.ConvergePostedReview(ctx, reviewID); err != nil || applied {
		t.Fatalf("cancelled converge applied=%v err=%v", applied, err)
	}
}
