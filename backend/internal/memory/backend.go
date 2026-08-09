package memory

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Backend names the memory implementation an installation reads and writes.
// The value lives in the installation's feature_flags JSONB under
// "memory_backend"; anything unrecognised means Supermemory, so a typo or a
// half-rolled-out flag can never silently move an install off the
// read-authoritative store mid-migration.
type Backend string

const (
	BackendSupermemory Backend = "supermemory"
	BackendPostgres    Backend = "postgres"
)

// StorageDimensions is the fixed width of the memories.embedding column
// (migration 059). It, not EMBEDDINGS_DIMENSIONS, is the authority on
// dimensionality: pgvector rejects any other width at INSERT, and pgx runs a
// whole batch in one implicit transaction, so one mismatched vector rolls back
// every row in it. Memory indexing failures are non-fatal and log at Warn, so
// the visible symptom is that writes simply stop. Reads fail the same way — a
// query vector of the wrong width errors on every `<=>` comparison.
const StorageDimensions = 1024

// backendCacheTTL bounds how long a flag flip takes to reach a running
// process. GetIndexer runs on every review and on several API paths, so
// reading feature_flags unconditionally would add a DB round-trip to each;
// the TTL mirrors embedderCacheTTL and llm.Registry's provider cache.
// InvalidateBackend drops the entry for an immediate flip.
const backendCacheTTL = 5 * time.Minute

// errorBackendTTL caches a failed flag read briefly rather than for the full
// TTL. Every caller passes its own request/run context, so one client
// disconnect or review timeout is enough to produce a read error — at full TTL
// that single cancellation would pin the installation to Supermemory for five
// minutes. Caching for a short window still bounds retry storms while the
// database is down.
const errorBackendTTL = 10 * time.Second

// FeatureFlagReader is the narrow store seam backend selection needs — the raw
// feature_flags JSONB for an installation. *store.Store satisfies it.
type FeatureFlagReader interface {
	GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error)
}

// backendFlags is the one key we read out of feature_flags. Decoding into a
// dedicated struct rather than pipeline.FeatureFlags keeps the memory package
// free of a pipeline import and means an unrelated flag's type changing cannot
// break backend selection.
type backendFlags struct {
	MemoryBackend string `json:"memory_backend"`
}

type cachedBackend struct {
	backend   Backend
	expiresAt time.Time
}

// backendCache resolves and caches the per-installation backend choice.
//
// Every failure mode resolves to BackendSupermemory: nil reader, zero id, DB
// error, malformed JSON, unknown value. During the migration Supermemory is
// read-authoritative, so falling back to it degrades to the status quo, while
// the opposite default would serve an empty or partially-backfilled Postgres
// corpus as though it were the real memory — silently, since an empty memory
// read is indistinguishable from "nothing relevant found".
type backendCache struct {
	reader FeatureFlagReader
	logger *slog.Logger

	mu    sync.Mutex
	cache map[int64]cachedBackend
	// gen counts invalidations per installation. The flag read happens with
	// the mutex released, so an invalidation can land while a resolve is in
	// flight: it deletes a cache entry that does not exist yet, and the
	// resolve then stores its now-stale answer under a fresh TTL — silently
	// undoing the invalidation. Comparing the generation across the read
	// discards exactly those results. The race detector cannot see this; it is
	// a lost update, not a data race.
	gen map[int64]uint64
}

func newBackendCache(reader FeatureFlagReader, logger *slog.Logger) *backendCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &backendCache{
		reader: reader,
		logger: logger,
		cache:  make(map[int64]cachedBackend),
		gen:    make(map[int64]uint64),
	}
}

// get returns the installation's backend, consulting the cache first.
func (b *backendCache) get(ctx context.Context, installationID int64) Backend {
	if b == nil || b.reader == nil || installationID == 0 {
		return BackendSupermemory
	}

	b.mu.Lock()
	if c, ok := b.cache[installationID]; ok && time.Now().Before(c.expiresAt) {
		b.mu.Unlock()
		return c.backend
	}
	gen := b.gen[installationID]
	b.mu.Unlock()

	backend, err := b.resolve(ctx, installationID)

	// A dead CALLER context says nothing about the installation. Caching this
	// answer would publish one aborted request's failure to every other
	// caller: for an install flagged postgres, a review inside the window gets
	// the Supermemory indexer and writes that review's memories into a
	// container its own reads no longer consult — a split corpus, not a
	// degrade, and nothing logs a mismatch. The short-TTL rationale below
	// covers a sick database; this is a healthy one with a hung-up client.
	if ctx.Err() != nil {
		return backend
	}

	ttl := backendCacheTTL
	if err != nil {
		ttl = errorBackendTTL
	}
	b.mu.Lock()
	// Drop the result if an invalidation raced this read. Rollback is the
	// direction that matters: an operator reverting an install to Supermemory
	// must not have Postgres restored under them by a read that was already
	// in flight.
	if b.gen[installationID] == gen {
		b.cache[installationID] = cachedBackend{backend: backend, expiresAt: time.Now().Add(ttl)}
	}
	b.mu.Unlock()
	return backend
}

// resolve reads and parses the flag, returning the READ error (not a parse
// error) so callers can distinguish "the flag says supermemory" from "I could
// not find out". The app wants both to mean Supermemory; a caller that mutates
// memory wants the second to mean "do nothing". Every case still answers
// Supermemory.
func (b *backendCache) resolve(ctx context.Context, installationID int64) (Backend, error) {
	raw, err := b.reader.GetInstallationFeatureFlags(ctx, installationID)
	if err != nil {
		// Visible at Warn: for an installation already migrated and stripped
		// of its Supermemory key, answering Supermemory here means memory is
		// OFF for the retry window — and a nil Indexer is silent everywhere
		// downstream. The "fallback is the status quo" argument holds only
		// while the install still has a Supermemory key, which stops being
		// true at exactly the state this program is driving toward.
		// A cancelled caller is an ordinary client disconnect, not database
		// trouble, and get() caches nothing in that case — so advertising a
		// retry window would be false. Phase 5 will read this line to judge
		// flag-read health; conflating the two would make it useless.
		if ctx.Err() != nil {
			b.logger.Debug("memory backend flag read abandoned by caller; assuming supermemory",
				"error", err, "installation_id", installationID)
		} else {
			b.logger.Warn("memory backend flag read failed; assuming supermemory",
				"error", err, "installation_id", installationID, "retry_in", errorBackendTTL)
		}
		return BackendSupermemory, err
	}
	var flags backendFlags
	if err := json.Unmarshal(raw, &flags); err != nil {
		b.logger.Warn("memory backend flag is not valid JSON; assuming supermemory",
			"error", err, "installation_id", installationID)
		return BackendSupermemory, nil
	}
	switch Backend(flags.MemoryBackend) {
	case BackendPostgres:
		return BackendPostgres, nil
	case BackendSupermemory, "":
		return BackendSupermemory, nil
	default:
		// The one ambiguous branch that used to answer silently. A phase-5
		// flip is a hand-written UPDATE, so "Postgres", "pgvector" or a
		// trailing space is an ordinary typo — and the operator's confirmation
		// is re-selecting the row, which shows the key present. Without this
		// the install stays on Supermemory, the answer is cached, and the
		// rollout looks successful while measuring nothing, because an
		// unchanged Postgres corpus is indistinguishable from "nothing
		// relevant found".
		b.logger.Warn("unrecognized memory_backend value; assuming supermemory",
			"value", flags.MemoryBackend, "installation_id", installationID,
			"recognized", []Backend{BackendSupermemory, BackendPostgres})
		return BackendSupermemory, nil
	}
}

// InstallationBackend resolves an installation's backend without caching, for
// one-shot callers that hold no Registry — the maintenance CLIs, which must
// know whether an install has moved before they mutate its memory.
//
// The error is returned rather than folded into a Supermemory answer, because
// these callers need the opposite polarity from the app: the app treats an
// unreadable flag as "carry on with Supermemory", while a tool that rewrites
// patterns.supermemory_id must treat it as "do nothing". Swallowing it here
// would make the CLI guards fail OPEN on a single pool timeout and perform
// exactly the corruption they exist to prevent.
func InstallationBackend(ctx context.Context, reader FeatureFlagReader, installationID int64, logger *slog.Logger) (Backend, error) {
	// Same preconditions as the cached path. Reaching resolve() directly would
	// give the exported entry point different rules from its sibling: a nil
	// reader would panic instead of failing safe, and id 0 would issue a
	// pointless query whose ErrNoRows the CLIs would read as "cannot
	// determine" and skip on.
	if reader == nil || installationID == 0 {
		return BackendSupermemory, nil
	}
	return newBackendCache(reader, logger).resolve(ctx, installationID)
}

func (b *backendCache) invalidate(installationID int64) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.cache, installationID)
	b.gen[installationID]++
	b.mu.Unlock()
}
