package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Registry hands out a per-installation Indexer. Memory lives in Postgres, so
// there is nothing to cache here: PGIndexer is a small value over a shared
// pool, and the only expensive dependency — the embedder — is cached by
// EmbedderRegistry behind GetEmbedder.
//
// The type exists because it is the seam the thirteen GetIndexer call sites
// bind to, not because it manages state.
type Registry struct {
	logger *slog.Logger

	// Wired by WithPostgresBackend. Without it GetIndexer returns nil and
	// memory is off, which is the honest answer for an unwired process.
	pool      *pgxpool.Pool
	embedders *EmbedderRegistry
}

// NewRegistry constructs a Registry with no usable backend. Call
// WithPostgresBackend to make GetIndexer return anything.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{logger: logger}
}

// WithPostgresBackend wires the pool and embedder registry GetIndexer needs.
//
// Storage dimensionality comes from embedders rather than a separate argument:
// two copies of the same number default differently (EmbedderRegistry rewrites
// 0 to 1024), and a mismatch makes PGIndexer reject every vector onto the
// fail-open NULL path — silently, behind one Warn, forever.
func (r *Registry) WithPostgresBackend(pool *pgxpool.Pool, embedders *EmbedderRegistry) *Registry {
	// A width the column cannot store is a misconfiguration whose every
	// outcome is silent. Wider or narrower than StorageDimensions and pgvector
	// rejects the INSERT, rolling back the whole batch — writes stop, at Warn.
	// Match the check but not the provider (voyage-4 ignores the dimensions
	// request parameter) and every vector is discarded to NULL instead.
	//
	// Refusing here leaves pool nil, so an unusable backend costs nothing per
	// call and a once-at-startup Error is the signal.
	if pool == nil || embedders == nil {
		r.log().Error("postgres memory backend NOT wired: a dependency is missing",
			"pool", pool != nil, "embedders", embedders != nil)
		return r
	}
	if d := embedders.Dimensions(); d != StorageDimensions {
		r.log().Error("postgres memory backend NOT wired: EMBEDDINGS_DIMENSIONS does not match the memories.embedding column",
			"configured", d, "column", StorageDimensions)
		return r
	}
	r.pool = pool
	r.embedders = embedders
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

// GetIndexer returns an Indexer for the installation, or nil if the backend
// was never wired. Callers treat nil as "memory is unavailable" and carry on.
//
// A nil embedder is NOT a failure: PGIndexer degrades to writing rows with a
// NULL embedding, which is migration 057's documented fail-open write path.
// Such a row is reachable through the full-text leg until ReembedMissing
// repairs it.
func (r *Registry) GetIndexer(ctx context.Context, installationID int64) Indexer {
	if r.pool == nil || r.embedders == nil {
		return nil
	}
	// GetEmbedder absorbs resolution failures internally (a broken BYOK row
	// falls back to the platform key) and never returns a non-nil error.
	embedder, _ := r.embedders.GetEmbedder(ctx, installationID)
	var disableSharedDecay bool
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(default_settings, '{}'::jsonb) @> '{"disable_shared_decay": true}'::jsonb
		FROM installations WHERE id = $1`, installationID).Scan(&disableSharedDecay); err != nil {
		// Settings lookup failure must not disable retirement silently. Default
		// decay remains active and the warning makes the override failure visible.
		r.log().Warn("resolve shared memory decay setting; using decay default",
			"installation_id", installationID, "error", err)
	}
	return NewPGIndexer(r.pool, embedder, installationID, r.embedders.Dimensions(), r.log(),
		WithSharedDecayDisabled(disableSharedDecay))
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

// ReembedCurrentSpace repairs one installation after embedding configuration
// rotation. A session advisory lock prevents two machines from buying the same
// replacement vectors concurrently; a busy lock is success because its holder
// is already converging the same tenant. The caller must provide a bounded
// context because a large corpus can require many provider batches.
func (r *Registry) ReembedCurrentSpace(ctx context.Context, installationID int64, batchSize int) (int, error) {
	if r.pool == nil || r.embedders == nil {
		return 0, fmt.Errorf("reembed current space: postgres memory backend is not configured")
	}
	embedder, _ := r.embedders.GetEmbedder(ctx, installationID)
	if embedder == nil {
		return 0, fmt.Errorf("reembed current space: no embedder for installation %d", installationID)
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("reembed current space: acquire lock connection: %w", err)
	}
	defer conn.Release()
	// Namespace "ARGU" in the high bits keeps this lock independent of other
	// tenant-scoped advisory locks while retaining the full installation id.
	lockKey := (int64(0x41524755) << 32) ^ installationID
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&acquired); err != nil {
		return 0, fmt.Errorf("reembed current space: acquire advisory lock: %w", err)
	}
	if !acquired {
		r.log().Info("memory reembed already running", "installation_id", installationID)
		return 0, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&released); err != nil || !released {
			r.log().Warn("release memory reembed advisory lock", "installation_id", installationID, "released", released, "error", err)
		}
	}()

	idx := NewPGIndexer(r.pool, embedder, installationID, r.embedders.Dimensions(), r.log())
	return idx.ReembedMissing(ctx, batchSize)
}
