package obs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/posthog/posthog-go"
)

// fakeClient captures Enqueue calls for unit assertions. Optionally returns an
// error so we can exercise the circuit breaker path without network.
type fakeClient struct {
	mu      sync.Mutex
	calls   []posthog.Capture
	err     error
	blockCh chan struct{}
}

func (f *fakeClient) Enqueue(m posthog.Message) error {
	if f.blockCh != nil {
		<-f.blockCh
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if c, ok := m.(posthog.Capture); ok {
		f.calls = append(f.calls, c)
	}
	return nil
}

// getCalls returns a copy so test assertions don't race with the drain goroutine.
func (f *fakeClient) getCalls() []posthog.Capture {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]posthog.Capture, len(f.calls))
	copy(out, f.calls)
	return out
}

// waitForCalls polls for the expected call count. Necessary because the drain
// goroutine is async; without this we race the test assertion.
func waitForCalls(t *testing.T, f *fakeClient, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.getCalls()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d calls; got %d", want, len(f.getCalls()))
}

func newTestHandler(t *testing.T, client *fakeClient) *Handler {
	t.Helper()
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := newHandler(inner, client)
	t.Cleanup(func() {
		_ = h.Close()
	})
	return h
}

func TestHandler_EventAttrPromotesName(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_123")

	logger := slog.New(h)
	logger.InfoContext(ctx, "starting review",
		slog.String("event", "review.started"),
		slog.String("review_id", "rev_abc"),
	)
	waitForCalls(t, fc, 1)
	calls := fc.getCalls()
	if calls[0].Event != "review.started" {
		t.Fatalf("want event review.started got %q", calls[0].Event)
	}
	if calls[0].DistinctId != "user_123" {
		t.Fatalf("want distinct_id user_123 got %q", calls[0].DistinctId)
	}
	if _, ok := calls[0].Properties["event"]; ok {
		t.Fatalf("event key must be stripped from properties")
	}
	if calls[0].Properties["review_id"] != "rev_abc" {
		t.Fatalf("want review_id propagated")
	}
}

func TestHandler_WarnWithoutEventPromotesToLogWarn(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).WarnContext(ctx, "db keepalive ping failed", slog.String("stage", "triage"))
	waitForCalls(t, fc, 1)
	if got := fc.getCalls()[0].Event; got != "log.warn" {
		t.Fatalf("want log.warn got %q", got)
	}
}

func TestHandler_ErrorWithoutEventPromotesToLogError(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).ErrorContext(ctx, "pipeline crash", slog.String("stage", "triage"))
	waitForCalls(t, fc, 1)
	if got := fc.getCalls()[0].Event; got != "log.error" {
		t.Fatalf("want log.error got %q", got)
	}
}

func TestHandler_InfoWithoutEventIsNotForwarded(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).InfoContext(ctx, "informational", slog.String("stage", "triage"))
	// Give the drain goroutine a chance to act.
	time.Sleep(50 * time.Millisecond)
	if len(fc.getCalls()) != 0 {
		t.Fatalf("info without event must not forward; got %d calls", len(fc.getCalls()))
	}
}

func TestHandler_UnlistedAttrsDropped(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).InfoContext(ctx, "ev",
		slog.String("event", "review.completed"),
		slog.String("unlisted_key", "leak"),
		slog.String("review_id", "rev_1"),
	)
	waitForCalls(t, fc, 1)
	props := fc.getCalls()[0].Properties
	if _, ok := props["unlisted_key"]; ok {
		t.Fatalf("unlisted_key must be dropped by filter")
	}
	if props["review_id"] != "rev_1" {
		t.Fatalf("whitelisted key must survive filter")
	}
}

// TestHandler_DenyKeysDroppedEvenIfListed mutates the package globals and
// therefore cannot run in parallel with other tests that read them.
func TestHandler_DenyKeysDroppedEvenIfListed(t *testing.T) {
	// "token" is already in DenyKeys. Temporarily add it to AllowedKeys to
	// prove deny wins precedence. Restored in t.Cleanup.
	AllowedKeys["token"] = struct{}{}
	t.Cleanup(func() { delete(AllowedKeys, "token") })

	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).InfoContext(ctx, "ev",
		slog.String("event", "x.y"),
		slog.String("token", "should_not_leak"),
	)
	waitForCalls(t, fc, 1)
	if _, ok := fc.getCalls()[0].Properties["token"]; ok {
		t.Fatalf("denied key must be dropped even when also allowed")
	}
}

func TestHandler_MsgFieldNeverForwarded(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	slog.New(h).WarnContext(ctx, "secret message that must not leak")
	waitForCalls(t, fc, 1)
	props := fc.getCalls()[0].Properties
	if _, ok := props["msg"]; ok {
		t.Fatalf("msg must never be forwarded")
	}
	// The text representation of the message body must not appear anywhere.
	for k, v := range props {
		if s, ok := v.(string); ok && s == "secret message that must not leak" {
			t.Fatalf("msg body leaked via key %q", k)
		}
	}
}

func TestHandler_DistinctIDFallbackChain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ctx  func() context.Context
		want string
	}{
		{
			name: "clerk user wins",
			ctx: func() context.Context {
				ctx := context.Background()
				ctx = SetClerkUser(ctx, "user_abc")
				ctx = SetGithubLogin(ctx, "octocat")
				ctx = SetInstallationID(ctx, 42)
				return ctx
			},
			want: "user_abc",
		},
		{
			name: "github login next",
			ctx: func() context.Context {
				ctx := SetGithubLogin(context.Background(), "octocat")
				return SetInstallationID(ctx, 42)
			},
			want: "github:octocat",
		},
		{
			name: "installation last",
			ctx: func() context.Context {
				return SetInstallationID(context.Background(), 42)
			},
			want: "installation:42",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fc := &fakeClient{}
			h := newTestHandler(t, fc)
			slog.New(h).InfoContext(tt.ctx(), "ev", slog.String("event", "x"))
			waitForCalls(t, fc, 1)
			if got := fc.getCalls()[0].DistinctId; got != tt.want {
				t.Fatalf("want %q got %q", tt.want, got)
			}
		})
	}
}

func TestHandler_UnattributedDropsAndCounts(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	// Blank ctx — no ids anywhere.
	slog.New(h).InfoContext(context.Background(), "ev", slog.String("event", "x"))
	time.Sleep(50 * time.Millisecond)
	if h.DroppedUnattributed() != 1 {
		t.Fatalf("expected 1 unattributed drop, got %d", h.DroppedUnattributed())
	}
	if len(fc.getCalls()) != 0 {
		t.Fatalf("unattributed record must not reach client")
	}
}

func TestHandler_GroupsBuiltFromCtxAndAttrs(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")
	ctx = SetInstallationID(ctx, 777)

	slog.New(h).InfoContext(ctx, "ev",
		slog.String("event", "review.completed"),
		slog.String("repo", "octo/hello"),
	)
	waitForCalls(t, fc, 1)
	g := fc.getCalls()[0].Groups
	if g == nil {
		t.Fatalf("expected Groups populated")
	}
	if g["installation"] != "777" {
		t.Fatalf("want installation=777 got %v", g["installation"])
	}
	if g["repo"] != "octo/hello" {
		t.Fatalf("want repo in groups got %v", g["repo"])
	}
}

func TestHandler_BufferFullIncrementsDroppedCounter(t *testing.T) {
	t.Parallel()
	// Block the drain by never unblocking client.Enqueue, then overflow a
	// tiny hand-rolled handler with a size-1 buffer.
	fc := &fakeClient{blockCh: make(chan struct{})}
	defer close(fc.blockCh) // unblock on test exit to let drain return

	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := &Handler{
		inner:  inner,
		client: fc,
		shared: &handlerShared{
			buf:    make(chan pendingEvent, 1),
			cb:     NewCircuitBreaker(),
			stopCh: make(chan struct{}),
		},
	}
	h.shared.wg.Add(1)
	go h.drain()
	t.Cleanup(func() { _ = h.Close() })

	ctx := SetClerkUser(context.Background(), "user_1")
	logger := slog.New(h)
	// First call: consumed by drain and blocked on Enqueue.
	logger.InfoContext(ctx, "ev", slog.String("event", "a"))
	// Wait until the drain goroutine has actually pulled the first event.
	time.Sleep(20 * time.Millisecond)
	// Fill buffer + overflow.
	logger.InfoContext(ctx, "ev", slog.String("event", "b"))
	logger.InfoContext(ctx, "ev", slog.String("event", "c")) // overflow
	logger.InfoContext(ctx, "ev", slog.String("event", "d")) // overflow

	// Drop counter bumps as soon as select-default fires. Give the goroutines
	// a chance to settle but not long enough to allow drain to drain the queue
	// (fc is blocked).
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && h.DroppedBuffer() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if h.DroppedBuffer() < 2 {
		t.Fatalf("expected at least 2 buffer drops, got %d", h.DroppedBuffer())
	}
}

func TestHandler_BreakerOpensAfterRepeatedFailures(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{err: errors.New("boom")}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")
	logger := slog.New(h)

	for i := 0; i < 20; i++ {
		logger.InfoContext(ctx, "ev", slog.String("event", "x"))
	}
	// Wait for drain to process enough failures to trip the breaker.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !h.BreakerOpen() {
		time.Sleep(10 * time.Millisecond)
	}
	if !h.BreakerOpen() {
		t.Fatalf("expected breaker open after repeated failures")
	}
}

func TestHandler_WithAttrsPropagates(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{}
	h := newTestHandler(t, fc)
	ctx := SetClerkUser(context.Background(), "user_1")

	logger := slog.New(h).With(slog.String("review_id", "rev_xyz"))
	logger.InfoContext(ctx, "ev", slog.String("event", "review.completed"))
	waitForCalls(t, fc, 1)
	if got := fc.getCalls()[0].Properties["review_id"]; got != "rev_xyz" {
		t.Fatalf("With-added attr missing; got %v", got)
	}
}
