package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
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
	pool          *pgxpool.Pool
	embedders     *EmbedderRegistry
	repairPermits chan struct{}

	repairKeysMu sync.Mutex
	repairKeys   map[int64]*repairKey
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
	repairLimit := reembedConcurrencyLimit(pool.Config().MaxConns)
	r.repairPermits = make(chan struct{}, repairLimit)
	r.log().Info("postgres memory backend wired", "dimensions", embedders.Dimensions(),
		"pool_max_connections", pool.Config().MaxConns, "reembed_concurrency_limit", repairLimit)
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
	operationID := obs.NewLogID()
	started := time.Now()
	r.log().InfoContext(ctx, "memory indexer resolution started", "operation_id", operationID,
		"installation_id", installationID, "backend_wired", r.pool != nil && r.embedders != nil)
	if r.pool == nil || r.embedders == nil {
		r.log().WarnContext(ctx, "memory indexer resolution skipped", "operation_id", operationID,
			"installation_id", installationID, "reason", "backend_not_wired",
			"duration_ms", time.Since(started).Milliseconds())
		return nil
	}
	// GetEmbedder absorbs resolution failures internally (a broken BYOK row
	// falls back to the platform key) and never returns a non-nil error.
	embedder, _ := r.embedders.GetEmbedder(ctx, installationID)
	var disableSharedDecay bool
	settingsErr := r.pool.QueryRow(ctx, `
		SELECT COALESCE(default_settings, '{}'::jsonb) @> '{"disable_shared_decay": true}'::jsonb
		FROM installations WHERE id = $1`, installationID).Scan(&disableSharedDecay)
	if settingsErr != nil {
		// Settings lookup failure must not disable retirement silently. Default
		// decay remains active and the warning makes the override failure visible.
		r.log().WarnContext(ctx, "resolve shared memory decay setting; using decay default",
			"operation_id", operationID, "installation_id", installationID, "error", settingsErr)
	}
	idx := NewPGIndexer(r.pool, embedder, installationID, r.embedders.Dimensions(), r.log(),
		WithSharedDecayDisabled(disableSharedDecay),
		withWriteEmbedderResolver(r.embedders.resolveUncachedFromConn))
	r.log().InfoContext(ctx, "memory indexer resolution completed", "operation_id", operationID,
		"installation_id", installationID, "embedder_available", embedder != nil,
		"shared_decay_disabled", disableSharedDecay, "settings_defaulted", settingsErr != nil,
		"dimensions", r.embedders.Dimensions(), "duration_ms", time.Since(started).Milliseconds())
	return idx
}

// InvalidateEmbedder drops the cached embedder for an installation. Call after
// any provider-key write that could have touched the "embeddings" slot: the
// delete path identifies keys by id and cannot tell which provider it removed,
// so callers invalidate unconditionally. The cost is one cache miss; the cost
// of missing it is up to embedderCacheTTL of writes embedded with a revoked or
// superseded key, landing rows in a vector space the reader will not search.
func (r *Registry) InvalidateEmbedder(installationID int64) {
	if r.embedders == nil {
		r.log().Debug("memory embedder invalidation skipped", "installation_id", installationID, "reason", "registry_not_wired")
		return
	}
	r.log().Info("memory embedder invalidation started", "installation_id", installationID)
	r.embedders.Invalidate(installationID)
	r.log().Info("memory embedder invalidation completed", "installation_id", installationID)
}

// reembedLockPollInterval bounds how long a waiter can miss a just-released
// advisory lock without turning a busy lock into a dropped repair.
const reembedLockPollInterval = 50 * time.Millisecond

// maxReembedConvergenceRounds bounds configuration churn and concurrent stale
// writers. The outer request/startup context bounds provider and corpus work;
// this bound ensures a tenant rotated continuously returns an explicit retry
// signal instead of monopolizing the advisory lock forever.
const maxReembedConvergenceRounds = 8

// Each repair can simultaneously retain its session-lock connection and
// borrow a second connection for provider/corpus queries. Beyond those pairs,
// reserve one connection for the durable EventBus LISTEN session and one for
// ordinary database work. Pools smaller than four cannot provide every slot;
// a minimum of one avoids disabling repair on two- and three-connection pools.
const (
	reembedConnectionsPerRepair int32 = 2
	reembedReservedConnections  int32 = 2
)

func reembedConcurrencyLimit(maxConns int32) int {
	limit := (maxConns - reembedReservedConnections) / reembedConnectionsPerRepair
	if limit < 1 {
		return 1
	}
	return int(limit)
}

// acquireRepairPermit bounds distinct-tenant winners within this Registry and
// therefore this process/pool. Cross-machine serialization remains the job of
// PostgreSQL advisory locks; separate machine pools do not share this permit.
func (r *Registry) acquireRepairPermit(ctx context.Context) error {
	select {
	case r.repairPermits <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Registry) releaseRepairPermit() {
	<-r.repairPermits
}

// repairKey serializes same-installation callers inside one Registry before
// they can occupy global repair capacity or poll PostgreSQL. refs includes the
// holder and waiters so an entry remains reachable across every handoff.
type repairKey struct {
	permit chan struct{}
	refs   int
}

func (r *Registry) acquireRepairKey(ctx context.Context, installationID int64) (*repairKey, error) {
	r.repairKeysMu.Lock()
	if r.repairKeys == nil {
		r.repairKeys = make(map[int64]*repairKey)
	}
	key := r.repairKeys[installationID]
	if key == nil {
		key = &repairKey{permit: make(chan struct{}, 1)}
		key.permit <- struct{}{}
		r.repairKeys[installationID] = key
	}
	key.refs++
	r.repairKeysMu.Unlock()

	select {
	case <-key.permit:
		return key, nil
	case <-ctx.Done():
		r.dropRepairKeyRef(installationID, key)
		return nil, ctx.Err()
	}
}

func (r *Registry) releaseRepairKey(installationID int64, key *repairKey) {
	// Make the key available before dropping the holder's ref. A concurrent
	// acquirer must find this same entry rather than create a second lock.
	key.permit <- struct{}{}
	r.dropRepairKeyRef(installationID, key)
}

func (r *Registry) dropRepairKeyRef(installationID int64, key *repairKey) {
	r.repairKeysMu.Lock()
	defer r.repairKeysMu.Unlock()

	key.refs--
	if key.refs == 0 && r.repairKeys[installationID] == key {
		delete(r.repairKeys, installationID)
	}
}

// memoryEmbeddingLockKey namespaces the tenant lock independently from every
// other advisory lock while retaining the full installation id.
func memoryEmbeddingLockKey(installationID int64) int64 {
	return (int64(0x41524755) << 32) ^ installationID
}

// acquireEmbeddingLock polls a session advisory lock without reserving a pool
// connection while another session owns it. Only the winning session remains
// checked out; every loser releases before sleeping so the holder can borrow a
// separate connection for corpus and provider-configuration work.
func acquireEmbeddingLock(ctx context.Context, pool *pgxpool.Pool, lockKey int64, shared bool) (*pgxpool.Conn, error) {
	lockKind := "advisory lock"
	query := "SELECT pg_try_advisory_lock($1)"
	if shared {
		lockKind = "shared advisory lock"
		query = "SELECT pg_try_advisory_lock_shared($1)"
	}

	for {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("acquire connection for %s: %w", lockKind, err)
		}
		var acquired bool
		if err := conn.QueryRow(ctx, query, lockKey).Scan(&acquired); err != nil {
			// The server may have acquired the session lock before the client
			// observed the query failure. Never return that session to the pool.
			discardEmbeddingLockConn(conn)
			return nil, fmt.Errorf("try %s: %w", lockKind, err)
		}
		if acquired {
			return conn, nil
		}
		conn.Release()

		timer := time.NewTimer(reembedLockPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for %s: %w", lockKind, ctx.Err())
		case <-timer.C:
		}
	}
}

func acquireReembedLock(ctx context.Context, pool *pgxpool.Pool, lockKey int64) (*pgxpool.Conn, error) {
	return acquireEmbeddingLock(ctx, pool, lockKey, false)
}

// acquireEmbeddingWriteLock takes the shared side of the same tenant lock used
// by repair. Writers can embed concurrently, but an exclusive repair cannot
// report convergence while a writer that resolved the previous space can
// still commit. The writer resolves its embedder only after acquiring this
// lock, so a write starting after repair sees the committed current provider.
func acquireEmbeddingWriteLock(ctx context.Context, pool *pgxpool.Pool, lockKey int64) (*pgxpool.Conn, error) {
	return acquireEmbeddingLock(ctx, pool, lockKey, true)
}

// discardEmbeddingLockConn closes a session whose lock state is uncertain.
// Releasing it to the pool could strand a session advisory lock indefinitely.
func discardEmbeddingLockConn(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = raw.Close(closeCtx)
}

func releaseEmbeddingLock(ctx context.Context, conn *pgxpool.Conn, lockKey int64, shared bool, logger *slog.Logger, installationID int64) {
	query := "SELECT pg_advisory_unlock($1)"
	kind := "memory reembed advisory lock"
	if shared {
		query = "SELECT pg_advisory_unlock_shared($1)"
		kind = "memory write advisory lock"
	}

	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var released bool
	err := conn.QueryRow(unlockCtx, query, lockKey).Scan(&released)
	if err == nil && released {
		conn.Release()
		return
	}

	logger.Warn("release "+kind, "installation_id", installationID, "released", released, "error", err)
	// A failed or false unlock leaves the session's lock depth uncertain. Close
	// it instead of poisoning the pool with a possibly still-locked session.
	discardEmbeddingLockConn(conn)
}

// ReembedCurrentSpace converges one installation to the latest embedding
// configuration committed in PostgreSQL. The tenant advisory lock serializes
// provider spend across machines. Lock waiters do not silently succeed, and a
// holder refreshes desired state after every pass so a B pass that overlaps a
// B-to-C rotation continues to C before reporting convergence.
//
// The caller must provide a bounded context because a large corpus can require
// many provider batches. Configuration churn is bounded separately by
// maxReembedConvergenceRounds and fails explicitly when exhausted.
func (r *Registry) ReembedCurrentSpace(ctx context.Context, installationID int64, batchSize int) (total int, err error) {
	operationID := obs.NewLogID()
	started := time.Now()
	r.log().InfoContext(ctx, "memory reembed current space started", "operation_id", operationID,
		"installation_id", installationID, "batch_size", batchSize)
	defer func() {
		attrs := []any{"operation_id", operationID, "installation_id", installationID, "batch_size", batchSize,
			"reembedded", total, "duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			r.log().ErrorContext(ctx, "memory reembed current space failed", attrs...)
			return
		}
		r.log().InfoContext(ctx, "memory reembed current space completed", attrs...)
	}()
	if r.pool == nil || r.embedders == nil {
		return 0, fmt.Errorf("reembed current space: postgres memory backend is not configured")
	}

	// Serialize this process's duplicate callers before they occupy global
	// capacity or poll PostgreSQL. The advisory lock remains authoritative
	// across Registries and machines.
	repairKey, err := r.acquireRepairKey(ctx, installationID)
	if err != nil {
		return 0, fmt.Errorf("reembed current space: wait for installation repair: %w", err)
	}
	defer r.releaseRepairKey(installationID, repairKey)

	// Bound distinct-tenant winners before borrowing the session-lock
	// connection. Otherwise distinct tenant keys can all win and exhaust the
	// pool while every winner waits for its corpus-work connection.
	if err := r.acquireRepairPermit(ctx); err != nil {
		return 0, fmt.Errorf("reembed current space: wait for repair capacity: %w", err)
	}
	defer r.releaseRepairPermit()

	lockKey := memoryEmbeddingLockKey(installationID)
	conn, err := acquireReembedLock(ctx, r.pool, lockKey)
	if err != nil {
		return 0, fmt.Errorf("reembed current space: acquire advisory lock: %w", err)
	}
	defer releaseEmbeddingLock(ctx, conn, lockKey, false, r.log(), installationID)

	total = 0
	lastTarget, lastDesired := "", ""
	var lastPending int64
	for round := 1; round <= maxReembedConvergenceRounds; round++ {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("reembed current space: %w", err)
		}

		// Capture desired state only after owning the cross-machine lock, and
		// bypass the process TTL: another machine may have committed this
		// rotation, so a locally cached embedder is not authoritative.
		embedder, err := r.embedders.refreshEmbedder(ctx, installationID)
		if err != nil {
			return total, fmt.Errorf("reembed current space: resolve desired space: %w", err)
		}
		if embedder == nil {
			return total, fmt.Errorf("reembed current space: no embedder for installation %d", installationID)
		}
		idx := NewPGIndexer(r.pool, embedder, installationID, r.embedders.Dimensions(), r.log())
		lastTarget = idx.embeddingSpaceID()

		repaired, err := idx.ReembedMissing(ctx, batchSize)
		total += repaired
		if err != nil {
			return total, err
		}

		// Re-read PostgreSQL after the pass. Equality is on the same canonical
		// endpoint/model/dimensions identity stamped on rows; credentials-only
		// rotations therefore do not buy identical vectors again.
		desiredEmbedder, err := r.embedders.refreshEmbedder(ctx, installationID)
		if err != nil {
			return total, fmt.Errorf("reembed current space: verify desired space: %w", err)
		}
		if desiredEmbedder == nil {
			return total, fmt.Errorf("reembed current space: no embedder for installation %d after repair", installationID)
		}
		desiredIdx := NewPGIndexer(r.pool, desiredEmbedder, installationID, r.embedders.Dimensions(), r.log())
		lastDesired = desiredIdx.embeddingSpaceID()
		if lastDesired != lastTarget {
			r.log().Info("embedding space rotated during repair; continuing to latest space",
				"installation_id", installationID, "completed_space", lastTarget,
				"desired_space", lastDesired, "round", round)
			continue
		}

		lastPending, err = desiredIdx.CountReembedPending(ctx)
		if err != nil {
			return total, fmt.Errorf("reembed current space: verify corpus: %w", err)
		}
		if lastPending == 0 {
			return total, nil
		}
		r.log().Info("memory rows drifted during repair; retrying current space",
			"installation_id", installationID, "space", lastDesired,
			"pending", lastPending, "round", round)
	}

	return total, fmt.Errorf(
		"reembed current space: desired state did not stabilize after %d rounds (target=%q desired=%q pending=%d); retry",
		maxReembedConvergenceRounds, lastTarget, lastDesired, lastPending)
}

// ReembedAllCurrentSpaces converges every installation that owns live memory.
// It is safe on every replica: ReembedCurrentSpace serializes each tenant with
// an advisory lock, and a clean tenant performs no embedding calls.
func (r *Registry) ReembedAllCurrentSpaces(ctx context.Context) (total int, err error) {
	operationID := obs.NewLogID()
	started := time.Now()
	r.log().InfoContext(ctx, "memory reembed all spaces started", "operation_id", operationID)
	defer func() {
		attrs := []any{"operation_id", operationID, "reembedded", total,
			"duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			r.log().ErrorContext(ctx, "memory reembed all spaces failed", attrs...)
			return
		}
		r.log().InfoContext(ctx, "memory reembed all spaces completed", attrs...)
	}()
	if r.pool == nil || r.embedders == nil {
		return 0, fmt.Errorf("reembed all spaces: postgres memory backend is not configured")
	}
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT installation_id FROM live_memories ORDER BY installation_id`)
	if err != nil {
		return 0, fmt.Errorf("reembed all spaces: list installations: %w", err)
	}
	var installationIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("reembed all spaces: scan installation: %w", err)
		}
		installationIDs = append(installationIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reembed all spaces: read installations: %w", err)
	}

	total = 0
	var failures []error
	for _, installationID := range installationIDs {
		if err := ctx.Err(); err != nil {
			return total, errors.Join(append(failures, err)...)
		}
		repaired, err := r.ReembedCurrentSpace(ctx, installationID, 100)
		total += repaired
		if err != nil {
			failures = append(failures, fmt.Errorf("installation %d: %w", installationID, err))
		}
	}
	return total, errors.Join(failures...)
}
