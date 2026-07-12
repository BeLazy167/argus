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
	EventStageChanged      EventType = "stage_changed"
	EventTriageComplete    EventType = "triage_complete"
	EventComment           EventType = "comment"
	EventScoringUpdate     EventType = "scoring_update"
	EventSynthesis         EventType = "synthesis"
	EventPatternLearned    EventType = "pattern_learned"
	EventCompleted         EventType = "completed"
	EventError             EventType = "error"
	EventCancelled         EventType = "cancelled"
	EventFileReviewStarted EventType = "file_review_started"
	EventTokenUpdate       EventType = "token_update"

	// Per-sub-step events — each distinct LLM call, memory upsert, or GitHub API
	// action that previously fired no event. EventMemoryIndexed payload carries
	// a "kind" field (patterns | conventions | file_synthesis | pr_summary |
	// arch_summary | arch_graph | patterns_praise) so one type covers all
	// Supermemory upserts.
	EventIntentExtracted     EventType = "intent_extracted"
	EventIntentVerified      EventType = "intent_verified"
	EventFindingsEnriched    EventType = "findings_enriched"
	EventBriefGenerated      EventType = "brief_generated"
	EventLeadBrief           EventType = "lead_brief"
	EventBlastRadius         EventType = "blast_radius"
	EventAcceptanceChecked   EventType = "acceptance_checked"
	EventCrossPRChecked      EventType = "cross_pr_checked"
	EventSimulationsComplete EventType = "simulations_complete"
	EventScenarioSimulated   EventType = "scenario_simulated"
	EventMemoryIndexed       EventType = "memory_indexed"
	EventPostedToGitHub      EventType = "posted_to_github"
	EventReplyGenerated      EventType = "reply_generated"
	// EventMemoryMatched fires when enrichFindings tags a finding with a
	// Supermemory-backed pattern / convention / rule / similarity hit. Payload
	// carries {file, line, kind, pr, score} so the live stream can show per-
	// finding memory context alongside the formatted review body tag.
	EventMemoryMatched EventType = "memory_matched"
	// EventReviewCompleted fires after the DB commit that sets
	// reviews.status='completed' returns nil. Distinct from EventCompleted
	// (which is per-review SSE UI signal) — this one is the in-process
	// lifecycle hook used by cross-review stages (e.g. async cross-PR
	// analysis; see crosspr_stage.go:OnReviewCompleted).
	EventReviewCompleted EventType = "review_completed"
)

// ReviewCompletedPayload is the data carried by EventReviewCompleted.
// Minimal by design — subscribers should re-hydrate from the store rather
// than rely on snapshot data here. The review row is guaranteed durable
// at publish time.
type ReviewCompletedPayload struct {
	ReviewID       uuid.UUID `json:"review_id"`
	RepoID         int64     `json:"repo_id"`
	PRNumber       int       `json:"pr_number"`
	InstallationID int64     `json:"installation_id"`
}

const maxHistoryEvents = 500

// Event is a single streaming event published during a pipeline run.
type Event struct {
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// EventBus provides per-review pub/sub for streaming pipeline events.
//
// Two subscription modes:
//   - Subscribe(reviewID) — per-review SSE-style; one topic per review,
//     used by the live-stream UI. History retained 60s post-close.
//   - SubscribeGlobal(fn) — bus-level listener; fires for every Publish
//     regardless of reviewID. Used for in-process lifecycle hooks that
//     need to observe all reviews (e.g. async cross-PR stage watching
//     EventReviewCompleted). Callers MUST not block — handler runs
//     synchronously under t.mu; spawn a goroutine if work is non-trivial.
type EventBus struct {
	mu     sync.RWMutex
	topics map[uuid.UUID]*topic
	logger *slog.Logger

	// globalMu guards globalSubs. Separate from mu so global-listener
	// registration doesn't contend with topic open/close.
	globalMu   sync.RWMutex
	globalSubs []GlobalHandler
}

// GlobalHandler receives every published event. Must not block.
type GlobalHandler func(reviewID uuid.UUID, evt Event)

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

// Publish sends an event to all subscribers of a review topic AND to any
// registered global handlers. Non-blocking for per-review subscribers:
// drops events for slow clients. Global handlers MUST return promptly
// (see GlobalHandler docs) or they will serialize Publish throughput.
//
// When no topic is open for reviewID, per-review delivery is skipped but
// global handlers still fire — this lets in-process lifecycle hooks
// observe events (e.g. EventReviewCompleted) even after the UI topic has
// already been closed.
func (eb *EventBus) Publish(reviewID uuid.UUID, evtType EventType, data any) {
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

	// Per-review delivery (topic may not exist — e.g. CLI replay, or a
	// late-fire lifecycle event published after CloseTopic).
	eb.mu.RLock()
	t, ok := eb.topics[reviewID]
	eb.mu.RUnlock()
	if ok {
		t.mu.Lock()
		if !t.closed {
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
		t.mu.Unlock()
	}

	// Global delivery. Snapshot under RLock so a concurrent SubscribeGlobal
	// can't race with invocation. Handlers are invoked outside the lock.
	eb.globalMu.RLock()
	handlers := eb.globalSubs
	eb.globalMu.RUnlock()
	for _, h := range handlers {
		h(reviewID, evt)
	}
}

// SubscribeGlobal registers a bus-level handler invoked for every Publish.
// Intended for in-process lifecycle hooks (e.g. cross-review workflows);
// not part of the per-review SSE stream. Handlers run synchronously in the
// publisher's goroutine — spawn a goroutine inside the handler if work is
// non-trivial.
func (eb *EventBus) SubscribeGlobal(h GlobalHandler) {
	if h == nil {
		return
	}
	eb.globalMu.Lock()
	eb.globalSubs = append(eb.globalSubs, h)
	eb.globalMu.Unlock()
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
