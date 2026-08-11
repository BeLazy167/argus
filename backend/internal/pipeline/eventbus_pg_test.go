package pipeline

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func durableEventTestReview(t *testing.T) (*pgxpool.Pool, context.Context, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var installationID, repoID int64
	if err := pool.QueryRow(ctx, `INSERT INTO installations (installation_id,org_login) VALUES ((random()*1000000000)::bigint,'eventbus-test') RETURNING id`).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO repos (installation_id,github_id,full_name) VALUES ($1,(random()*1000000000)::bigint,'acme/eventbus-test') RETURNING id`, installationID).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	reviewID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reviews (id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha) VALUES ($1,$2,1,'test','bot','head','base')`, reviewID, repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE id=$1`, reviewID)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id=$1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id=$1`, installationID)
	})
	return pool, ctx, reviewID
}

func TestDurableEventBusCrossMachineDeliveryAndReplay(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	busA := NewDurableEventBus(ctx, pool, logger)
	busB := NewDurableEventBus(ctx, pool, logger)
	live, history, unsub, err := busB.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()
	if len(history) != 0 {
		t.Fatalf("initial history=%d", len(history))
	}

	busA.Publish(reviewID, EventStageChanged, map[string]string{"stage": "reviewing"})
	var liveEvent Event
	select {
	case liveEvent = <-live:
	case <-time.After(3 * time.Second):
		t.Fatal("cross-machine event not delivered")
	}
	if liveEvent.ID == 0 || liveEvent.Type != EventStageChanged {
		t.Fatalf("live event=%+v", liveEvent)
	}

	busC := NewDurableEventBus(ctx, pool, logger)
	_, replay, replayUnsub, err := busC.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer replayUnsub()
	if len(replay) != 1 || replay[0].ID != liveEvent.ID {
		t.Fatalf("replay=%+v live=%+v", replay, liveEvent)
	}
	_, after, afterUnsub, err := busC.SubscribeContext(ctx, reviewID, liveEvent.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer afterUnsub()
	if len(after) != 0 {
		t.Fatalf("cursor replay=%+v, want empty", after)
	}
}

func TestRecoveryClaimIsAtomicAcrossMachines(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	runID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (id,review_id,state,payload,updated_at) VALUES ($1,$2,$3,'{}',NOW()-INTERVAL '20 minutes')`, runID, reviewID, StatePosting); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	machines := []*StateMachine{{db: pool, logger: logger}, {db: pool, logger: logger}}
	type result struct {
		id      uuid.UUID
		claimed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, sm := range machines {
		go func(sm *StateMachine) {
			<-start
			id, claimed, err := sm.claimIncomplete(context.Background(), uuid.New())
			results <- result{id, claimed, err}
		}(sm)
	}
	close(start)
	winners := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.claimed {
			winners++
			if r.id != runID {
				t.Fatalf("claimed %s want %s", r.id, runID)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d want 1", winners)
	}
}

func TestDurableEventBusDropsStaleAttemptEvents(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	if _, err := pool.Exec(ctx, `UPDATE reviews SET attempt_generation=2 WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	bus := NewDurableEventBus(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bus.PublishForAttempt(reviewID, 1, EventCompleted, map[string]string{"status": "completed"})
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_events WHERE review_id=$1`, reviewID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale attempt persisted %d events", count)
	}
	bus.PublishForAttempt(reviewID, 2, EventStageChanged, map[string]string{"stage": "reviewing"})
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_events WHERE review_id=$1 AND attempt_generation=2`, reviewID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("current attempt events=%d want 1", count)
	}
}
