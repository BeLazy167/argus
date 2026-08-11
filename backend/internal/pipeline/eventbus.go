package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	EventError          EventType = "error"
	// EventBudgetReduced reports that the Budget narrowed this review, so the
	// live view can say why fewer files appear than the pull request changed.
	EventBudgetReduced     EventType = "budget_reduced"
	EventCancelled         EventType = "cancelled"
	EventFileReviewStarted EventType = "file_review_started"
	EventTokenUpdate       EventType = "token_update"

	// Per-sub-step events — each distinct LLM call, memory upsert, or GitHub API
	// action that previously fired no event. EventMemoryIndexed payload carries
	// a "kind" field (patterns | conventions | file_synthesis | pr_summary |
	// arch_summary | arch_graph | patterns_praise) so one type covers all
	// memory upserts.
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
	// pattern / convention / rule / similarity hit. Payload
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
	ID                int64           `json:"id,omitempty"`
	AttemptGeneration int             `json:"attempt_generation,omitempty"`
	Type              EventType       `json:"type"`
	Timestamp         time.Time       `json:"timestamp"`
	Data              json.RawMessage `json:"data"`
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
	mu       sync.RWMutex
	topics   map[uuid.UUID]*topic
	logger   *slog.Logger
	pool     *pgxpool.Pool
	instance uuid.UUID

	seenMu sync.Mutex
	seen   map[int64]struct{}

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
		topics:   make(map[uuid.UUID]*topic),
		logger:   slog.Default(),
		instance: uuid.New(),
		seen:     make(map[int64]struct{}),
	}
}

// NewDurableEventBus persists events and relays PostgreSQL notifications from
// every application machine into this process's local subscribers.
func NewDurableEventBus(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) *EventBus {
	eb := NewEventBus()
	eb.pool = pool
	eb.logger = logger
	ready := make(chan struct{})
	go eb.listen(ctx, ready)
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		logger.Warn("eventbus: listener startup timed out")
	}
	return eb
}

// OpenTopic creates a topic for a review. Safe to call multiple times.
func (eb *EventBus) OpenTopic(reviewID uuid.UUID) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if existing, ok := eb.topics[reviewID]; ok {
		existing.mu.Lock()
		closed := existing.closed
		existing.mu.Unlock()
		if !closed {
			return
		}
	}
	eb.topics[reviewID] = &topic{subscribers: make(map[uint64]chan Event)}
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
		if eb.topics[reviewID] == t {
			delete(eb.topics, reviewID)
		}
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
	eb.publish(reviewID, 0, evtType, data)
}

// PublishForAttempt drops the event when generation is no longer current.
func (eb *EventBus) PublishForAttempt(reviewID uuid.UUID, generation int, evtType EventType, data any) {
	eb.publish(reviewID, generation, evtType, data)
}

func (eb *EventBus) publish(reviewID uuid.UUID, generation int, evtType EventType, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		eb.logger.Error("eventbus: marshal failed", "type", evtType, "review_id", reviewID, "error", err)
		return
	}

	evt := Event{AttemptGeneration: generation, Type: evtType, Timestamp: time.Now(), Data: raw}
	if eb.pool != nil {
		var notified string
		publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = eb.pool.QueryRow(publishCtx, `
			WITH inserted AS (
				INSERT INTO review_events (review_id, attempt_generation, event_type, data)
				SELECT $1,$2,$3,$4
				WHERE $2=0 OR EXISTS (SELECT 1 FROM reviews WHERE id=$1 AND attempt_generation=$2)
				RETURNING id, created_at
			)
			SELECT id, created_at, pg_notify('argus_review_events', $5 || ':' || id::text)
			FROM inserted`, reviewID, generation, string(evtType), raw, eb.instance.String()).Scan(&evt.ID, &evt.Timestamp, &notified)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				eb.logger.Error("eventbus: durable publish failed", "type", evtType, "review_id", reviewID, "error", err)
			}
			if generation > 0 {
				return
			}
		}
	}
	eb.deliver(reviewID, evt, true)
}

func (eb *EventBus) deliver(reviewID uuid.UUID, evt Event, notifyGlobal bool) {
	if evt.ID > 0 {
		eb.seenMu.Lock()
		if _, duplicate := eb.seen[evt.ID]; duplicate {
			eb.seenMu.Unlock()
			return
		}
		eb.seen[evt.ID] = struct{}{}
		if len(eb.seen) > maxHistoryEvents*4 {
			eb.seen = map[int64]struct{}{evt.ID: {}}
		}
		eb.seenMu.Unlock()
	}

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
					eb.logger.Warn("eventbus: dropped event for slow client", "subscriber", id, "type", evt.Type, "review_id", reviewID)
				}
			}
		}
		t.mu.Unlock()
	}
	if !notifyGlobal {
		return
	}
	eb.globalMu.RLock()
	handlers := append([]GlobalHandler(nil), eb.globalSubs...)
	eb.globalMu.RUnlock()
	for _, h := range handlers {
		h(reviewID, evt)
	}
}

func (eb *EventBus) listen(ctx context.Context, ready chan struct{}) {
	var readyOnce sync.Once
	for ctx.Err() == nil {
		conn, err := eb.pool.Acquire(ctx)
		if err != nil {
			eb.waitToReconnect(ctx, err)
			continue
		}
		_, err = conn.Exec(ctx, `LISTEN argus_review_events`)
		if err != nil {
			conn.Release()
			eb.waitToReconnect(ctx, err)
			continue
		}
		readyOnce.Do(func() { close(ready) })
		for ctx.Err() == nil {
			n, waitErr := conn.Conn().WaitForNotification(ctx)
			if waitErr != nil {
				err = waitErr
				break
			}
			parts := strings.SplitN(n.Payload, ":", 2)
			if len(parts) != 2 || parts[0] == eb.instance.String() {
				continue
			}
			id, parseErr := strconv.ParseInt(parts[1], 10, 64)
			if parseErr != nil {
				eb.logger.Warn("eventbus: invalid notification", "payload", n.Payload)
				continue
			}
			if fetchErr := eb.deliverStored(ctx, id); fetchErr != nil {
				eb.logger.Error("eventbus: notification fetch failed", "event_id", id, "error", fetchErr)
			}
		}
		conn.Release()
		if ctx.Err() == nil {
			eb.waitToReconnect(ctx, err)
		}
	}
}

func (eb *EventBus) waitToReconnect(ctx context.Context, err error) {
	eb.logger.Warn("eventbus: listener disconnected", "error", err)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

// authorizedReviewEventsSQL is the single authority relation for every
// durable-event read. Generation-zero events are review-wide; attempt-scoped
// events are visible only while their attempt is current.
const authorizedReviewEventsSQL = `
	SELECT e.review_id, e.id, COALESCE(e.attempt_generation,0) AS attempt_generation,
	       e.event_type, e.created_at, e.data
	FROM review_events e
	JOIN reviews r ON r.id=e.review_id
	WHERE e.attempt_generation=0 OR e.attempt_generation=r.attempt_generation`

func (eb *EventBus) deliverStored(ctx context.Context, id int64) error {
	var reviewID uuid.UUID
	var evt Event
	err := eb.pool.QueryRow(ctx, `
		SELECT review_id, id, attempt_generation, event_type, created_at, data
		FROM (`+authorizedReviewEventsSQL+`) authorized
		WHERE id=$1`, id).Scan(&reviewID, &evt.ID, &evt.AttemptGeneration, &evt.Type, &evt.Timestamp, &evt.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		// A notification can race deletion or BeginReviewRetry. Both mean the
		// referenced event has no authority now, not that the listener failed.
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading review event %d: %w", id, err)
	}
	eb.deliver(reviewID, evt, false)
	return nil
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

// SubscribeContext replays durable events newer than afterID and then streams
// live notifications without a replay/subscribe gap.
func (eb *EventBus) SubscribeContext(ctx context.Context, reviewID uuid.UUID, afterID int64) (<-chan Event, []Event, func(), error) {
	if eb.pool == nil {
		ch, history, unsub := eb.Subscribe(reviewID)
		return ch, history, unsub, nil
	}
	eb.OpenTopic(reviewID)
	eb.mu.RLock()
	t := eb.topics[reviewID]
	eb.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := eb.pool.Query(ctx, `
		SELECT id, attempt_generation, event_type, created_at, data FROM (
			SELECT id, attempt_generation, event_type, created_at, data
			FROM (`+authorizedReviewEventsSQL+`) authorized
			WHERE review_id=$1 AND id>$2
			ORDER BY id DESC LIMIT $3
		) replay ORDER BY id`, reviewID, afterID, maxHistoryEvents)
	if err != nil {
		return nil, nil, func() {}, fmt.Errorf("querying event replay: %w", err)
	}
	history := make([]Event, 0)
	for rows.Next() {
		var evt Event
		if err := rows.Scan(&evt.ID, &evt.AttemptGeneration, &evt.Type, &evt.Timestamp, &evt.Data); err != nil {
			rows.Close()
			return nil, nil, func() {}, err
		}
		history = append(history, evt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, func() {}, err
	}
	rows.Close()

	ch := make(chan Event, 64)
	if t.closed {
		close(ch)
		return ch, history, func() {}, nil
	}
	t.nextID++
	id := t.nextID
	t.subscribers[id] = ch
	unsub := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, exists := t.subscribers[id]; exists {
			delete(t.subscribers, id)
			close(ch)
		}
	}
	return ch, history, unsub, nil
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
