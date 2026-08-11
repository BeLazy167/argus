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
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Cleanup is LIFO: cancel listener contexts before waiting for pool.Close.
	t.Cleanup(cancel)
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
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (id,review_id,state,payload,updated_at) VALUES ($1,$2,$3,'{}','-infinity'::timestamptz)`, runID, reviewID, StatePosting); err != nil {
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
	targetClaims := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		// A shared integration database may contain another eligible run. It is
		// correct for the other machine to claim that different run; this test
		// asserts only that exactly one machine can own the target generation.
		if r.claimed && r.id == runID {
			targetClaims++
		}
	}
	if targetClaims != 1 {
		t.Fatalf("target claim winners=%d want 1", targetClaims)
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

func TestDurableEventBusDeliveryRechecksAttemptAuthority(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	bus := NewDurableEventBus(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	live, _, unsubscribe, err := bus.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()

	var staleID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data)
		VALUES ($1, 1, $2, '{}') RETURNING id`, reviewID, EventCompleted).Scan(&staleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reviews SET attempt_generation=2 WHERE id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}

	// This is the same fetch path used after a remote NOTIFY. The event was
	// current when inserted but lost authority before delivery.
	if err := bus.deliverStored(ctx, staleID); err != nil {
		t.Fatalf("stale delivery should be benign: %v", err)
	}
	select {
	case event := <-live:
		t.Fatalf("delivered stale event: %+v", event)
	case <-time.After(100 * time.Millisecond):
	}

	var durableID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data)
		VALUES ($1, 0, $2, '{}') RETURNING id`, reviewID, EventStageChanged).Scan(&durableID); err != nil {
		t.Fatal(err)
	}
	if err := bus.deliverStored(ctx, durableID); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-live:
		if event.ID != durableID {
			t.Fatalf("delivered event id=%d want %d", event.ID, durableID)
		}
	case <-time.After(time.Second):
		t.Fatal("generation-zero event was not delivered")
	}
}

func TestDurableEventBusPrunesOutsideReplayGuarantees(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := NewEventBus()
	bus.pool = pool
	bus.logger = logger
	now := time.Now().UTC()

	// More than one replay window, all old enough that delayed NOTIFY fetches
	// cannot still be in flight.
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data, created_at)
		SELECT $1, 1, $2, '{}', $3
		FROM generate_series(1, $4)`, reviewID, EventStageChanged,
		now.Add(-2*eventPruneSafetyWindow), maxHistoryEvents+10); err != nil {
		t.Fatal(err)
	}
	var expiredID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data, created_at)
		VALUES ($1,1,$2,'{}',$3) RETURNING id`, reviewID, EventComment,
		now.Add(-eventRetention-time.Hour)).Scan(&expiredID); err != nil {
		t.Fatal(err)
	}
	var recentID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data, created_at)
		VALUES ($1,1,$2,'{}',$3) RETURNING id`, reviewID, EventComment,
		now).Scan(&recentID); err != nil {
		t.Fatal(err)
	}

	deleted, err := bus.pruneReviewEvents(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 13 { // twelve beyond replay capacity plus the expired row
		t.Fatalf("deleted=%d want 13", deleted)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_events WHERE review_id=$1`, reviewID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != maxHistoryEvents-1 { // absolute expiry can shrink the replay window
		t.Fatalf("remaining=%d want %d", remaining, maxHistoryEvents-1)
	}
	for name, id := range map[string]int64{"expired": expiredID, "recent": recentID} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM review_events WHERE id=$1)`, id).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if name == "expired" && exists {
			t.Fatal("expired event survived absolute retention")
		}
		if name == "recent" && !exists {
			t.Fatal("recent event was pruned despite notification safety window")
		}
	}
}
