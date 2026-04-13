package pipeline

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
