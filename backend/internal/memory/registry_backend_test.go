package memory

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/crypto"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// offline marks the installation's LLM filter as already disabled. GetIndexer
// otherwise issues a real GET+PATCH against api.supermemory.ai — the default
// base URL — with whatever token the fake resolver produced, so these tests
// would dial a third party's production API on every CI run and block on the
// client's 30s timeout wherever egress hangs. Nothing here is testing the
// Supermemory roundtrip; it is incidental to reaching the backend selection.
func offline(reg *Registry, installationID int64) *Registry {
	reg.mu.Lock()
	reg.filterDisabled[installationID] = true
	reg.mu.Unlock()
	return reg
}

// lazyPool builds a pool that never connects — pgxpool dials on first use, so
// this is enough to exercise wiring and construction without a database.
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/argus_unused")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func defaultEmbedders() *EmbedderRegistry {
	return NewEmbedderRegistry(&fakeEmbedResolver{}, PlatformEmbeddings{}, discardLogger())
}

// wired builds a Registry with a usable Postgres backend, which is what
// installs the flag cache: WithPostgresBackend deliberately leaves it nil when
// the backend is unusable, so an unusable backend costs no flag reads.
func wired(t *testing.T, flags FeatureFlagReader) *Registry {
	t.Helper()
	return NewRegistry(&fakeResolver{enc: encKey(t)}, discardLogger()).
		WithPostgresBackend(lazyPool(t), defaultEmbedders(), flags)
}

// encKey returns an encrypted Supermemory key so GetClient yields a client.
func encKey(t *testing.T) string {
	t.Helper()
	if err := crypto.Init(hex.EncodeToString(make([]byte, 32))); err != nil {
		t.Fatalf("crypto.Init: %v", err)
	}
	enc, err := crypto.Encrypt("sm-api-key")
	if err != nil {
		t.Fatalf("crypto.Encrypt: %v", err)
	}
	return enc
}

// TestGetIndexerDefaultsToSupermemory: a Registry with no Postgres wiring —
// the shape of both cmd/ binaries and of any deploy that forgets
// WithPostgresBackend — must keep serving Supermemory. Asserting the concrete
// type, not just non-nil: the bug this guards against returns a working
// indexer pointed at the wrong store, which no nil-check would catch.
func TestGetIndexerDefaultsToSupermemory(t *testing.T) {
	reg := offline(NewRegistry(&fakeResolver{enc: encKey(t)}, discardLogger()), 42)
	idx := reg.GetIndexer(context.Background(), 42)
	if idx == nil {
		t.Fatal("GetIndexer returned nil for a keyed installation")
	}
	if _, ok := idx.(*PGIndexer); ok {
		t.Fatal("unwired Registry served a PGIndexer")
	}
	if _, ok := idx.(*indexerImpl); !ok {
		t.Errorf("GetIndexer returned %T, want the Supermemory *indexerImpl", idx)
	}
}

// TestGetIndexerFlagOffStaysSupermemory: wiring the Postgres backend must not
// move anyone by itself. This is the deploy state of this PR — wired,
// flag unset everywhere — so a regression here moves every installation at
// once, which is precisely the blast radius the flag exists to prevent.
func TestGetIndexerFlagOffStaysSupermemory(t *testing.T) {
	reg := offline(wired(t, &fakeFlagReader{raw: `{"cross_pr_checks":true}`}), 42)
	idx := reg.GetIndexer(context.Background(), 42)
	if _, ok := idx.(*PGIndexer); ok {
		t.Fatal("flag unset but GetIndexer served a PGIndexer")
	}
}

// TestGetIndexerFallsBackWhenPGUnavailable: a dependency is missing (wiring
// bug, or a binary that constructs its own Registry). Serving Supermemory
// keeps memory working; returning nil would disable memory for the install,
// and returning a PGIndexer with a nil pool would panic on first use.
func TestGetIndexerFallsBackWhenPGUnavailable(t *testing.T) {
	reg := offline(NewRegistry(&fakeResolver{enc: encKey(t)}, discardLogger()).
		WithPostgresBackend(nil, defaultEmbedders(), &fakeFlagReader{raw: `{"memory_backend":"postgres"}`}), 42)
	idx := reg.GetIndexer(context.Background(), 42)
	if idx == nil {
		t.Fatal("fell back to nil — memory disabled for the installation")
	}
	if _, ok := idx.(*PGIndexer); ok {
		t.Fatal("served a PGIndexer despite a nil pool; first use would panic")
	}
}

// TestGetIndexerServesPostgresWhenFlagged is the positive case — the one line
// this whole change exists to add. Every other test here asserts a PGIndexer
// is NOT served, and each passes a nil pool, so the dispatch's `return idx`
// branch was unreachable in tests: inverting `== BackendPostgres` or replacing
// it with `false` left the entire suite green, which means a refactor could
// silently un-wire the backend (or, inverted, wire it for everyone) unnoticed.
//
// No database is needed: pgxpool connects lazily, so a pool built from a
// parsed config is enough to reach the constructor.
func TestGetIndexerServesPostgresWhenFlagged(t *testing.T) {
	reg := offline(wired(t, &fakeFlagReader{raw: `{"memory_backend":"postgres"}`}), 42)

	idx := reg.GetIndexer(context.Background(), 42)
	if idx == nil {
		t.Fatal("flagged installation got no indexer at all")
	}
	pg, ok := idx.(*PGIndexer)
	if !ok {
		t.Fatalf("GetIndexer returned %T, want *PGIndexer — the Postgres dispatch is dead", idx)
	}
	if pg.installationID != 42 {
		t.Errorf("PGIndexer scoped to installation %d, want 42", pg.installationID)
	}
	if pg.dims != StorageDimensions {
		t.Errorf("PGIndexer dims = %d, want %d", pg.dims, StorageDimensions)
	}
}

// TestWithPostgresBackendRefusesWrongDimensions: the memories.embedding column
// is a fixed vector(1024), so EMBEDDINGS_DIMENSIONS is not free. Set it to 768
// or 1536 and both outcomes are silent: pgvector rejects the INSERT and rolls
// back the whole pgx batch (writes stop, at Warn), or — when the provider
// ignores the requested width, as voyage-4 does — every vector is discarded to
// NULL. Refusing to wire keeps flagged installs on Supermemory instead.
func TestWithPostgresBackendRefusesWrongDimensions(t *testing.T) {
	for _, dims := range []int{768, 1536, 1} {
		// A real (lazy) pool, so the refusal is attributable to the width
		// rather than to a missing dependency.
		reg := offline(NewRegistry(&fakeResolver{enc: encKey(t)}, discardLogger()).
			WithPostgresBackend(lazyPool(t),
				NewEmbedderRegistry(&fakeEmbedResolver{}, PlatformEmbeddings{Dimensions: dims}, discardLogger()),
				&fakeFlagReader{raw: `{"memory_backend":"postgres"}`}), 42)
		if reg.pool != nil || reg.embedders != nil {
			t.Errorf("dims=%d: backend wired against a vector(%d) column", dims, StorageDimensions)
		}
		if _, ok := reg.GetIndexer(context.Background(), 42).(*PGIndexer); ok {
			t.Errorf("dims=%d: served a PGIndexer whose writes cannot land", dims)
		}
	}
	// The default (0 -> 1024 in NewEmbedderRegistry) must still wire.
	reg := wired(t, &fakeFlagReader{raw: `{}`})
	if reg.embedders == nil {
		t.Error("default dimensions refused; only a mismatch should refuse")
	}
}

// TestInvalidateBackendRereadsFlag: the feature-flag handler drops the cached
// derivation after writing the column, so the next call re-reads.
func TestInvalidateBackendRereadsFlag(t *testing.T) {
	flags := &fakeFlagReader{raw: `{}`}
	reg := wired(t, flags)
	if got := reg.backends.get(context.Background(), 42); got != BackendSupermemory {
		t.Fatalf("initial backend = %q", got)
	}
	flags.raw = `{"memory_backend":"postgres"}`
	reg.InvalidateBackend(42)
	if got := reg.backends.get(context.Background(), 42); got != BackendPostgres {
		t.Errorf("backend after InvalidateBackend = %q, want the re-read value", got)
	}
}

// TestInvalidateClientLeavesBackendAlone: a Supermemory key change says nothing
// about which backend an installation reads, so it must not force a flag
// re-read. Coupling them would make the cache's invalidation surface wider
// than its actual dependencies.
func TestInvalidateClientLeavesBackendAlone(t *testing.T) {
	flags := &fakeFlagReader{raw: `{}`}
	reg := wired(t, flags)
	if got := reg.backends.get(context.Background(), 42); got != BackendSupermemory {
		t.Fatalf("initial backend = %q", got)
	}
	flags.raw = `{"memory_backend":"postgres"}`
	reg.InvalidateClient(42)
	if got := reg.backends.get(context.Background(), 42); got != BackendSupermemory {
		t.Errorf("backend after InvalidateClient = %q; a key change should not re-read the flag", got)
	}
}

// TestInvalidateOnUnwiredRegistryIsSafe: the API server calls these whenever
// memRegistry is non-nil, which includes registries built without Postgres
// wiring. None may panic.
func TestInvalidateOnUnwiredRegistryIsSafe(t *testing.T) {
	reg := NewRegistry(&fakeResolver{}, discardLogger())
	reg.InvalidateClient(1)
	reg.InvalidateEmbedder(1)
	reg.InvalidateBackend(1)
}
