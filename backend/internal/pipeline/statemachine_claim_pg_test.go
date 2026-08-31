// Package pipeline — statemachine_claim_pg_test.go exercises the recovery
// claim against real Postgres. Gated on TEST_DATABASE_URL like the other
// _pg_test files.
package pipeline

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestClaimIncompleteHonoursSkipList pins the backoff skip list through to the
// SQL. Claims order by updated_at, so without the exclusion a poisoned run is
// re-claimed FIRST on every sweep, starving every other stranded run.
func TestClaimIncompleteHonoursSkipList(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	sm := &StateMachine{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	older, younger := uuid.New(), uuid.New()
	for id, age := range map[uuid.UUID]time.Duration{older: 120 * time.Minute, younger: 90 * time.Minute} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO pipeline_states (id, review_id, state, payload, updated_at) VALUES ($1,$2,'posting','{}'::jsonb,$3)`,
			id, reviewID, time.Now().Add(-age)); err != nil {
			t.Fatal(err)
		}
	}

	skip := []string{older.String()}
	for i := 0; i < 50; i++ {
		claimed, ok, err := sm.claimIncomplete(ctx, uuid.New(), skip)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("nothing claimable, but this test inserted two stale runs")
		}
		if claimed == older {
			t.Fatalf("claimed the skipped run %s — the skip list never reaches the SQL", older)
		}
		if claimed == younger {
			return // correct: the skipped row was passed over
		}
		// A foreign row on a shared database: exclude it and keep looking.
		skip = append(skip, claimed.String())
		if _, err := pool.Exec(ctx,
			`UPDATE pipeline_states SET recovery_owner=NULL, recovery_lease_until=NULL WHERE id=$1`, claimed); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("run %s was never claimed", younger)
}

// TestClaimIncompleteHonoursClaimFloor pins that recovery uses the WIDER
// window: a run stale past the retry-liveness floor but not past the claim
// floor must stay untouched, or periodic scanning steals live work.
func TestClaimIncompleteHonoursClaimFloor(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	sm := &StateMachine{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// Between the two floors: retryable by a human, not claimable by recovery.
	between := recoverStaleAfter + (recoveryClaimStaleAfter-recoverStaleAfter)/2
	runID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO pipeline_states (id, review_id, state, payload, updated_at) VALUES ($1,$2,'reviewing','{}'::jsonb,$3)`,
		runID, reviewID, time.Now().Add(-between)); err != nil {
		t.Fatal(err)
	}

	var skip []string
	for i := 0; i < 50; i++ {
		claimed, ok, err := sm.claimIncomplete(ctx, uuid.New(), skip)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return // correct: nothing inside the claim floor was taken
		}
		if claimed == runID {
			t.Fatalf("claimed a run only %v stale; the claim floor is %v", between, recoveryClaimStaleAfter)
		}
		skip = append(skip, claimed.String())
		if _, err := pool.Exec(ctx,
			`UPDATE pipeline_states SET recovery_owner=NULL, recovery_lease_until=NULL WHERE id=$1`, claimed); err != nil {
			t.Fatal(err)
		}
	}
	// Falling out of the loop means a shared database held more claimable rows
	// than the budget: the claim floor was never actually exercised, so this
	// must not report success.
	t.Fatal("claim floor was never exercised: 50 foreign rows claimed first")
}
