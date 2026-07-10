package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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
}

// NewRegistry constructs a Registry that resolves encrypted per-installation
// Supermemory keys via resolver.
func NewRegistry(resolver KeyResolver, logger *slog.Logger) *Registry {
	return &Registry{
		clients:        make(map[int64]*Client),
		filterDisabled: make(map[int64]bool),
		resolver:       resolver,
		logger:         logger,
	}
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
			r.logger.Warn("failed to load supermemory key", "error", err, "installation_id", installationID)
			return (*Client)(nil), nil
		}
		if enc == "" {
			return (*Client)(nil), nil
		}
		secret, err := crypto.Decrypt(enc)
		if err != nil {
			r.logger.Error("failed to decrypt supermemory key", "error", err, "installation_id", installationID)
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

// GetIndexer returns an Indexer for the installation, or nil if no key configured.
// Disables the server-side LLM filter once per install per process — Argus
// pre-filters at the application layer, so the server filter only adds
// latency and non-determinism.
func (r *Registry) GetIndexer(ctx context.Context, installationID int64) Indexer {
	client := r.GetClient(ctx, installationID)
	if client == nil {
		return nil
	}
	indexer := NewIndexer(client, r.logger)

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
			r.logger.Info("disabled supermemory account LLM filter", "installation_id", installationID)
			r.mu.Lock()
			r.filterDisabled[installationID] = true
			r.mu.Unlock()
			return nil, nil
		})
		if err != nil {
			r.logger.Warn("disabling supermemory LLM filter (will retry)", "error", err, "installation_id", installationID)
		}
	}

	return indexer
}

// InvalidateClient removes the cached client for an installation.
// Call when the API key is changed or deleted.
func (r *Registry) InvalidateClient(installationID int64) {
	r.mu.Lock()
	delete(r.clients, installationID)
	delete(r.filterDisabled, installationID)
	r.mu.Unlock()
}
