package memory

import (
	"context"
	"testing"
	"time"
)

// blockingEmbedResolver holds a resolve open so an invalidation can be made to
// land in the window where GetEmbedder has released its lock.
type blockingEmbedResolver struct {
	entered chan struct{}
	release chan struct{}
	key     string
	model   string
}

// Values are captured on entry, before the test changes them: the scenario is
// a resolve that already read the OLD key and is slow to return, not one that
// observes the rotation.
func (b *blockingEmbedResolver) ResolveEmbeddingsKey(context.Context, int64) (string, string, string, bool, error) {
	key, model := b.key, b.model
	b.entered <- struct{}{}
	<-b.release
	return key, "", model, true, nil
}

// TestEmbedderInvalidateDuringResolve is the sibling of
// TestBackendCacheInvalidateDuringResolve, which was covering the identical
// guard in backendCache while this one had none: neutering the generation
// check in GetEmbedder left the whole package green.
//
// The runtime failure it guards: a caller misses the cache and blocks inside
// ResolveEmbeddingsKey; meanwhile a provider-key PUT commits a rotated
// "embeddings" key and InvalidateEmbedder fires. Without the guard the
// in-flight resolve re-caches the OLD key and model for the full TTL, so every
// memory written in that window is embedded with a revoked key and stamped
// with the superseded embedding space. Reads gate the score on
// embedding_space, so those rows are invisible — and carrying a foreign space stamp, the
// repair sweep must select them as well as NULL vectors.
func TestEmbedderInvalidateDuringResolve(t *testing.T) {
	r := &blockingEmbedResolver{
		// Buffered: the post-rotation re-read resolves again, and its send
		// must not block on a receiver the test no longer has.
		entered: make(chan struct{}, 4),
		release: make(chan struct{}),
		key:     "old-key",
		model:   "old-model",
	}
	reg := NewEmbedderRegistry(r, PlatformEmbeddings{APIKey: "platform"}, discardLogger())

	done := make(chan Embedder, 1)
	go func() {
		e, _ := reg.GetEmbedder(context.Background(), 7)
		done <- e
	}()

	<-r.entered       // resolve is past the lock, holding the old key
	reg.Invalidate(7) // the key rotation commits mid-flight
	r.key, r.model = "new-key", "new-model"
	close(r.release)

	if e := <-done; e == nil {
		t.Fatal("in-flight resolve produced no embedder")
	}
	// The stale answer must NOT have been cached: the next caller re-resolves
	// and sees the rotated key.
	e, err := reg.GetEmbedder(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetEmbedder: %v", err)
	}
	if e == nil {
		t.Fatal("post-rotation GetEmbedder returned no embedder")
	}
	if got := e.Model(); got != "new-model" {
		t.Errorf("embedder model = %q after a rotation raced an in-flight resolve; the invalidation was swallowed", got)
	}
}

// TestEmbedderErrorResolveCachesBriefly: a failed resolve substitutes the
// PLATFORM key and model. Caching that for the full TTL stamps up to five
// minutes of rows with the platform model while reads gate the score on the
// install's real BYOK model — those rows score 0 forever, and because their
// embedding is NOT NULL; the repair sweep must select its foreign space
// stamp as well as NULL vectors. Asserted on the stored expiry, since the window is real time.
func TestEmbedderErrorResolveCachesBriefly(t *testing.T) {
	reg := NewEmbedderRegistry(&fakeEmbedResolver{err: context.DeadlineExceeded},
		PlatformEmbeddings{APIKey: "platform"}, discardLogger())
	if _, err := reg.GetEmbedder(context.Background(), 7); err != nil {
		t.Fatalf("GetEmbedder: %v", err)
	}
	reg.mu.Lock()
	entry, ok := reg.cache[7]
	reg.mu.Unlock()
	if !ok {
		t.Fatal("failed resolve cached nothing; every call would re-hit the resolver")
	}
	// Two assertions, because either alone leaves a hole. Bounding against
	// errorEmbedderTTL (not the flag cache's constant, which governs an
	// independent policy) proves the error path uses the error constant rather
	// than embedderCacheTTL — but that bound is RELATIVE, so raising the
	// constant moves the bar with it. Pinning the policy itself is what catches
	// a change that lifts it to the success TTL and silently restores the
	// five-minute stamping window.
	if errorEmbedderTTL >= embedderCacheTTL {
		t.Errorf("errorEmbedderTTL (%v) is not shorter than embedderCacheTTL (%v); a failed resolve is now published for as long as a successful one",
			errorEmbedderTTL, embedderCacheTTL)
	}
	if d := time.Until(entry.expiresAt); d > errorEmbedderTTL {
		t.Errorf("failed resolve cached for %v, want at most %v — the platform fallback must not be published for the full TTL", d, errorEmbedderTTL)
	}
}

// TestEmbedderDoesNotCacheCancelledCaller: same reasoning as the backend
// cache — one aborted request must not decide which key every other caller
// embeds with.
func TestEmbedderDoesNotCacheCancelledCaller(t *testing.T) {
	res := &fakeEmbedResolver{key: "byok", model: "byok-model", found: true}
	reg := NewEmbedderRegistry(res, PlatformEmbeddings{APIKey: "platform"}, discardLogger())

	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.GetEmbedder(dead, 7); err != nil {
		t.Fatalf("GetEmbedder: %v", err)
	}
	reg.mu.Lock()
	_, cached := reg.cache[7]
	reg.mu.Unlock()
	if cached {
		t.Error("a cancelled caller's resolve was cached for everyone else")
	}
}
