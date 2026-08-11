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
	"github.com/jackc/pgx/v5/pgconn"
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

const (
	maxHistoryEvents          = 500
	eventCatchUpBatchSize     = 500
	durablePublishAttempts    = 3
	durablePublishTimeout     = 5 * time.Second
	eventRetention            = 7 * 24 * time.Hour
	eventPruneSafetyWindow    = time.Hour
	eventPruneInterval        = 6 * time.Hour
	eventPruneBatchSize       = 5000
	eventPruneAdvisoryLockKey = int64(0x415247555345564e) // "ARGUSEVN"
)

var durableRetryDelays = [...]time.Duration{10 * time.Millisecond, 25 * time.Millisecond}

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
	mu           sync.RWMutex
	topics       map[uuid.UUID]*topic
	logger       *slog.Logger
	pool         *pgxpool.Pool
	instance     uuid.UUID
	persistEvent durableEventPersister

	seenMu sync.Mutex
	seen   map[int64]struct{}

	// globalMu guards globalSubs. Separate from mu so global-listener
	// registration doesn't contend with topic open/close.
	globalMu   sync.RWMutex
	globalSubs []GlobalHandler
}

// GlobalHandler receives every published event. Must not block.
type GlobalHandler func(reviewID uuid.UUID, evt Event)

// durableEventPersister returns current=false when an attempt event lost
// authority before insertion. Errors describe storage failures, not stale work.
type durableEventPersister func(context.Context, uuid.UUID, *Event) (current bool, err error)

type topicSubscriber struct {
	ch            chan Event
	durableCursor int64
}

type topic struct {
	mu            sync.Mutex
	subscribers   map[uint64]*topicSubscriber
	history       []Event
	historyCursor int64
	closed        bool
	nextID        uint64
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
	eb.persistEvent = eb.persistToPostgres
	ready := make(chan struct{})
	go eb.listen(ctx, ready)
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		logger.Warn("eventbus: listener startup timed out")
	}
	go eb.pruneLoop(ctx)
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
	eb.topics[reviewID] = &topic{subscribers: make(map[uint64]*topicSubscriber)}
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
	for id, subscriber := range t.subscribers {
		close(subscriber.ch)
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
	if eb.persistEvent != nil {
		publishCtx, cancel := context.WithTimeout(context.Background(), durablePublishTimeout)
		defer cancel()
		current, persistAttempts, persistErr := eb.persistWithRetry(publishCtx, reviewID, &evt)
		if !current {
			return
		}
		if persistErr != nil {
			// Persistence is an availability enhancement, not the authority for
			// local lifecycle delivery. In particular, ReviewCompleted must still
			// reach the local cross-PR subscriber when PostgreSQL has a transient
			// outage. ID=0 tells clients this event cannot advance a replay cursor.
			eb.logger.Error("eventbus: durable publish failed; delivering locally",
				"type", evtType, "review_id", reviewID, "attempts", persistAttempts, "error", persistErr)
		}
	}
	eb.deliver(reviewID, evt, true)
}

func (eb *EventBus) persistWithRetry(ctx context.Context, reviewID uuid.UUID, evt *Event) (bool, int, error) {
	var lastErr error
	attempts := 0
	localTimestamp := evt.Timestamp
	for attempt := 0; attempt < durablePublishAttempts; attempt++ {
		attempts = attempt + 1
		current, err := eb.persistEvent(ctx, reviewID, evt)
		if err == nil {
			return current, attempt + 1, nil
		}
		lastErr = err
		// A failed Scan may have assigned a prefix of the returned columns. Never
		// expose a cursor unless the complete durable write result was observed.
		evt.ID = 0
		evt.Timestamp = localTimestamp
		if !shouldRetryDurablePublish(err) || attempt == durablePublishAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return true, attempt + 1, errors.Join(lastErr, ctx.Err())
		case <-time.After(durableRetryDelays[attempt]):
		}
	}
	return true, attempts, lastErr
}

func shouldRetryDurablePublish(err error) bool {
	if pgconn.SafeToRetry(err) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	// These server-side failures guarantee the statement did not commit.
	switch pgErr.Code {
	case "40001", "40P01", "53300", "57P03":
		return true
	default:
		return false
	}
}

func (eb *EventBus) persistToPostgres(ctx context.Context, reviewID uuid.UUID, evt *Event) (bool, error) {
	var notified string
	err := eb.pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO review_events (review_id, attempt_generation, event_type, data)
			SELECT $1,$2,$3,$4
			WHERE $2=0 OR EXISTS (SELECT 1 FROM reviews WHERE id=$1 AND attempt_generation=$2)
			RETURNING id, created_at
		)
		SELECT id, created_at, pg_notify('argus_review_events', $5 || ':' || id::text)
		FROM inserted`, reviewID, evt.AttemptGeneration, string(evt.Type), evt.Data, eb.instance.String()).Scan(&evt.ID, &evt.Timestamp, &notified)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, nil
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
			// Durable IDs are per-subscriber cursors: a second subscriber may
			// replay an event that the first still needs from reconnect catch-up.
			// ID=0 events exist only in memory and must never advance a cursor.
			if evt.ID == 0 || evt.ID > t.historyCursor {
				if len(t.history) < maxHistoryEvents {
					t.history = append(t.history, evt)
				}
				if evt.ID > 0 {
					t.historyCursor = evt.ID
				}
			}
			for id, subscriber := range t.subscribers {
				if evt.ID > 0 && evt.ID <= subscriber.durableCursor {
					continue
				}
				if evt.ID > 0 {
					subscriber.durableCursor = evt.ID
				}
				select {
				case subscriber.ch <- evt:
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

func (eb *EventBus) pruneLoop(ctx context.Context) {
	ticker := time.NewTicker(eventPruneInterval)
	defer ticker.Stop()
	for {
		eb.pruneUntilCaughtUp(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (eb *EventBus) pruneUntilCaughtUp(ctx context.Context) {
	for ctx.Err() == nil {
		deleted, err := eb.pruneReviewEvents(ctx, time.Now())
		if err != nil {
			eb.logger.Warn("eventbus: retention cleanup failed", "error", err)
			return
		}
		if deleted < eventPruneBatchSize {
			return
		}
	}
}

// pruneReviewEvents removes events outside both replay guarantees: every
// event younger than the safety window is retained for delayed LISTEN fetches;
// afterwards, the newest replay window per review is retained for reconnects,
// and the absolute retention deadline removes even low-volume histories.
// The transaction-scoped advisory lock makes concurrent app machines race-safe.
func (eb *EventBus) pruneReviewEvents(ctx context.Context, now time.Time) (int64, error) {
	var deleted int64
	err := eb.pool.QueryRow(ctx, `
		WITH cleanup_lock AS MATERIALIZED (
			SELECT pg_try_advisory_xact_lock($1) AS acquired
		), ranked AS MATERIALIZED (
			SELECT e.id, e.created_at,
			       row_number() OVER (PARTITION BY e.review_id ORDER BY e.id DESC) AS replay_position
			FROM review_events e
			CROSS JOIN cleanup_lock l
			WHERE l.acquired
		), victims AS (
			SELECT id
			FROM ranked
			WHERE created_at < $2
			  AND (created_at < $3 OR replay_position > $4)
			ORDER BY id
			LIMIT $5
		), removed AS (
			DELETE FROM review_events e
			USING victims v
			WHERE e.id=v.id
			RETURNING e.id
		)
		SELECT count(*) FROM removed`, eventPruneAdvisoryLockKey,
		now.Add(-eventPruneSafetyWindow), now.Add(-eventRetention), maxHistoryEvents, eventPruneBatchSize).Scan(&deleted)
	if err != nil {
		return 0, fmt.Errorf("pruning review events: %w", err)
	}
	return deleted, nil
}

func (eb *EventBus) listen(ctx context.Context, ready chan struct{}) {
	var readyOnce sync.Once
	var durableCursor int64
	cursorInitialized := false
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

		if !cursorInitialized {
			// LISTEN first, then establish the durable snapshot. PostgreSQL will
			// queue notifications committed after LISTEN while this query runs.
			if floor, subscribed := eb.activeSubscriptionCursorFloor(); subscribed {
				durableCursor, err = eb.catchUpStored(ctx, floor)
			} else {
				durableCursor, err = eb.reviewEventHighWater(ctx)
			}
			if err == nil {
				cursorInitialized = true
			}
		} else {
			// NOTIFY is lossy while disconnected. Fill the durable interval before
			// consuming notifications queued on the replacement connection.
			durableCursor, err = eb.catchUpStored(ctx, durableCursor)
		}
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
			if parseErr != nil || id <= 0 {
				eb.logger.Warn("eventbus: invalid notification", "payload", n.Payload)
				continue
			}
			var fetchErr error
			durableCursor, fetchErr = eb.processStoredNotification(ctx, durableCursor, id)
			if fetchErr != nil {
				// Reconnect even when LISTEN itself is healthy. This gives both the
				// point fetch and its catch-up query a fresh retry opportunity.
				err = fetchErr
				eb.logger.Error("eventbus: notification recovery failed", "event_id", id, "error", fetchErr)
				break
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

func (eb *EventBus) reviewEventHighWater(ctx context.Context) (int64, error) {
	var id int64
	if err := eb.pool.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM review_events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("loading review event high-water: %w", err)
	}
	return id, nil
}

// activeReviewIDs returns only reviews with an existing live subscription.
// Catch-up must not turn one listener reconnect into a scan or delivery of
// another tenant's unrelated review history.
func (eb *EventBus) activeReviewIDs() []uuid.UUID {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	ids := make([]uuid.UUID, 0, len(eb.topics))
	for reviewID, t := range eb.topics {
		t.mu.Lock()
		active := !t.closed && len(t.subscribers) > 0
		t.mu.Unlock()
		if active {
			ids = append(ids, reviewID)
		}
	}
	return ids
}

// activeSubscriptionCursorFloor is used only for the listener's first durable
// snapshot. It avoids scanning pre-subscription history if construction timed
// out and a subscriber was installed before LISTEN became ready.
func (eb *EventBus) activeSubscriptionCursorFloor() (int64, bool) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	var floor int64
	found := false
	for _, t := range eb.topics {
		t.mu.Lock()
		if !t.closed {
			for _, subscriber := range t.subscribers {
				cursor := subscriber.durableCursor
				if cursor < 0 {
					cursor = 0
				}
				if !found || cursor < floor {
					floor = cursor
					found = true
				}
			}
		}
		t.mu.Unlock()
	}
	return floor, found
}

// catchUpStored fills one lost-NOTIFY interval for currently subscribed
// reviews. Each query and result set is bounded; cursor is returned after every
// successfully consumed page so a later reconnect resumes rather than rescans.
func (eb *EventBus) catchUpStored(ctx context.Context, afterID int64) (int64, error) {
	upper, err := eb.reviewEventHighWater(ctx)
	if err != nil {
		return afterID, err
	}
	if upper <= afterID {
		return afterID, nil
	}
	reviewIDs := eb.activeReviewIDs()
	if len(reviewIDs) == 0 {
		return upper, nil
	}

	cursor := afterID
	for cursor < upper {
		rows, queryErr := eb.pool.Query(ctx, `
			SELECT review_id, id, attempt_generation, event_type, created_at, data
			FROM (`+authorizedReviewEventsSQL+`) authorized
			WHERE id>$1 AND id<=$2 AND review_id=ANY($3::uuid[])
			ORDER BY id
			LIMIT $4`, cursor, upper, reviewIDs, eventCatchUpBatchSize)
		if queryErr != nil {
			return cursor, fmt.Errorf("querying review event catch-up after %d: %w", cursor, queryErr)
		}

		count := 0
		for rows.Next() {
			var reviewID uuid.UUID
			var evt Event
			if scanErr := rows.Scan(&reviewID, &evt.ID, &evt.AttemptGeneration, &evt.Type, &evt.Timestamp, &evt.Data); scanErr != nil {
				rows.Close()
				return cursor, fmt.Errorf("scanning review event catch-up: %w", scanErr)
			}
			eb.deliver(reviewID, evt, false)
			cursor = evt.ID
			count++
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return cursor, fmt.Errorf("reading review event catch-up: %w", rowsErr)
		}
		rows.Close()
		if count < eventCatchUpBatchSize {
			return upper, nil
		}
	}
	return upper, nil
}

func (eb *EventBus) processStoredNotification(ctx context.Context, cursor, id int64) (int64, error) {
	return recoverStoredNotification(ctx, cursor, id, eb.deliverStored, eb.catchUpStored)
}

func recoverStoredNotification(
	ctx context.Context,
	cursor, id int64,
	deliverOne func(context.Context, int64) error,
	catchUp func(context.Context, int64) (int64, error),
) (int64, error) {
	deliverErr := deliverOne(ctx, id)
	if deliverErr == nil {
		if id > cursor {
			return id, nil
		}
		return cursor, nil
	}

	// A failed point fetch must remain behind the high-water even when the
	// notification arrived late. Replaying overlap is safe because each
	// subscriber suppresses durable IDs it already observed.
	retryFrom := cursor
	if id <= retryFrom {
		retryFrom = id - 1
	}
	caughtUp, catchErr := catchUp(ctx, retryFrom)
	if catchErr != nil {
		return retryFrom, errors.Join(deliverErr, catchErr)
	}
	if caughtUp < cursor {
		return cursor, nil
	}
	return caughtUp, nil
}

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
	durableCursor := afterID
	if len(history) > 0 && history[len(history)-1].ID > durableCursor {
		durableCursor = history[len(history)-1].ID
	}
	t.subscribers[id] = &topicSubscriber{ch: ch, durableCursor: durableCursor}
	unsub := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if subscriber, exists := t.subscribers[id]; exists {
			delete(t.subscribers, id)
			close(subscriber.ch)
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
	t.subscribers[id] = &topicSubscriber{ch: ch}
	history := make([]Event, len(t.history))
	copy(history, t.history)
	t.mu.Unlock()

	unsub := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if subscriber, exists := t.subscribers[id]; exists {
			delete(t.subscribers, id)
			close(subscriber.ch)
		}
	}

	return ch, history, unsub
}
