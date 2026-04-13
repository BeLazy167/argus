package memory

import (
	"context"
	"log/slog"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/crypto"
)

// KeyResolver loads encrypted Supermemory API keys for installations.
type KeyResolver interface {
	GetSupermemoryKey(ctx context.Context, installationID int64) (string, error)
}

// Registry provides per-installation memory.Client instances with caching.
type Registry struct {
	mu               sync.RWMutex
	clients          map[int64]*Client
	filterConfigured map[int64]bool
	resolver         KeyResolver
	logger           *slog.Logger
}

func NewRegistry(resolver KeyResolver, logger *slog.Logger) *Registry {
	return &Registry{
		clients:          make(map[int64]*Client),
		filterConfigured: make(map[int64]bool),
		resolver:         resolver,
		logger:           logger,
	}
}

// GetClient returns a cached Client for the installation, or nil if no key configured.
func (r *Registry) GetClient(ctx context.Context, installationID int64) *Client {
	r.mu.RLock()
	if c, ok := r.clients[installationID]; ok {
		r.mu.RUnlock()
		return c
	}
	r.mu.RUnlock()

	// Load key from DB
	enc, err := r.resolver.GetSupermemoryKey(ctx, installationID)
	if err != nil {
		r.logger.Warn("failed to load supermemory key", "error", err, "installation_id", installationID)
		return nil
	}
	if enc == "" {
		return nil
	}

	key, err := crypto.Decrypt(enc)
	if err != nil {
		r.logger.Error("failed to decrypt supermemory key", "error", err, "installation_id", installationID)
		return nil
	}

	client := NewClient(key)

	r.mu.Lock()
	r.clients[installationID] = client
	r.mu.Unlock()

	return client
}

// GetIndexer returns a new Indexer for the installation, or nil if no key configured.
// Also runs ConfigureFilterPrompt on first use per-org.
func (r *Registry) GetIndexer(ctx context.Context, installationID int64) *Indexer {
	client := r.GetClient(ctx, installationID)
	if client == nil {
		return nil
	}
	indexer := NewIndexer(client, r.logger)

	r.mu.RLock()
	configured := r.filterConfigured[installationID]
	r.mu.RUnlock()

	if !configured {
		indexer.ConfigureFilterPrompt(ctx)
		r.mu.Lock()
		r.filterConfigured[installationID] = true
		r.mu.Unlock()
	}

	return indexer
}

// InvalidateClient removes the cached client for an installation.
// Call when the API key is changed or deleted.
func (r *Registry) InvalidateClient(installationID int64) {
	r.mu.Lock()
	delete(r.clients, installationID)
	delete(r.filterConfigured, installationID)
	r.mu.Unlock()
}
