package obs

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/posthog/posthog-go"
)

// defaultBufferSize caps memory backpressure — at worst we hold this many
// enqueued records while the drain goroutine catches up before starting to
// drop. 4096 records * ~1KB each ≈ 4MB, acceptable for the API binary.
const defaultBufferSize = 4096

// posthogEnqueuer is the narrow slice of posthog.Client the handler depends on.
// Keeping it narrow lets tests supply a fake without stubbing the full flag API.
type posthogEnqueuer interface {
	Enqueue(posthog.Message) error
}

// Handler is a slog.Handler that forwards selected records to PostHog while
// still delegating formatting and writing to an inner handler (kept intact
// so stdout/stderr logging is unaffected).
//
// Forwarding rules (in order):
//   - record has an `event=` attr → capture as a PostHog event named by that attr.
//   - no `event=` but level is Warn → capture as `log.warn`.
//   - no `event=` but level is Error → capture as `log.error`.
//   - otherwise (Debug / Info without event) → not forwarded.
type Handler struct {
	inner  slog.Handler
	client posthogEnqueuer

	// groupAttrs carries attrs added via With/WithGroup so subsequent Handle
	// calls can include them in filtered props. Copy-on-write per With call.
	groupAttrs []slog.Attr

	// drain plumbing — shared across clones produced by With/WithGroup.
	shared *handlerShared
}

type handlerShared struct {
	buf       chan pendingEvent
	wg        sync.WaitGroup
	cb        *CircuitBreaker
	closeOnce sync.Once
	stopCh    chan struct{}
	// closed gates Handle after Close so late slog records during shutdown
	// don't silently fill the orphaned buffer.
	closed atomic.Bool

	sent                atomic.Int64
	droppedBuffer       atomic.Int64
	droppedBreaker      atomic.Int64
	droppedEnqueue      atomic.Int64
	droppedUnattributed atomic.Int64
}

// pendingEvent is the internal struct queued to the drain goroutine. Packaging
// the whole posthog.Capture here means the drain goroutine is a trivial
// pump that cannot race on mutating fields.
type pendingEvent struct {
	msg posthog.Capture
}

// NewPostHogHandler wraps inner with PostHog forwarding. Spawns a single
// drain goroutine that calls client.Enqueue; shut down via Close. A nil
// client is treated as a no-op forwarder — callers that want the kill-switch
// behaviour pass nil when POSTHOG_API_KEY is unset.
func NewPostHogHandler(inner slog.Handler, client posthog.Client) *Handler {
	// posthog.Client satisfies posthogEnqueuer; the narrower interface exists
	// only to keep tests tractable (avoid mocking the 8-method flag API).
	return newHandler(inner, client)
}

func newHandler(inner slog.Handler, client posthogEnqueuer) *Handler {
	h := &Handler{
		inner:  inner,
		client: client,
		shared: &handlerShared{
			buf:    make(chan pendingEvent, defaultBufferSize),
			cb:     NewCircuitBreaker(),
			stopCh: make(chan struct{}),
		},
	}
	h.shared.wg.Add(1)
	go h.drain()
	return h
}

// Enabled delegates entirely to the inner handler so log levels remain
// whatever the app configures.
func (h *Handler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle forwards to the inner handler unconditionally, then — if the record
// qualifies per the 3-tier rule — enqueues a PostHog capture. Enqueue is
// non-blocking: if the internal buffer is full we drop and increment a
// counter. An inner handler error is returned; forwarding failures are
// silent by design (they must never break application logging).
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	// Always write to the inner handler — keep text logs intact.
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	if h.client == nil || h.shared.closed.Load() {
		return nil
	}
	event, ok := h.resolveEvent(r)
	if !ok {
		return nil
	}
	props := h.buildProps(ctx, r)
	distinctID, repo, ok := h.resolveDistinctID(ctx, props)
	if !ok {
		h.shared.droppedUnattributed.Add(1)
		return nil
	}
	// "event" is the promoter key, not a payload property — strip before send.
	delete(props, "event")

	msg := posthog.Capture{
		DistinctId: distinctID,
		Event:      event,
		Properties: posthog.Properties(props),
		Groups:     Groups(ctx, repo),
	}
	select {
	case h.shared.buf <- pendingEvent{msg: msg}:
	default:
		h.shared.droppedBuffer.Add(1)
	}
	return nil
}

// WithAttrs returns a clone bound to extra attrs. The clone shares the drain
// goroutine and counters with the original — there is exactly one drain per
// root handler regardless of how many child loggers fan out from it.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	dup := *h
	dup.inner = h.inner.WithAttrs(attrs)
	dup.groupAttrs = append(append([]slog.Attr{}, h.groupAttrs...), attrs...)
	return &dup
}

// WithGroup returns a clone with the given group opened on the inner handler.
// PostHog forwarding ignores nested groups (see allowlist.go collectAttr).
func (h *Handler) WithGroup(name string) slog.Handler {
	dup := *h
	dup.inner = h.inner.WithGroup(name)
	return &dup
}

// Close stops the drain goroutine and returns once all buffered events have
// been enqueued (which hands them to posthog-go's own flush queue — callers
// must still call posthog.Client.Close() afterward for the wire flush).
func (h *Handler) Close() error {
	h.shared.closeOnce.Do(func() {
		h.shared.closed.Store(true)
		close(h.shared.stopCh)
	})
	h.shared.wg.Wait()
	return nil
}

// Sent returns the count of events successfully handed to posthog.Client.Enqueue.
func (h *Handler) Sent() int64 { return h.shared.sent.Load() }

// DroppedBuffer returns the count of events dropped because the internal
// channel was full (drain goroutine or downstream is slow).
func (h *Handler) DroppedBuffer() int64 { return h.shared.droppedBuffer.Load() }

// DroppedBreaker returns the count of events dropped because the circuit
// breaker was open (sustained PostHog failures).
func (h *Handler) DroppedBreaker() int64 { return h.shared.droppedBreaker.Load() }

// DroppedEnqueue returns the count of events where posthog-go's Enqueue
// returned an error or the drain goroutine recovered from a panic.
func (h *Handler) DroppedEnqueue() int64 { return h.shared.droppedEnqueue.Load() }

// DroppedUnattributed returns the count of events dropped because the
// DistinctId fallback chain resolved to empty.
func (h *Handler) DroppedUnattributed() int64 { return h.shared.droppedUnattributed.Load() }

// BreakerOpen reports whether the PostHog circuit breaker is currently open.
func (h *Handler) BreakerOpen() bool { return h.shared.cb.IsOpen() }

// resolveEvent applies the 3-tier rule and returns the PostHog event name.
func (h *Handler) resolveEvent(r slog.Record) (string, bool) {
	var name string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "event" && a.Value.Kind() == slog.KindString {
			name = a.Value.String()
			return false
		}
		return true
	})
	if name != "" {
		return name, true
	}
	// Fall back to level-based naming for warn/error — everything else skips.
	switch r.Level {
	case slog.LevelWarn:
		return "log.warn", true
	case slog.LevelError:
		return "log.error", true
	default:
		return "", false
	}
}

// buildProps combines record attrs, WithAttrs-added attrs, and ctx-derived
// identity fields into a single filtered property bag.
func (h *Handler) buildProps(ctx context.Context, r slog.Record) map[string]any {
	props := FilterAttrs(r)
	// WithAttrs-added attrs go through the same filter.
	for _, a := range h.groupAttrs {
		collectAttr(a, props)
	}
	// Ctx-derived fields — only set if absent in attrs so explicit values win.
	if _, ok := props["trace_id"]; !ok {
		if id := TraceID(ctx); id != "" {
			props["trace_id"] = id
		}
	}
	if _, ok := props["installation_id"]; !ok {
		if id := InstallationID(ctx); id != 0 {
			props["installation_id"] = id
		}
	}
	if _, ok := props["github_login"]; !ok {
		if login := GithubLogin(ctx); login != "" {
			props["github_login"] = login
		}
	}
	return props
}

// resolveDistinctID runs the fallback chain and returns (id, repo, ok).
// Order matters: per-user ids first, installation (coarse-grained) last.
func (h *Handler) resolveDistinctID(ctx context.Context, props map[string]any) (string, string, bool) {
	repo, _ := props["repo"].(string)
	if user := ClerkUser(ctx); user != "" {
		return user, repo, true
	}
	if login := GithubLogin(ctx); login != "" {
		return "github:" + login, repo, true
	}
	if v, ok := props["github_login"].(string); ok && v != "" {
		return "github:" + v, repo, true
	}
	if id := InstallationID(ctx); id != 0 {
		return "installation:" + strconv.FormatInt(id, 10), repo, true
	}
	if v, ok := props["installation_id"]; ok {
		switch id := v.(type) {
		case int64:
			if id != 0 {
				return "installation:" + strconv.FormatInt(id, 10), repo, true
			}
		case int:
			if id != 0 {
				return "installation:" + strconv.Itoa(id), repo, true
			}
		}
	}
	return "", repo, false
}

func (h *Handler) drain() {
	defer h.shared.wg.Done()
	// Recover from panics in forward() or posthog-go serialization so a single
	// malformed event cannot kill the drain goroutine and silently disable all
	// telemetry for the remainder of the process.
	defer func() {
		if r := recover(); r != nil {
			// Route via the inner handler directly to avoid recursion through
			// our own forwarder on shutdown.
			h.shared.droppedEnqueue.Add(1)
		}
	}()
	for {
		select {
		case evt := <-h.shared.buf:
			h.forward(evt.msg)
		case <-h.shared.stopCh:
			for {
				select {
				case evt := <-h.shared.buf:
					h.forward(evt.msg)
				default:
					return
				}
			}
		}
	}
}

func (h *Handler) forward(msg posthog.Capture) {
	if !h.shared.cb.AllowRequest() {
		h.shared.droppedBreaker.Add(1)
		return
	}
	if err := h.client.Enqueue(msg); err != nil {
		h.shared.cb.RecordFailure()
		h.shared.droppedEnqueue.Add(1)
		return
	}
	h.shared.cb.RecordSuccess()
	h.shared.sent.Add(1)
}
