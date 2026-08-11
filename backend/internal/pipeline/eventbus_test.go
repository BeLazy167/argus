package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type retryableEventPersistError struct{ message string }

func (e retryableEventPersistError) Error() string   { return e.message }
func (retryableEventPersistError) SafeToRetry() bool { return true }

func TestEventBus_PubSub(t *testing.T) {
	t.Run("subscribe_then_publish", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		ch, history, unsub := eb.Subscribe(id)
		defer unsub()

		if len(history) != 0 {
			t.Fatalf("expected empty history, got %d", len(history))
		}

		eb.Publish(id, EventStageChanged, map[string]string{"stage": "triage"})

		select {
		case evt := <-ch:
			if evt.Type != EventStageChanged {
				t.Errorf("type = %q, want %q", evt.Type, EventStageChanged)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event")
		}
	})

	t.Run("data_round_trip", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		ch, _, unsub := eb.Subscribe(id)
		defer unsub()

		type payload struct {
			Score int `json:"score"`
		}
		eb.Publish(id, EventScoringUpdate, payload{Score: 7})

		select {
		case evt := <-ch:
			var p payload
			if err := json.Unmarshal(evt.Data, &p); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if p.Score != 7 {
				t.Errorf("score = %d, want 7", p.Score)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	})

	t.Run("history_for_late_subscriber", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		eb.Publish(id, EventStageChanged, "first")
		eb.Publish(id, EventComment, "second")

		_, history, unsub := eb.Subscribe(id)
		defer unsub()

		if len(history) != 2 {
			t.Fatalf("history len = %d, want 2", len(history))
		}
		if history[0].Type != EventStageChanged {
			t.Errorf("history[0].Type = %q, want %q", history[0].Type, EventStageChanged)
		}
		if history[1].Type != EventComment {
			t.Errorf("history[1].Type = %q, want %q", history[1].Type, EventComment)
		}
	})

	t.Run("close_topic_closes_channel", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		ch, _, _ := eb.Subscribe(id)
		eb.CloseTopic(id)

		// channel should be closed
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("expected channel to be closed")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for channel close")
		}
	})

	t.Run("subscribe_to_closed_topic", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		eb.Publish(id, EventCompleted, "done")
		eb.CloseTopic(id)

		ch, history, _ := eb.Subscribe(id)

		if len(history) != 1 {
			t.Fatalf("history len = %d, want 1", len(history))
		}

		// channel should already be closed
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("expected closed channel")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	})

	t.Run("publish_nonexistent_topic_no_panic", func(t *testing.T) {
		eb := NewEventBus()
		// should not panic
		eb.Publish(uuid.New(), EventError, "oops")
	})

	t.Run("multiple_subscribers", func(t *testing.T) {
		eb := NewEventBus()
		id := uuid.New()
		eb.OpenTopic(id)

		ch1, _, unsub1 := eb.Subscribe(id)
		defer unsub1()
		ch2, _, unsub2 := eb.Subscribe(id)
		defer unsub2()

		eb.Publish(id, EventStageChanged, "x")

		for i, ch := range []<-chan Event{ch1, ch2} {
			select {
			case evt := <-ch:
				if evt.Type != EventStageChanged {
					t.Errorf("sub%d: type = %q, want %q", i, evt.Type, EventStageChanged)
				}
			case <-time.After(time.Second):
				t.Fatalf("sub%d: timed out", i)
			}
		}
	})
}

func TestEventBusLiveTerminalClosesTopicAfterDeliveringEverySubscriber(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)

	first, _, firstClosed, firstUnsub := eb.subscribeWithCloseReason(reviewID)
	defer firstUnsub()
	second, _, secondClosed, secondUnsub := eb.subscribeWithCloseReason(reviewID)
	defer secondUnsub()

	// deliverFrom models a terminal notification relayed by another app
	// instance: the local pipeline does not own this topic and cannot close it.
	eb.deliverFrom(reviewID, Event{ID: 42, Type: EventCompleted}, false, deliveryFresh)

	for name, subscriber := range map[string]struct {
		events <-chan Event
		closed <-chan SubscriberCloseReason
	}{
		"first":  {first, firstClosed},
		"second": {second, secondClosed},
	} {
		select {
		case evt, ok := <-subscriber.events:
			if !ok || evt.ID != 42 || evt.Type != EventCompleted {
				t.Fatalf("%s terminal event=(%+v,%v)", name, evt, ok)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive terminal event", name)
		}
		select {
		case _, ok := <-subscriber.events:
			if ok {
				t.Fatalf("%s subscription remained open after terminal", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s subscription was not closed after terminal", name)
		}
		select {
		case reason := <-subscriber.closed:
			if reason != SubscriberCloseTopic {
				t.Fatalf("%s close reason=%q want %q", name, reason, SubscriberCloseTopic)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s close reason was not reported", name)
		}
	}

	// Late subscribers can replay the retained terminal, but cannot keep the
	// terminal topic live while its pointer-guarded history GC is pending.
	late, history, lateClosed, _ := eb.subscribeWithCloseReason(reviewID)
	if len(history) != 1 || history[0].ID != 42 {
		t.Fatalf("late history=%+v", history)
	}
	select {
	case _, ok := <-late:
		if ok {
			t.Fatal("late subscriber joined a topic that should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("late subscription was not closed")
	}
	select {
	case reason := <-lateClosed:
		if reason != SubscriberCloseTopic {
			t.Fatalf("late close reason=%q want %q", reason, SubscriberCloseTopic)
		}
	case <-time.After(time.Second):
		t.Fatal("late close reason was not reported")
	}
}

// TestEventBus_NewEventTypes_Registry asserts the 26 EventType constants
// (11 existing + 13 sub-step events + 1 memory-match + 1 review-completed)
// are all non-empty and distinct. Guards against copy-paste collisions
// and empty-string bugs.
func TestEventBus_NewEventTypes_Registry(t *testing.T) {
	all := []EventType{
		// existing
		EventStageChanged, EventTriageComplete, EventComment, EventScoringUpdate,
		EventSynthesis, EventPatternLearned, EventCompleted, EventError,
		EventCancelled, EventFileReviewStarted, EventTokenUpdate,
		// sub-step events — names fixed by eventbus.go; rename => compile error.
		EventIntentExtracted, EventIntentVerified, EventFindingsEnriched,
		EventBriefGenerated, EventLeadBrief, EventBlastRadius,
		EventAcceptanceChecked, EventCrossPRChecked,
		EventSimulationsComplete, EventScenarioSimulated,
		EventMemoryIndexed, EventPostedToGitHub, EventReplyGenerated,
		// memory-match
		EventMemoryMatched,
		// cross-review lifecycle
		EventReviewCompleted,
	}
	if len(all) != 26 {
		t.Fatalf("expected 26 event types, listed %d", len(all))
	}
	seen := make(map[EventType]int, len(all))
	for i, e := range all {
		if e == "" {
			t.Errorf("event[%d] is empty string", i)
		}
		if prev, dup := seen[e]; dup {
			t.Errorf("duplicate event %q at index %d (first at %d)", e, i, prev)
		}
		seen[e] = i
	}
}

// TestEventBus_NewEventTypes_PubSub round-trips each of the 13 new types
// through the bus to confirm they are wired end-to-end. Data-shape is
// covered by the existing TestEventBus_PubSub; here we only assert Type
// propagation and payload round-trip.
func TestEventBus_NewEventTypes_PubSub(t *testing.T) {
	type marker struct {
		M string `json:"m"`
	}

	cases := []struct {
		name string
		evt  EventType
	}{
		{"intent_extracted", EventIntentExtracted},
		{"intent_verified", EventIntentVerified},
		{"findings_enriched", EventFindingsEnriched},
		{"brief_generated", EventBriefGenerated},
		{"lead_brief", EventLeadBrief},
		{"blast_radius", EventBlastRadius},
		{"acceptance_checked", EventAcceptanceChecked},
		{"cross_pr_checked", EventCrossPRChecked},
		{"simulations_complete", EventSimulationsComplete},
		{"scenario_simulated", EventScenarioSimulated},
		{"memory_indexed", EventMemoryIndexed},
		{"posted_to_github", EventPostedToGitHub},
		{"reply_generated", EventReplyGenerated},
	}
	if len(cases) != 13 {
		t.Fatalf("expected 13 new events, have %d", len(cases))
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			eb := NewEventBus()
			id := uuid.New()
			eb.OpenTopic(id)
			ch, _, unsub := eb.Subscribe(id)
			defer unsub()

			eb.Publish(id, tc.evt, marker{M: tc.name})

			select {
			case got := <-ch:
				if got.Type != tc.evt {
					t.Errorf("type = %q, want %q", got.Type, tc.evt)
				}
				var m marker
				if err := json.Unmarshal(got.Data, &m); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if m.M != tc.name {
					t.Errorf("payload = %q, want %q", m.M, tc.name)
				}
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for %s", tc.evt)
			}
		})
	}
}

func TestEventBus_Concurrent(t *testing.T) {
	eb := NewEventBus()
	id := uuid.New()
	eb.OpenTopic(id)

	ch, _, unsub := eb.Subscribe(id)
	defer unsub()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(v int) {
			defer wg.Done()
			eb.Publish(id, EventComment, v)
		}(i)
	}
	wg.Wait()

	// drain and count
	received := 0
	for {
		select {
		case <-ch:
			received++
		default:
			goto done
		}
	}
done:
	if received != n {
		t.Errorf("received %d events, want %d", received, n)
	}
}

func TestEventBusOpenTopicStartsFreshGenerationAfterClose(t *testing.T) {
	eb := NewEventBus()
	id := uuid.New()
	eb.OpenTopic(id)
	eb.Publish(id, EventError, "old")
	eb.CloseTopic(id)
	eb.OpenTopic(id)
	events, history, unsub := eb.Subscribe(id)
	defer unsub()
	if events == nil {
		t.Fatal("reopened topic has no live subscription")
	}
	if len(history) != 0 {
		t.Fatalf("reopened history=%d want 0", len(history))
	}
	eb.Publish(id, EventStageChanged, "new")
	select {
	case evt := <-events:
		if evt.Type != EventStageChanged {
			t.Fatalf("event=%s", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("reopened topic did not deliver")
	}
}

func TestDurableEventBusPublishDegradesWithoutSkippingLocalLifecycle(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	attempts := 0
	eb.persistEvent = func(context.Context, uuid.UUID, *Event) (bool, error) {
		attempts++
		return true, retryableEventPersistError{message: "postgres unavailable"}
	}
	live, _, unsub := eb.Subscribe(reviewID)
	defer unsub()
	global := make(chan Event, 1)
	eb.SubscribeGlobal(func(_ uuid.UUID, evt Event) { global <- evt })

	eb.PublishForAttempt(reviewID, 1, EventReviewCompleted, ReviewCompletedPayload{ReviewID: reviewID})
	if attempts != durablePublishAttempts {
		t.Fatalf("persistence attempts=%d want %d", attempts, durablePublishAttempts)
	}
	for name, events := range map[string]<-chan Event{"topic": live, "global": global} {
		select {
		case evt := <-events:
			if evt.Type != EventReviewCompleted || evt.ID != 0 {
				t.Fatalf("%s event=%+v", name, evt)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s delivery was suppressed by durable failure", name)
		}
	}
}

func TestDurableEventBusPublishRetriesBeforeDelivery(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	attempts := 0
	eb.persistEvent = func(_ context.Context, _ uuid.UUID, evt *Event) (bool, error) {
		attempts++
		if attempts < durablePublishAttempts {
			return true, retryableEventPersistError{message: "transient"}
		}
		evt.ID = 42
		evt.Timestamp = time.Unix(42, 0)
		return true, nil
	}
	live, _, unsub := eb.Subscribe(reviewID)
	defer unsub()
	eb.PublishForAttempt(reviewID, 1, EventStageChanged, map[string]string{"stage": "reviewing"})
	select {
	case evt := <-live:
		if evt.ID != 42 {
			t.Fatalf("event ID=%d want durable ID 42", evt.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("retried durable event was not delivered")
	}
	if attempts != durablePublishAttempts {
		t.Fatalf("persistence attempts=%d want %d", attempts, durablePublishAttempts)
	}
}

func TestDurableEventBusStaleAttemptIsNotDelivered(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.persistEvent = func(context.Context, uuid.UUID, *Event) (bool, error) {
		return false, nil
	}
	live, _, unsub := eb.Subscribe(reviewID)
	defer unsub()
	globalCalls := 0
	eb.SubscribeGlobal(func(uuid.UUID, Event) { globalCalls++ })
	eb.PublishForAttempt(reviewID, 1, EventCompleted, nil)
	select {
	case evt := <-live:
		t.Fatalf("stale event delivered: %+v", evt)
	default:
	}
	if globalCalls != 0 {
		t.Fatalf("stale event reached %d global subscribers", globalCalls)
	}
}

func TestDurableEventBusDoesNotRetryAmbiguousWriteFailure(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	attempts := 0
	eb.persistEvent = func(context.Context, uuid.UUID, *Event) (bool, error) {
		attempts++
		return true, errors.New("connection lost after write may have committed")
	}
	live, _, unsub := eb.Subscribe(reviewID)
	defer unsub()
	eb.PublishForAttempt(reviewID, 1, EventComment, nil)
	if attempts != 1 {
		t.Fatalf("ambiguous write attempts=%d want 1; retry could duplicate a committed event", attempts)
	}
	select {
	case <-live:
	case <-time.After(time.Second):
		t.Fatal("ambiguous durable failure suppressed local delivery")
	}
}

func TestDurableEventBusSubscriberCursorsAreIndependent(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()

	first := make(chan Event, 4)
	second := make(chan Event, 4)
	topic.mu.Lock()
	topic.subscribers[1] = newTopicSubscriber(first, 10)
	// Mirrors a later SubscribeContext with scalar after=20. That scalar does
	// not prove ID 15 was visible when 20 committed.
	topic.subscribers[2] = newTopicSubscriber(second, 20)
	topic.mu.Unlock()

	eb.deliverFrom(reviewID, Event{ID: 15, Type: EventComment}, false, deliveryCatchUp)
	select {
	case evt := <-first:
		if evt.ID != 15 {
			t.Fatalf("first subscriber event=%+v", evt)
		}
	default:
		t.Fatal("first subscriber missed durable event")
	}
	select {
	case evt := <-second:
		if evt.ID != 15 {
			t.Fatalf("second subscriber event=%+v", evt)
		}
	default:
		t.Fatal("scalar replay floor hid unseen lower-ID event")
	}

	// Ephemeral fallback events never participate in durable ordering and must
	// reach both subscribers even when their durable cursors differ.
	eb.deliver(reviewID, Event{ID: 0, Type: EventReviewCompleted}, false)
	for name, ch := range map[string]<-chan Event{"first": first, "second": second} {
		select {
		case evt := <-ch:
			if evt.ID != 0 || evt.Type != EventReviewCompleted {
				t.Fatalf("%s ephemeral event=%+v", name, evt)
			}
		default:
			t.Fatalf("%s subscriber missed ephemeral event", name)
		}
	}
}

func TestRecoverStoredNotificationRetriesFailedFetchWithoutAdvancingCursor(t *testing.T) {
	ctx := context.Background()
	loadErr := errors.New("transient stored fetch")
	catchErr := errors.New("transient catch-up fetch")
	loads := 0
	catches := 0

	cursor, err := recoverStoredNotification(ctx, 50, 40,
		func(context.Context, int64) error {
			loads++
			return loadErr
		},
		func(_ context.Context, after int64) (int64, error) {
			catches++
			if after != 39 {
				t.Fatalf("catch-up after=%d want 39", after)
			}
			return after, catchErr
		})
	if !errors.Is(err, loadErr) || !errors.Is(err, catchErr) {
		t.Fatalf("recovery error=%v", err)
	}
	if cursor != 39 {
		t.Fatalf("failed recovery cursor=%d want 39", cursor)
	}

	terminalDelivered := 0
	cursor, err = recoverStoredNotification(ctx, cursor, 40,
		func(context.Context, int64) error {
			loads++
			return loadErr
		},
		func(_ context.Context, after int64) (int64, error) {
			catches++
			if after != 39 {
				t.Fatalf("retried catch-up after=%d want 39", after)
			}
			terminalDelivered++
			return 55, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 55 || terminalDelivered != 1 || loads != 2 || catches != 2 {
		t.Fatalf("cursor=%d terminal=%d loads=%d catches=%d", cursor, terminalDelivered, loads, catches)
	}
}

func TestDurableEventBusFreshNotificationsAllowOutOfOrderIDs(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	live := make(chan Event, 4)
	topic.mu.Lock()
	topic.subscribers[1] = newTopicSubscriber(live, 0)
	topic.mu.Unlock()

	// BIGSERIAL allocates before commit: transaction 100 can notify before a
	// still-open transaction 51. Both are fresh live events, not duplicates.
	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryFresh)
	eb.deliverFrom(reviewID, Event{ID: 51, Type: EventComment}, false, deliveryFresh)
	// A repeated point notification for the same row is still suppressed.
	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryFresh)

	for i, want := range []int64{100, 51} {
		select {
		case evt := <-live:
			if evt.ID != want {
				t.Fatalf("event[%d].ID=%d want %d", i, evt.ID, want)
			}
		default:
			t.Fatalf("event[%d] missing", i)
		}
	}
	select {
	case evt := <-live:
		t.Fatalf("duplicate fresh notification delivered: %+v", evt)
	default:
	}

	_, history, unsubscribe := eb.Subscribe(reviewID)
	defer unsubscribe()
	if len(history) != 2 || history[0].ID != 100 || history[1].ID != 51 {
		t.Fatalf("out-of-order history=%+v", history)
	}
}

func TestDurableEventBusSlowSubscriberIsClosedBeforeDurableDrop(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	live := make(chan Event, 1)
	subscriber := newTopicSubscriber(live, 0)
	topic.mu.Lock()
	topic.subscribers[1] = subscriber
	topic.mu.Unlock()

	eb.deliverFrom(reviewID, Event{ID: 10, Type: EventStageChanged}, false, deliveryFresh)
	eb.deliverFrom(reviewID, Event{ID: 11, Type: EventComment}, false, deliveryFresh)

	topic.mu.Lock()
	_, retained := topic.subscribers[1]
	topic.mu.Unlock()
	if retained {
		t.Fatal("slow subscriber retained after durable channel overflow")
	}
	if evt, ok := <-live; !ok || evt.ID != 10 {
		t.Fatalf("buffered event=(%+v,%v) want ID 10 before close", evt, ok)
	}
	if evt, ok := <-live; ok {
		t.Fatalf("overflow event was treated as delivered: %+v", evt)
	}
	if reason := <-subscriber.closed; reason != SubscriberCloseDurableOverflow {
		t.Fatalf("overflow close reason=%q want %q", reason, SubscriberCloseDurableOverflow)
	}
}

func TestMaxDurableEventIDIgnoresTrailingEphemeralEvents(t *testing.T) {
	events := []Event{{ID: 12}, {ID: 0, Type: EventReviewCompleted}}
	if got := maxDurableEventID(7, events); got != 12 {
		t.Fatalf("max durable ID=%d want 12", got)
	}
	if got := maxDurableEventID(20, events); got != 20 {
		t.Fatalf("existing after ID=%d want 20", got)
	}
}

func TestDurableEventBusCatchUpOverlapDoesNotHideFreshLowerID(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	live := make(chan Event, 4)
	topic.mu.Lock()
	topic.subscribers[1] = newTopicSubscriber(live, 0)
	topic.mu.Unlock()

	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryCatchUp)
	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryFresh)
	eb.deliverFrom(reviewID, Event{ID: 51, Type: EventComment}, false, deliveryFresh)

	for i, want := range []int64{100, 51} {
		select {
		case evt := <-live:
			if evt.ID != want {
				t.Fatalf("event[%d].ID=%d want %d", i, evt.ID, want)
			}
		default:
			t.Fatalf("event[%d] missing", i)
		}
	}
	select {
	case evt := <-live:
		t.Fatalf("catch-up overlap duplicated: %+v", evt)
	default:
	}
}

func TestRecoverStoredNotificationDoesNotTrustSequenceOrder(t *testing.T) {
	catchCalled := false
	cursor, err := recoverStoredNotification(context.Background(), 50, 100,
		func(context.Context, int64) error { return nil },
		func(context.Context, int64) (int64, error) {
			catchCalled = true
			return 0, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 50 {
		t.Fatalf("fresh notification advanced cursor=%d want safe checkpoint 50", cursor)
	}
	if catchCalled {
		t.Fatal("successful point notification unexpectedly ran catch-up")
	}
}

func TestDurableEventBusReplayFloorDoesNotHideLateFreshLowerID(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	live := make(chan Event, 4)
	replayed := []Event{{ID: 100, Type: EventStageChanged}, {ID: 0, Type: EventReviewCompleted}}
	subscriber := newTopicSubscriber(live, maxDurableEventID(0, replayed))
	subscriber.seedReplay(replayed)
	topic.mu.Lock()
	topic.subscribers[1] = subscriber
	topic.mu.Unlock()

	// The queued copy of replayed ID 100 is an exact duplicate. ID 51 is not:
	// its transaction committed only after the replay snapshot.
	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryFresh)
	eb.deliverFrom(reviewID, Event{ID: 51, Type: EventComment}, false, deliveryFresh)
	select {
	case evt := <-live:
		if evt.ID != 51 {
			t.Fatalf("fresh event=%+v want late lower ID 51", evt)
		}
	default:
		t.Fatal("late lower-ID fresh notification was hidden by replay floor")
	}
	select {
	case evt := <-live:
		t.Fatalf("replayed ID was duplicated: %+v", evt)
	default:
	}
}

func TestDurableEventBusCatchUpFloorDoesNotHideUnseenLowerID(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	live := make(chan Event, 2)
	subscriber := newTopicSubscriber(live, 100)
	subscriber.seedReplay([]Event{{ID: 100, Type: EventStageChanged}})
	topic.mu.Lock()
	topic.subscribers[1] = subscriber
	topic.mu.Unlock()

	// A scalar high-water is not proof that every lower sequence transaction
	// committed before the replay snapshot. Catch-up suppresses exact IDs only.
	eb.deliverFrom(reviewID, Event{ID: 51, Type: EventComment}, false, deliveryCatchUp)
	select {
	case evt := <-live:
		if evt.ID != 51 {
			t.Fatalf("catch-up event=%+v want late lower ID 51", evt)
		}
	default:
		t.Fatal("catch-up floor hid unseen lower ID")
	}

	eb.deliverFrom(reviewID, Event{ID: 100, Type: EventStageChanged}, false, deliveryCatchUp)
	select {
	case evt := <-live:
		t.Fatalf("exact replay duplicate delivered: %+v", evt)
	default:
	}
}

func TestDurableEventBusAmbiguousLocalAndReplayShareLogicalDeliveryIdentity(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	var stored Event
	attempts := 0
	eb.persistEvent = func(_ context.Context, _ uuid.UUID, evt *Event) (bool, error) {
		attempts++
		stored = *evt
		stored.Data = append(json.RawMessage(nil), evt.Data...)
		return true, errors.New("connection lost after commit acknowledgement")
	}
	live, _, unsub := eb.Subscribe(reviewID)
	defer unsub()

	payload := map[string]any{"body": "user payload", "nested": map[string]any{"delivery_id": "belongs-to-user"}}
	eb.PublishForAttempt(reviewID, 2, EventComment, payload)
	if attempts != 1 {
		t.Fatalf("ambiguous durable write attempts=%d want 1", attempts)
	}
	local := <-live
	if local.ID != 0 || local.DeliveryID == "" {
		t.Fatalf("local fallback=%+v, want ID=0 with logical delivery identity", local)
	}
	var localPayload map[string]any
	if err := json.Unmarshal(local.Data, &localPayload); err != nil {
		t.Fatal(err)
	}
	if localPayload["body"] != "user payload" {
		t.Fatalf("local payload mutated: %s", local.Data)
	}

	stored.ID = 73
	stored.Timestamp = time.Unix(73, 0)
	eb.deliverFrom(reviewID, stored, false, deliveryCatchUp)
	replay := <-live
	if replay.ID != 73 || replay.DeliveryID != local.DeliveryID {
		t.Fatalf("replay=%+v local=%+v", replay, local)
	}
	if string(replay.Data) != string(local.Data) {
		t.Fatalf("replay payload=%s local payload=%s", replay.Data, local.Data)
	}
}

func TestDurableEventBusDedupExhaustionHasRetryableCloseReason(t *testing.T) {
	eb := NewEventBus()
	reviewID := uuid.New()
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	topic := eb.topics[reviewID]
	eb.mu.RUnlock()
	subscriber := newTopicSubscriber(make(chan Event, 1), 0)
	for id := int64(1); id <= subscriberSeenCapacity; id++ {
		subscriber.remember(id, deliveryCatchUp)
	}
	topic.mu.Lock()
	topic.subscribers[1] = subscriber
	topic.mu.Unlock()

	eb.deliverFrom(reviewID, Event{ID: subscriberSeenCapacity + 1, Type: EventComment}, false, deliveryCatchUp)
	select {
	case reason := <-subscriber.closed:
		if reason != SubscriberCloseDedupExhausted {
			t.Fatalf("close reason=%q want %q", reason, SubscriberCloseDedupExhausted)
		}
	case <-time.After(time.Second):
		t.Fatal("dedup exhaustion did not signal subscriber closure")
	}
}
