package memory

import (
	"context"
	"testing"
)

// An unwired registry must answer false, not panic, because MCP tool handlers
// call this before deciding how to describe an empty result.
func TestEmbedderAvailableUnwiredRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry(discardLogger())
	if r.EmbedderAvailable(context.Background(), 1) {
		t.Fatal("unwired registry reported an available embedder")
	}
}

// WarmVectorProbe on an unwired registry is a no-op; it must never touch a
// nil pool.
func TestWarmVectorProbeUnwiredRegistryIsNoop(t *testing.T) {
	t.Parallel()
	NewRegistry(discardLogger()).WarmVectorProbe(context.Background())
}

func TestEmbedderAvailableFollowsEmbedderRegistry(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	// No platform key and no BYOK row: embeddings are off for this tenant.
	off := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{Dimensions: StorageDimensions}, discardLogger()))
	if off.EmbedderAvailable(ctx, install) {
		t.Fatal("registry with no keys reported embeddings available")
	}

	// A platform key makes an embedder resolvable for every installation.
	on := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{APIKey: "k", BaseURL: "https://example.invalid", Model: "m", Dimensions: StorageDimensions}, discardLogger()))
	if !on.EmbedderAvailable(ctx, install) {
		t.Fatal("registry with a platform key reported embeddings unavailable")
	}
}

type noKeysResolver struct{}

func (noKeysResolver) ResolveEmbeddingsKey(context.Context, int64) (string, string, string, bool, error) {
	return "", "", "", false, nil
}
