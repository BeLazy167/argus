package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/BeLazy167/argus/backend/internal/crypto"
)

// KeyResolver loads encrypted Supermemory API keys for installations.
type KeyResolver interface {
	GetSupermemoryKey(ctx context.Context, installationID int64) (string, error)
}

// Registry provides per-installation memory.Client instances with caching.
// Tracks which installations have had their server-side LLM filter disabled
// so we only issue the UpdateSettings call once per install per process.
type Registry struct {
	mu             sync.RWMutex
	clients        map[int64]*Client
	filterDisabled map[int64]bool
	// clientGroup dedupes in-flight client construction per installation.
	// Without it, N concurrent first-callers for the same install each RLock-miss
	// the cache, then independently resolve+decrypt the key and build their own
	// *Client with its own rate limiter — multiplying the per-install QPS/burst
	// cap by N under exactly the concurrent-pipeline scenario the limiter exists
	// to bound, plus N redundant DB reads + decrypts. singleflight collapses them
	// into one build; the rest share its result.
	clientGroup singleflight.Group
	// filterGroup dedupes in-flight DisableLLMFilter calls per installation.
	// Without this, N concurrent GetIndexer calls on a fresh install each
	// observe filterDisabled[id]=false and issue their own UpdateSettings
	// roundtrip — wasted quota, inconsistent log volume. singleflight lets
	// only one call hit the API; the rest wait on its result.
	filterGroup singleflight.Group
	resolver    KeyResolver
	logger      *slog.Logger

	// Postgres backend, wired by WithPostgresBackend. Without it GetIndexer
	// can only serve Supermemory, whatever the flag says.
	pool      *pgxpool.Pool
	embedders *EmbedderRegistry
	backends  *backendCache
}

// NewRegistry constructs a Registry that resolves encrypted per-installation
// Supermemory keys via resolver. Supermemory is the only available backend
// until WithPostgresBackend is called.
func NewRegistry(resolver KeyResolver, logger *slog.Logger) *Registry {
	return &Registry{
		clients:        make(map[int64]*Client),
		filterDisabled: make(map[int64]bool),
		resolver:       resolver,
		logger:         logger,
	}
}

// WithPostgresBackend makes the Postgres indexer reachable for installations
// whose memory_backend flag selects it. Without this call — and on any
// individual dependency being nil — GetIndexer serves Supermemory regardless
// of the flag, so a deploy that forgets the wiring degrades to the status quo
// rather than to an empty corpus.
// Storage dimensionality comes from embedders, not from a separate argument:
// two copies of the same number default differently (EmbedderRegistry rewrites
// 0 to 1024), and a mismatch makes PGIndexer reject every vector onto the
// fail-open NULL path — silently, behind one Warn, forever.
func (r *Registry) WithPostgresBackend(pool *pgxpool.Pool, embedders *EmbedderRegistry, flags FeatureFlagReader) *Registry {
	// A width the column cannot store is a misconfiguration whose every
	// outcome is silent. Wider or narrower than StorageDimensions and pgvector
	// rejects the INSERT, rolling back the whole batch — writes stop, at Warn,
	// and reads error on every distance comparison. Match the check but not
	// the provider (voyage-4 ignores the dimensions request parameter) and
	// every vector is discarded to NULL instead.
	//
	// Refusing here leaves backends nil, so GetIndexer never reads the flag
	// and never logs per call: an unusable backend should cost nothing, and a
	// once-at-startup Error is the signal. Same for a nil embedder registry.
	if pool == nil || embedders == nil || flags == nil {
		r.log().Error("postgres memory backend NOT wired: a dependency is missing",
			"pool", pool != nil, "embedders", embedders != nil, "flags", flags != nil)
		return r
	}
	if d := embedders.Dimensions(); d != StorageDimensions {
		r.log().Error("postgres memory backend NOT wired: EMBEDDINGS_DIMENSIONS does not match the memories.embedding column",
			"configured", d, "column", StorageDimensions)
		return r
	}
	r.pool = pool
	r.embedders = embedders
	r.backends = newBackendCache(flags, r.log())
	return r
}

// log returns a usable logger. NewRegistry accepts a nil *slog.Logger, so
// every logging path in this type goes through here — half-safety is worse
// than none, since it makes the contract look honoured on whichever path a
// reader happens to check.
func (r *Registry) log() *slog.Logger {
	if r.logger == nil {
		return slog.Default()
	}
	return r.logger
}

// GetClient returns a cached Client for the installation, or nil if no key configured.
//
// Client construction is single-flighted per installation: the RLock fast-path
// serves the steady state, and concurrent first-callers coalesce through
// clientGroup so exactly one *Client (one rate limiter) exists per installation.
// Key-resolution / decrypt failures are logged inside the group (once, by the
// leader) and surface as a nil client to every caller.
func (r *Registry) GetClient(ctx context.Context, installationID int64) *Client {
	r.mu.RLock()
	if c, ok := r.clients[installationID]; ok {
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	key := fmt.Sprintf("%d", installationID)
	v, _, _ := r.clientGroup.Do(key, func() (any, error) {
		// Re-check under the group: a prior leader may have cached a client
		// while we were queued behind it.
		r.mu.RLock()
		cached, ok := r.clients[installationID]
		r.mu.RUnlock()
		if ok {
			return cached, nil
		}

		enc, err := r.resolver.GetSupermemoryKey(ctx, installationID)
		if err != nil {
			r.log().Warn("failed to load supermemory key", "error", err, "installation_id", installationID)
			return (*Client)(nil), nil
		}
		if enc == "" {
			return (*Client)(nil), nil
		}
		secret, err := crypto.Decrypt(enc)
		if err != nil {
			r.log().Error("failed to decrypt supermemory key", "error", err, "installation_id", installationID)
			return (*Client)(nil), nil
		}

		// Attach a per-installation token bucket so no single PR run saturates
		// Supermemory's quota. QPS/burst are defaults here; Bundle 3 will read
		// overrides from org_settings and inject via InvalidateClient + refresh.
		limiter := NewLimiter(DefaultSupermemoryQPS, DefaultSupermemoryBurst)
		client := NewClient(secret, WithLimiter(limiter))

		r.mu.Lock()
		r.clients[installationID] = client
		r.mu.Unlock()
		return client, nil
	})

	client, _ := v.(*Client)
	return client
}

// GetIndexer returns an Indexer for the installation, or nil if memory is
// unavailable for it. The backend is chosen per installation from the
// memory_backend feature flag; every one of the thirteen call sites is
// unaffected, which is the whole point of the Indexer seam.
//
// Supermemory remains the default and the fallback: see backendCache for why
// every failure resolves that way.
func (r *Registry) GetIndexer(ctx context.Context, installationID int64) Indexer {
	if r.backends.get(ctx, installationID) == BackendPostgres {
		if idx := r.pgIndexer(ctx, installationID); idx != nil {
			return idx
		}
		// Defensive: WithPostgresBackend sets pool, embedders and backends
		// together, so a flag answer of postgres implies the deps exist and
		// this branch is unreachable in the wired app. It survives for
		// hand-constructed registries — falling through keeps memory working,
		// where returning nil would silently disable it.
		r.log().Warn("postgres memory backend selected but unavailable; using supermemory",
			"installation_id", installationID)
	}
	return r.supermemoryIndexer(ctx, installationID)
}

// pgIndexer builds the Postgres-backed Indexer, or nil if its dependencies are
// missing. A nil embedder is NOT a failure: PGIndexer degrades to writing rows
// with a NULL embedding (FTS-searchable immediately, backfilled later), which
// is migration 057's documented fail-open write path.
func (r *Registry) pgIndexer(ctx context.Context, installationID int64) Indexer {
	if r.pool == nil || r.embedders == nil {
		return nil
	}
	// GetEmbedder absorbs resolution failures internally (a broken BYOK row
	// falls back to the platform key) and never returns a non-nil error. A nil
	// embedder is not a failure either: PGIndexer writes rows with a NULL
	// embedding, FTS-searchable immediately and backfilled later — migration
	// 057's fail-open write path.
	embedder, _ := r.embedders.GetEmbedder(ctx, installationID)
	return NewPGIndexer(r.pool, embedder, installationID, r.embedders.Dimensions(), r.log())
}

// supermemoryIndexer is the pre-existing path, unchanged: a client keyed by
// the install's BYOK key, with the server-side LLM filter disabled once per
// install per process — Argus pre-filters at the application layer, so the
// server filter only adds latency and non-determinism.
func (r *Registry) supermemoryIndexer(ctx context.Context, installationID int64) Indexer {
	client := r.GetClient(ctx, installationID)
	if client == nil {
		return nil
	}
	indexer := NewIndexer(client, r.log())

	r.mu.RLock()
	disabled := r.filterDisabled[installationID]
	r.mu.RUnlock()

	if !disabled {
		// singleflight coalesces concurrent calls for the same installationID
		// into a single API roundtrip. The Do closure is guaranteed to run at
		// most once per in-flight group; subsequent callers block until the
		// leader returns, then all share its result. On success we mark the
		// filter disabled so future GetIndexer calls skip the group entirely.
		// On failure we do NOT mark it — a transient 5xx should be retried on
		// the next webhook event.
		key := fmt.Sprintf("disable-filter:%d", installationID)
		_, err, _ := r.filterGroup.Do(key, func() (any, error) {
			// Re-check inside the group: a prior leader may have finished and
			// already marked the install disabled while we were waiting.
			r.mu.RLock()
			already := r.filterDisabled[installationID]
			r.mu.RUnlock()
			if already {
				return nil, nil
			}
			if err := indexer.DisableLLMFilter(ctx); err != nil {
				return nil, err
			}
			// Visible at Info: this mutated an ACCOUNT-level setting on the
			// customer's BYOK Supermemory org (not container-scoped), so an
			// operator can trace when/which install flipped it. DisableLLMFilter
			// itself no-ops the PATCH when the filter is already off.
			r.log().Info("disabled supermemory account LLM filter", "installation_id", installationID)
			r.mu.Lock()
			r.filterDisabled[installationID] = true
			r.mu.Unlock()
			return nil, nil
		})
		if err != nil {
			r.log().Warn("disabling supermemory LLM filter (will retry)", "error", err, "installation_id", installationID)
		}
	}

	return indexer
}

// InvalidateClient removes the cached client for an installation.
// Call when the API key is changed or deleted. It deliberately does NOT touch
// the cached backend choice: a Supermemory key change says nothing about which
// backend the installation reads.
func (r *Registry) InvalidateClient(installationID int64) {
	r.mu.Lock()
	delete(r.clients, installationID)
	delete(r.filterDisabled, installationID)
	r.mu.Unlock()
}

// InvalidateEmbedder drops the cached embedder for an installation. Call after
// any provider-key write that could have touched the "embeddings" slot: the
// delete path identifies keys by id and cannot tell which provider it removed,
// so callers invalidate unconditionally. The cost is one cache miss; the cost
// of missing it is up to embedderCacheTTL of writes embedded with a revoked or
// superseded key, landing rows in a vector space the reader will not search.
func (r *Registry) InvalidateEmbedder(installationID int64) {
	if r.embedders == nil {
		return
	}
	r.embedders.Invalidate(installationID)
}

// InvalidateBackend drops the cached memory_backend choice so a flag flip
// takes effect on the next GetIndexer rather than after backendCacheTTL.
// Called by the feature-flag handler; the TTL covers flips made directly in
// the database.
func (r *Registry) InvalidateBackend(installationID int64) {
	r.backends.invalidate(installationID)
}
