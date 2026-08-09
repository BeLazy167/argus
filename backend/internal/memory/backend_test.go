package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFlagReader serves a canned feature_flags blob and counts reads so cache
// behaviour is observable without a clock.
type fakeFlagReader struct {
	raw   string
	err   error
	calls int32
}

func (f *fakeFlagReader) GetInstallationFeatureFlags(context.Context, int64) (json.RawMessage, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.raw), nil
}

// TestBackendSelectionFailsSafe is the load-bearing test of this change. During
// the migration Supermemory is read-authoritative and Postgres is empty or
// partially backfilled, so EVERY ambiguous input must resolve to Supermemory.
// The failure is silent in production — an empty memory read is
// indistinguishable from "nothing relevant found", so a wrong default here
// shows up as quietly worse reviews, not as an error.
func TestBackendSelectionFailsSafe(t *testing.T) {
	cases := map[string]struct {
		raw  string
		err  error
		want Backend
	}{
		"explicit postgres":  {raw: `{"memory_backend":"postgres"}`, want: BackendPostgres},
		"explicit sm":        {raw: `{"memory_backend":"supermemory"}`, want: BackendSupermemory},
		"absent key":         {raw: `{"cross_pr_checks":true}`, want: BackendSupermemory},
		"empty object":       {raw: `{}`, want: BackendSupermemory},
		"empty blob":         {raw: ``, want: BackendSupermemory},
		"null":               {raw: `null`, want: BackendSupermemory},
		"malformed json":     {raw: `{"memory_backend":`, want: BackendSupermemory},
		"unknown value":      {raw: `{"memory_backend":"pgvector"}`, want: BackendSupermemory},
		"wrong case":         {raw: `{"memory_backend":"Postgres"}`, want: BackendSupermemory},
		"whitespace padded":  {raw: `{"memory_backend":" postgres "}`, want: BackendSupermemory},
		"wrong type":         {raw: `{"memory_backend":true}`, want: BackendSupermemory},
		"db error":           {err: fmt.Errorf("connection refused"), want: BackendSupermemory},
		"empty string value": {raw: `{"memory_backend":""}`, want: BackendSupermemory},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBackendCache(&fakeFlagReader{raw: c.raw, err: c.err}, nil)
			if got := b.get(context.Background(), 42); got != c.want {
				t.Errorf("backend = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBackendSelectionWithoutReader: a Registry built without
// WithPostgresBackend has a nil cache. Selection must answer Supermemory
// rather than panic — this is the shape every non-wired caller has, including
// the two cmd/ binaries that construct their own Registry.
func TestBackendSelectionWithoutReader(t *testing.T) {
	var nilCache *backendCache
	if got := nilCache.get(context.Background(), 42); got != BackendSupermemory {
		t.Errorf("nil cache backend = %q, want %q", got, BackendSupermemory)
	}
	nilCache.invalidate(42) // must not panic

	empty := newBackendCache(nil, nil)
	if got := empty.get(context.Background(), 42); got != BackendSupermemory {
		t.Errorf("nil reader backend = %q, want %q", got, BackendSupermemory)
	}
	// installationID 0 means "no installation" — never read flags for it.
	r := &fakeFlagReader{raw: `{"memory_backend":"postgres"}`}
	if got := newBackendCache(r, nil).get(context.Background(), 0); got != BackendSupermemory {
		t.Errorf("zero installation backend = %q, want %q", got, BackendSupermemory)
	}
	if n := atomic.LoadInt32(&r.calls); n != 0 {
		t.Errorf("zero installation read flags %d times, want 0", n)
	}
}

// TestBackendCacheAvoidsPerCallReads: GetIndexer runs on every review and on
// several API paths, so an uncached flag read would add a DB round-trip to
// each. The cache must serve repeats, and must be per-installation — a cache
// keyed carelessly would serve one install's backend to another, which during
// the migration means reading a corpus that belongs to a different tenant's
// backend entirely.
func TestBackendCacheAvoidsPerCallReads(t *testing.T) {
	r := &fakeFlagReader{raw: `{"memory_backend":"postgres"}`}
	b := newBackendCache(r, nil)
	for i := 0; i < 5; i++ {
		if got := b.get(context.Background(), 1); got != BackendPostgres {
			t.Fatalf("call %d: backend = %q", i, got)
		}
	}
	if n := atomic.LoadInt32(&r.calls); n != 1 {
		t.Errorf("flag reads = %d, want 1 (cache not serving repeats)", n)
	}

	// A second installation must resolve independently, not inherit the first.
	sm := &fakeFlagReader{raw: `{}`}
	b2 := newBackendCache(sm, nil)
	if got := b2.get(context.Background(), 1); got != BackendSupermemory {
		t.Fatalf("second cache backend = %q", got)
	}
	if got := b.get(context.Background(), 2); got != BackendPostgres {
		t.Fatalf("install 2 backend = %q, want its own resolution", got)
	}
	if n := atomic.LoadInt32(&r.calls); n != 2 {
		t.Errorf("flag reads = %d, want 2 (one per installation)", n)
	}
}

// TestBackendCacheInvalidate: a flag flip must take effect on the next call,
// not after backendCacheTTL. Phase 5 flips installs one at a time and watches
// telemetry; a five-minute lag between flipping and observing would make that
// loop untrustworthy.
func TestBackendCacheInvalidate(t *testing.T) {
	r := &fakeFlagReader{raw: `{}`}
	b := newBackendCache(r, nil)
	if got := b.get(context.Background(), 42); got != BackendSupermemory {
		t.Fatalf("initial backend = %q", got)
	}
	r.raw = `{"memory_backend":"postgres"}`
	if got := b.get(context.Background(), 42); got != BackendSupermemory {
		t.Fatalf("backend changed before invalidate = %q; the cache is not caching", got)
	}
	b.invalidate(42)
	if got := b.get(context.Background(), 42); got != BackendPostgres {
		t.Errorf("backend after invalidate = %q, want the flipped value", got)
	}
}

// blockingFlagReader lets a test hold a resolve open, so an invalidation can
// be made to land in the window where the cache lock is released.
type blockingFlagReader struct {
	entered chan struct{}
	release chan struct{}
	raw     string
}

// The value is captured on entry, before the test is allowed to change it:
// the modelled scenario is a read that already saw the OLD flag and is slow to
// return, not one that observes the rollback. Capturing before the send to
// entered also establishes the happens-before edge for b.raw.
func (b *blockingFlagReader) GetInstallationFeatureFlags(context.Context, int64) (json.RawMessage, error) {
	raw := b.raw
	b.entered <- struct{}{}
	<-b.release
	return json.RawMessage(raw), nil
}

// TestBackendCacheInvalidateDuringResolve guards the rollback lever. get()
// releases the lock across the flag read, so an invalidate() can land while a
// resolve is in flight: it deletes an entry that does not exist yet, and the
// resolve then stores its stale answer under a FRESH TTL, silently undoing the
// invalidation. The direction of harm is the bad one — an operator reverting
// an installation to Supermemory would have Postgres restored under them for
// the next five minutes.
//
// This is a lost update, not a data race, so -race cannot see it.
func TestBackendCacheInvalidateDuringResolve(t *testing.T) {
	r := &blockingFlagReader{
		// Buffered: the post-rollback re-read resolves again, and its send
		// must not block on a receiver the test no longer has.
		entered: make(chan struct{}, 4),
		release: make(chan struct{}),
		raw:     `{"memory_backend":"postgres"}`,
	}
	b := newBackendCache(r, nil)

	done := make(chan Backend, 1)
	go func() { done <- b.get(context.Background(), 42) }()

	<-r.entered      // the resolve is past the lock, holding "postgres"
	b.invalidate(42) // operator rolls back mid-flight
	r.raw = `{"memory_backend":"supermemory"}`
	close(r.release)

	if got := <-done; got != BackendPostgres {
		t.Fatalf("in-flight resolve returned %q; it read before the rollback", got)
	}
	// The in-flight answer must NOT have been cached: the next call re-reads
	// and observes the rollback.
	if got := b.get(context.Background(), 42); got != BackendSupermemory {
		t.Errorf("backend = %q after a rollback raced an in-flight read; the invalidation was swallowed", got)
	}
}

// TestUnrecognizedBackendValueIsLogged: every other ambiguous branch in
// resolve() logs, and this one used to answer silently. A phase-5 flip is a
// hand-written UPDATE, so "Postgres" or "pgvector" is an ordinary typo — and
// the operator's confirmation is re-selecting the row, which shows the key
// present. Silence there means the rollout looks successful while measuring
// nothing, because an unchanged Postgres corpus is indistinguishable from
// "nothing relevant found".
func TestUnrecognizedBackendValueIsLogged(t *testing.T) {
	for _, v := range []string{"Postgres", "pgvector", " postgres ", "POSTGRES", "pg"} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		b := newBackendCache(&fakeFlagReader{raw: `{"memory_backend":"` + v + `"}`}, logger)

		if got := b.get(context.Background(), 42); got != BackendSupermemory {
			t.Errorf("%q resolved to %q, want supermemory", v, got)
		}
		if !strings.Contains(buf.String(), "unrecognized memory_backend") {
			t.Errorf("%q resolved silently; an operator typo would be invisible. log=%q", v, buf.String())
		}
	}
	// The two recognized values, and an absent key, must NOT warn — a warning
	// on every review for every install would train people to ignore it.
	for _, raw := range []string{`{"memory_backend":"postgres"}`, `{"memory_backend":"supermemory"}`, `{}`, `{"memory_backend":""}`} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		newBackendCache(&fakeFlagReader{raw: raw}, logger).get(context.Background(), 42)
		if strings.Contains(buf.String(), "unrecognized") {
			t.Errorf("%s warned; only unrecognized values should. log=%q", raw, buf.String())
		}
	}
}

// TestInstallationBackendUncached: the maintenance CLIs hold no Registry and
// run one-shot, so they need the answer without a cache in the way.
func TestInstallationBackendUncached(t *testing.T) {
	r := &fakeFlagReader{raw: `{"memory_backend":"postgres"}`}
	got, err := InstallationBackend(context.Background(), r, 42, discardLogger())
	if err != nil || got != BackendPostgres {
		t.Errorf("InstallationBackend = %q, %v; want postgres", got, err)
	}
	r.raw = `{}`
	got, err = InstallationBackend(context.Background(), r, 42, discardLogger())
	if err != nil || got != BackendSupermemory {
		t.Errorf("InstallationBackend = %q, %v after the flag changed; it must not cache", got, err)
	}
}

// TestInstallationBackendSurfacesReadError is the CLI guards' fail-closed
// hinge. resolve() already distinguishes "the flag says supermemory" from "I
// could not find out"; folding the second into the first made the guards fail
// OPEN, so one pool timeout during a sweep would let reconcile-memory reindex
// a flipped install through Supermemory and write SM doc ids over
// patterns.supermemory_id — after which dashboard deletion matches zero rows
// and returns 200 while the memory stays live.
//
// The app deliberately wants the opposite polarity, which is why the decision
// belongs to the caller rather than to this function.
func TestInstallationBackendSurfacesReadError(t *testing.T) {
	r := &fakeFlagReader{raw: `{"memory_backend":"postgres"}`, err: fmt.Errorf("timeout: pool exhausted")}
	got, err := InstallationBackend(context.Background(), r, 42, discardLogger())
	if err == nil {
		t.Fatalf("read error swallowed; got %q with nil error — a mutating CLI would proceed", got)
	}
	// The returned value still fails safe for anyone who ignores the error.
	if got != BackendSupermemory {
		t.Errorf("backend = %q on a read error, want supermemory", got)
	}
}

// TestBackendErrorResolveCachesBriefly is the sibling of
// TestEmbedderErrorResolveCachesBriefly, which was covering the identical
// policy while this side had none: publishing a failed flag read for the full
// backendCacheTTL left the suite green, because TestBackendSelectionFailsSafe's
// "db error" case asserts only the returned value, never the stored expiry.
//
// The behaviour it protects: one transient read error pins the installation to
// Supermemory for the window, and for an install already migrated and stripped
// of its Supermemory key that means GetIndexer returns nil — memory silently
// OFF, since a nil Indexer is quiet everywhere downstream.
func TestBackendErrorResolveCachesBriefly(t *testing.T) {
	// The policy, not just the value: bounding the expiry against
	// errorBackendTTL is a RELATIVE check, so raising that constant would move
	// the bar with it and restore the full-length window unnoticed.
	if errorBackendTTL >= backendCacheTTL {
		t.Errorf("errorBackendTTL (%v) is not shorter than backendCacheTTL (%v); a failed read is now cached as long as a successful one",
			errorBackendTTL, backendCacheTTL)
	}

	b := newBackendCache(&fakeFlagReader{err: fmt.Errorf("connection refused")}, discardLogger())
	if got := b.get(context.Background(), 42); got != BackendSupermemory {
		t.Fatalf("read error resolved to %q", got)
	}
	b.mu.Lock()
	entry, ok := b.cache[42]
	b.mu.Unlock()
	if !ok {
		t.Fatal("failed read cached nothing; every call would re-query a database that is already struggling")
	}
	if d := time.Until(entry.expiresAt); d > errorBackendTTL {
		t.Errorf("failed read cached for %v, want at most %v", d, errorBackendTTL)
	}
}

// ctxAwareFlagReader propagates the caller's context error, as a real DB
// driver does.
type ctxAwareFlagReader struct {
	raw   string
	calls int32
}

func (c *ctxAwareFlagReader) GetInstallationFeatureFlags(ctx context.Context, _ int64) (json.RawMessage, error) {
	atomic.AddInt32(&c.calls, 1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.RawMessage(c.raw), nil
}

// TestBackendCacheDoesNotCacheCancelledCaller: a dead CALLER context says
// nothing about the installation. Caching it publishes one aborted request's
// failure to every other caller — and for an install flagged postgres, a
// review inside that window gets the Supermemory indexer and writes its
// memories into a container the install's own reads no longer consult. That
// is a split corpus, not a degrade, and nothing logs a mismatch.
//
// Several call sites pass the caller's request context directly (the rules,
// patterns and commands handlers), so a user navigating away is enough.
func TestBackendCacheDoesNotCacheCancelledCaller(t *testing.T) {
	r := &ctxAwareFlagReader{raw: `{"memory_backend":"postgres"}`}
	b := newBackendCache(r, nil)

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if got := b.get(dead, 42); got != BackendSupermemory {
		t.Fatalf("cancelled caller got %q; its own request is dead either way", got)
	}

	// A healthy caller must not inherit that answer.
	if got := b.get(context.Background(), 42); got != BackendPostgres {
		t.Errorf("healthy caller got %q after one cancelled request; the cancellation was published to everyone", got)
	}
	if n := atomic.LoadInt32(&r.calls); n != 2 {
		t.Errorf("flag reads = %d, want 2 (the cancelled result must not be cached)", n)
	}
}

// TestBackendCacheConcurrent: GetIndexer is called from concurrent pipeline
// stages. Run under -race.
func TestBackendCacheConcurrent(t *testing.T) {
	b := newBackendCache(&fakeFlagReader{raw: `{"memory_backend":"postgres"}`}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.get(context.Background(), int64(i%4))
			if i%8 == 0 {
				b.invalidate(int64(i % 4))
			}
		}(i)
	}
	wg.Wait()
}
