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

func TestDurableEventBusCatchUpAcrossChurnMultipleReviews(t *testing.T) {
	pool, ctx, reviewA := durableEventTestReview(t)
	_, _, reviewB := durableEventTestReview(t)
	_, _, unrelatedReview := durableEventTestReview(t)
	bus := NewEventBus()
	bus.pool = pool
	bus.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	liveA, _, unsubA, err := bus.SubscribeContext(ctx, reviewA, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubA()
	liveB, _, unsubB, err := bus.SubscribeContext(ctx, reviewB, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubB()
	// A topic without a live subscriber is deliberately outside catch-up. A
	// later SubscribeContext will replay its own authorized history.
	bus.OpenTopic(unrelatedReview)

	cursor, err := bus.reviewEventHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(reviewID uuid.UUID, generation int, eventType EventType) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,$2,$3,'{}') RETURNING id`, reviewID, generation, eventType).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	stageID := insert(reviewA, 1, EventStageChanged)
	_ = insert(unrelatedReview, 1, EventComment)
	commentID := insert(reviewB, 1, EventComment)
	completedID := insert(reviewA, 0, EventReviewCompleted)

	cursor, err = bus.catchUpStored(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents := func(name string, ch <-chan Event, wantIDs []int64, wantTypes []EventType) {
		t.Helper()
		for i := range wantIDs {
			select {
			case evt := <-ch:
				if evt.ID != wantIDs[i] || evt.Type != wantTypes[i] {
					t.Fatalf("%s event[%d]=%+v want id=%d type=%s", name, i, evt, wantIDs[i], wantTypes[i])
				}
			case <-time.After(time.Second):
				t.Fatalf("%s event[%d] not delivered", name, i)
			}
		}
	}
	assertEvents("review A", liveA, []int64{stageID, completedID}, []EventType{EventStageChanged, EventReviewCompleted})
	assertEvents("review B", liveB, []int64{commentID}, []EventType{EventComment})

	// Reconnect with no new rows: overlap must not duplicate delivery.
	cursor, err = bus.catchUpStored(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan Event{"review A": liveA, "review B": liveB} {
		select {
		case evt := <-ch:
			t.Fatalf("%s duplicate after churn: %+v", name, evt)
		default:
		}
	}
	bus.mu.RLock()
	unrelatedTopic := bus.topics[unrelatedReview]
	bus.mu.RUnlock()
	unrelatedTopic.mu.Lock()
	unrelatedHistory := len(unrelatedTopic.history)
	unrelatedTopic.mu.Unlock()
	if unrelatedHistory != 0 {
		t.Fatalf("unsubscribed cross-tenant topic received %d events", unrelatedHistory)
	}

	cancelledID := insert(reviewB, 0, EventCancelled)
	cursor, err = bus.catchUpStored(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents("review B terminal", liveB, []int64{cancelledID}, []EventType{EventCancelled})
	if cursor < cancelledID {
		t.Fatalf("cursor=%d did not reach terminal id=%d", cursor, cancelledID)
	}
}

func TestDurableEventBusCatchUpPaginatesBoundedQueries(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	bus := NewEventBus()
	bus.pool = pool
	bus.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	bus.OpenTopic(reviewID)
	bus.mu.RLock()
	topic := bus.topics[reviewID]
	bus.mu.RUnlock()
	live := make(chan Event, eventCatchUpBatchSize+2)
	topic.mu.Lock()
	topic.subscribers[1] = newTopicSubscriber(live, 0)
	topic.mu.Unlock()

	cursor, err := bus.reviewEventHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_events (review_id, attempt_generation, event_type, data)
		SELECT $1,1,$2,'{}' FROM generate_series(1,$3)`,
		reviewID, EventStageChanged, eventCatchUpBatchSize+1); err != nil {
		t.Fatal(err)
	}
	cursor, err = bus.catchUpStored(ctx, cursor)
	if err != nil {
		t.Fatal(err)
	}
	var previous int64
	for i := 0; i < eventCatchUpBatchSize+1; i++ {
		select {
		case evt := <-live:
			if evt.ID <= previous {
				t.Fatalf("event[%d] id=%d after %d; catch-up order is not increasing", i, evt.ID, previous)
			}
			previous = evt.ID
		case <-time.After(time.Second):
			t.Fatalf("received %d catch-up events, want %d", i, eventCatchUpBatchSize+1)
		}
	}
	if cursor != previous {
		t.Fatalf("cursor=%d last event=%d", cursor, previous)
	}
}

func TestDurableEventBusListenerReconnectCatchesDisconnectedInterval(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	applicationName := "eventbus-churn-" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	config.MaxConns = 3
	listenerPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listenerPool.Close)
	listenerCtx, cancelListener := context.WithCancel(context.Background())
	t.Cleanup(cancelListener)
	bus := NewDurableEventBus(listenerCtx, listenerPool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	live, _, unsubscribe, err := bus.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()

	listenerPID := func(previous int32) int32 {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			var pid int32
			err := pool.QueryRow(ctx, `
				SELECT pid FROM pg_stat_activity
				WHERE application_name=$1 AND query='LISTEN argus_review_events'
				  AND pid<>$2
				ORDER BY backend_start DESC LIMIT 1`, applicationName, previous).Scan(&pid)
			if err == nil {
				return pid
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("listener %q did not connect", applicationName)
		return 0
	}
	terminate := func(pid int32) {
		t.Helper()
		var terminated bool
		if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
			t.Fatal(err)
		}
		if !terminated {
			t.Fatalf("listener pid %d was not terminated", pid)
		}
	}
	insert := func(eventType EventType) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,0,$2,'{}') RETURNING id`, reviewID, eventType).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	assertLive := func(wantID int64, wantType EventType) {
		t.Helper()
		select {
		case evt := <-live:
			if evt.ID != wantID || evt.Type != wantType {
				t.Fatalf("live event=%+v want id=%d type=%s", evt, wantID, wantType)
			}
		case <-time.After(4 * time.Second):
			t.Fatalf("missed reconnect event id=%d type=%s", wantID, wantType)
		}
	}

	firstPID := listenerPID(0)
	terminate(firstPID)
	stageID := insert(EventStageChanged)
	completedID := insert(EventReviewCompleted)
	assertLive(stageID, EventStageChanged)
	assertLive(completedID, EventReviewCompleted)

	secondPID := listenerPID(firstPID)
	terminate(secondPID)
	cancelledID := insert(EventCancelled)
	assertLive(cancelledID, EventCancelled)
	select {
	case evt := <-live:
		t.Fatalf("duplicate after repeated connection churn: %+v", evt)
	default:
	}
}

func TestDurableEventBusReconnectCatchesLateCommitBelowFreshNotification(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	applicationName := "eventbus-order-" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	config.MaxConns = 3
	listenerPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listenerPool.Close)
	listenerCtx, cancelListener := context.WithCancel(context.Background())
	t.Cleanup(cancelListener)
	bus := NewDurableEventBus(listenerCtx, listenerPool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	live, _, unsubscribe, err := bus.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()

	slow, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Rollback(ctx) }()
	var slowID int64
	var notified string
	if err := slow.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,0,$2,'{}') RETURNING id
		)
		SELECT id, pg_notify('argus_review_events','remote:' || id::text)
		FROM inserted`, reviewID, EventComment).Scan(&slowID, &notified); err != nil {
		t.Fatal(err)
	}

	var fastID int64
	if err := pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,0,$2,'{}') RETURNING id
		)
		SELECT id, pg_notify('argus_review_events','remote:' || id::text)
		FROM inserted`, reviewID, EventStageChanged).Scan(&fastID, &notified); err != nil {
		t.Fatal(err)
	}
	if slowID >= fastID {
		t.Fatalf("test setup IDs slow=%d fast=%d", slowID, fastID)
	}
	select {
	case evt := <-live:
		if evt.ID != fastID {
			t.Fatalf("first live event=%+v want fast ID %d", evt, fastID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fast notification not delivered")
	}

	var listenerPID int32
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = pool.QueryRow(ctx, `
			SELECT pid FROM pg_stat_activity
			WHERE application_name=$1 AND query='LISTEN argus_review_events'
			ORDER BY backend_start DESC LIMIT 1`, applicationName).Scan(&listenerPID)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if listenerPID == 0 {
		t.Fatal("listener PID not found")
	}
	var terminated bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, listenerPID).Scan(&terminated); err != nil {
		t.Fatal(err)
	}
	if !terminated {
		t.Fatalf("listener pid %d was not terminated", listenerPID)
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case evt := <-live:
		if evt.ID != slowID || evt.Type != EventComment {
			t.Fatalf("late lower-ID event=%+v want id=%d type=%s", evt, slowID, EventComment)
		}
	case <-time.After(4 * time.Second):
		t.Fatalf("late committed ID %d was skipped below fast ID %d", slowID, fastID)
	}
	select {
	case evt := <-live:
		t.Fatalf("fast event duplicated by reconnect catch-up: %+v", evt)
	default:
	}
}

func TestDurableEventBusSubscribeBoundaryDeliversLateLowerFreshID(t *testing.T) {
	pool, ctx, reviewID := durableEventTestReview(t)
	listenerCtx, cancelListener := context.WithCancel(context.Background())
	t.Cleanup(cancelListener)
	bus := NewDurableEventBus(listenerCtx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	slow, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Rollback(ctx) }()
	var slowID int64
	var notified string
	if err := slow.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,0,$2,'{}') RETURNING id
		)
		SELECT id, pg_notify('argus_review_events','remote:' || id::text)
		FROM inserted`, reviewID, EventComment).Scan(&slowID, &notified); err != nil {
		t.Fatal(err)
	}
	var fastID int64
	if err := pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			VALUES ($1,0,$2,'{}') RETURNING id
		)
		SELECT id, pg_notify('argus_review_events','remote:' || id::text)
		FROM inserted`, reviewID, EventStageChanged).Scan(&fastID, &notified); err != nil {
		t.Fatal(err)
	}
	if slowID >= fastID {
		t.Fatalf("test setup IDs slow=%d fast=%d", slowID, fastID)
	}

	fromStart, history, unsubscribeStart, err := bus.SubscribeContext(ctx, reviewID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribeStart()
	if len(history) != 1 || history[0].ID != fastID {
		t.Fatalf("initial replay=%+v want fast ID %d", history, fastID)
	}
	// Mirrors a browser that processed fastID and reconnects while the lower-ID
	// transaction is still open. Its scalar after must not hide a future NOTIFY.
	fromAfter, afterHistory, unsubscribeAfter, err := bus.SubscribeContext(ctx, reviewID, fastID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribeAfter()
	if len(afterHistory) != 0 {
		t.Fatalf("after replay=%+v want empty", afterHistory)
	}

	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for name, live := range map[string]<-chan Event{"initial": fromStart, "after": fromAfter} {
		select {
		case evt := <-live:
			if evt.ID != slowID || evt.Type != EventComment {
				t.Fatalf("%s fresh event=%+v want id=%d type=%s", name, evt, slowID, EventComment)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s subscriber missed late lower ID %d below after %d", name, slowID, fastID)
		}
		select {
		case evt := <-live:
			t.Fatalf("%s subscriber received replay overlap duplicate: %+v", name, evt)
		default:
		}
	}
}
