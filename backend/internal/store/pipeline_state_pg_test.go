package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedPipelineRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reviewID uuid.UUID, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (review_id, state) VALUES ($1, $2)`, reviewID, state); err != nil {
		t.Fatalf("seed pipeline run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM pipeline_states WHERE review_id = $1`, reviewID)
	})
}

func TestGetLatestRunStateForReview(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	_, withRun, withoutRun := seedLearnTenant(t, ctx, pool, "mcp-run-state")
	st := NewWithDB(pool)

	// Two runs; the most recently UPDATED one wins, which is the documented
	// recovered-run semantics. The bump goes on the OLDER row deliberately:
	// bumping the newer one would leave insert order, created_at, and
	// updated_at all agreeing, so an `ORDER BY created_at`/`ORDER BY id`
	// implementation would pass the assertion without ordering by
	// updated_at at all. Making the oldest row the freshest is the only
	// arrangement the wrong implementations actually fail.
	seedPipelineRun(t, ctx, pool, withRun, "reviewing")
	seedPipelineRun(t, ctx, pool, withRun, "synthesizing")
	if _, err := pool.Exec(ctx, `UPDATE pipeline_states SET updated_at = NOW() + interval '1 minute' WHERE review_id = $1 AND state = 'reviewing'`, withRun); err != nil {
		t.Fatal(err)
	}

	state, err := st.GetLatestRunStateForReview(ctx, withRun)
	if err != nil {
		t.Fatalf("GetLatestRunStateForReview: %v", err)
	}
	if state != "reviewing" {
		t.Fatalf("state = %q, want reviewing (the most recently updated run, seeded first)", state)
	}

	if _, err := st.GetLatestRunStateForReview(ctx, withoutRun); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("review with no run: err = %v, want pgx.ErrNoRows", err)
	}
}
