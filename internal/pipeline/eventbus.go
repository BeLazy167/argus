package pipeline

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EventType classifies pipeline streaming events.
type EventType string

const (
	EventStageChanged   EventType = "stage_changed"
	EventTriageComplete EventType = "triage_complete"
	EventComment        EventType = "comment"
	EventScoringUpdate  EventType = "scoring_update"
	EventSynthesis      EventType = "synthesis"
	EventPatternLearned EventType = "pattern_learned"
	EventCompleted      EventType = "completed"
	EventError              EventType = "error"
	EventFileReviewStarted  EventType = "file_review_started"
	EventTokenUpdate        EventType = "token_update"
)

const maxHistoryEvents = 500

// Event is a single streaming event published during a pipeline run.
type Event struct {
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// EventBus provides per-review pub/sub for streaming pipeline events.
type EventBus struct {
	mu     sync.RWMutex
	topics map[uuid.UUID]*topic
	logger *slog.Logger
}

type topic struct {
	mu          sync.Mutex
	subscribers map[uint64]chan Event
	history     []Event
	closed      bool
	nextID      uint64
}

func NewEventBus() *EventBus {
	return &EventBus{
		topics: make(map[uuid.UUID]*topic),
		logger: slog.Default(),
	}
}

// OpenTopic creates a topic for a review. Safe to call multiple times.
func (eb *EventBus) OpenTopic(reviewID uuid.UUID) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if _, ok := eb.topics[reviewID]; !ok {
		eb.topics[reviewID] = &topic{
			subscribers: make(map[uint64]chan Event),
		}
	}
}

// CloseTopic marks a topic as closed and closes all subscriber channels.
// History is retained for 60s for late joiners, then GC'd.
func (eb *EventBus) CloseTopic(reviewID uuid.UUID) {
	eb.mu.RLock()
	t, ok := eb.topics[reviewID]
	eb.mu.RUnlock()
	if !ok {
		return
	}

	t.mu.Lock()
	t.closed = true
	for id, ch := range t.subscribers {
		close(ch)
		delete(t.subscribers, id)
	}
	t.mu.Unlock()

	// GC history after 60s
	go func() {
		time.Sleep(60 * time.Second)
		eb.mu.Lock()
		delete(eb.topics, reviewID)
		eb.mu.Unlock()
	}()
}

// Publish sends an event to all subscribers of a review topic.
// Non-blocking: drops events for slow clients.
func (eb *EventBus) Publish(reviewID uuid.UUID, evtType EventType, data any) {
	eb.mu.RLock()
	t, ok := eb.topics[reviewID]
	eb.mu.RUnlock()
	if !ok {
		return
	}

	raw, err := json.Marshal(data)
	if err != nil {
		eb.logger.Error("eventbus: marshal failed", "type", evtType, "review_id", reviewID, "error", err)
		return
	}

	evt := Event{
		Type:      evtType,
		Timestamp: time.Now(),
		Data:      raw,
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}

	if len(t.history) < maxHistoryEvents {
		t.history = append(t.history, evt)
	}

	for id, ch := range t.subscribers {
		select {
		case ch <- evt:
		default:
			eb.logger.Warn("eventbus: dropped event for slow client", "subscriber", id, "type", evtType, "review_id", reviewID)
		}
	}
}

// Subscribe returns a channel of events, the history so far, and an unsubscribe function.
// Returns nil channel if the topic doesn't exist.
func (eb *EventBus) Subscribe(reviewID uuid.UUID) (<-chan Event, []Event, func()) {
	eb.mu.RLock()
	t, ok := eb.topics[reviewID]
	eb.mu.RUnlock()
	if !ok {
		return nil, nil, func() {}
	}

	ch := make(chan Event, 64)
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	// If topic already closed, return history + closed channel
	if t.closed {
		history := make([]Event, len(t.history))
		copy(history, t.history)
		t.mu.Unlock()
		close(ch)
		return ch, history, func() {}
	}
	t.subscribers[id] = ch
	history := make([]Event, len(t.history))
	copy(history, t.history)
	t.mu.Unlock()

	unsub := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, exists := t.subscribers[id]; exists {
			delete(t.subscribers, id)
			close(ch)
		}
	}

	return ch, history, unsub
}
