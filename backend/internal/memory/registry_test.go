package memory

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/crypto"
)

type fakeResolver struct {
	enc   string
	calls int32
}

func (f *fakeResolver) GetSupermemoryKey(_ context.Context, _ int64) (string, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.enc, nil
}

// TestRegistry_GetClient_SingleFlight pins idx 11: concurrent first-callers for
// the same installation must share ONE *Client (one rate limiter), not each
// build their own — which would multiply the per-install QPS cap. Run under
// -race to catch the check-then-act data race the fix closes.
func TestRegistry_GetClient_SingleFlight(t *testing.T) {
	if err := crypto.Init(hex.EncodeToString(make([]byte, 32))); err != nil {
		t.Fatalf("crypto.Init: %v", err)
	}
	enc, err := crypto.Encrypt("sm-api-key")
	if err != nil {
		t.Fatalf("crypto.Encrypt: %v", err)
	}
	res := &fakeResolver{enc: enc}
	reg := NewRegistry(res, slog.New(slog.NewTextHandler(io.Discard, nil)))

	const n = 32
	clients := make([]*Client, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			clients[i] = reg.GetClient(context.Background(), 42)
		}(i)
	}
	wg.Wait()

	first := clients[0]
	if first == nil {
		t.Fatal("GetClient returned nil")
	}
	for i, c := range clients {
		if c != first {
			t.Fatalf("client[%d] = %p, want single shared client %p (duplicate limiters)", i, c, first)
		}
	}
	// Steady-state cache hit returns the same instance.
	if got := reg.GetClient(context.Background(), 42); got != first {
		t.Errorf("post-warm GetClient = %p, want cached %p", got, first)
	}
}

// TestRegistry_GetClient_NoKey covers the empty-key path: a nil client, no panic.
func TestRegistry_GetClient_NoKey(t *testing.T) {
	res := &fakeResolver{enc: ""}
	reg := NewRegistry(res, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c := reg.GetClient(context.Background(), 7); c != nil {
		t.Errorf("GetClient with empty key = %p, want nil", c)
	}
}
