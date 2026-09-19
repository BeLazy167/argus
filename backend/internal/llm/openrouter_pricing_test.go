package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestPricing(t *testing.T, body string, status int) *OpenRouterPricing {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenRouterPricing()
	p.url = srv.URL
	// Synchronous fetch keeps matching assertions deterministic — production
	// code goes through Warm/maybeRefresh instead.
	p.Refresh(context.Background())
	return p
}

const testCatalog = `{"data":[
	{"id":"openai/gpt-5.6-terra","pricing":{"prompt":"0.0000025","completion":"0.00001"}},
	{"id":"anthropic/claude-x","pricing":{"prompt":"0.000003","completion":"0.000015"}},
	{"id":"bad/missing-pricing","pricing":{"prompt":"","completion":""}},
	{"id":"evil/negative","pricing":{"prompt":"-1","completion":"-2"}},
	{"id":"evil/nan","pricing":{"prompt":"NaN","completion":"0.00001"}},
	{"id":"evil/inf","pricing":{"prompt":"Inf","completion":"0.00001"}}
]}`

func TestOpenRouterPricing_ExactMatch(t *testing.T) {
	p := newTestPricing(t, testCatalog, http.StatusOK)
	in, out, ok := p.Lookup("openai/gpt-5.6-terra")
	if !ok || in != 2.5 || out != 10.0 {
		t.Fatalf("Lookup = %v,%v,%v want 2.5,10,true", in, out, ok)
	}
}

func TestOpenRouterPricing_UniqueSuffixMatch(t *testing.T) {
	p := newTestPricing(t, testCatalog, http.StatusOK)
	in, out, ok := p.Lookup("claude-x")
	if !ok || in != 3.0 || out != 15.0 {
		t.Fatalf("Lookup = %v,%v,%v want 3.0,15.0,true", in, out, ok)
	}
}

func TestOpenRouterPricing_AmbiguousSuffixNotFound(t *testing.T) {
	body := `{"data":[
		{"id":"a/dup","pricing":{"prompt":"0.000001","completion":"0.000001"}},
		{"id":"b/dup","pricing":{"prompt":"0.000002","completion":"0.000002"}}
	]}`
	p := newTestPricing(t, body, http.StatusOK)
	if _, _, ok := p.Lookup("dup"); ok {
		t.Fatal("ambiguous suffix must not guess a price")
	}
}

func TestOpenRouterPricing_PrefixMatch(t *testing.T) {
	p := newTestPricing(t, testCatalog, http.StatusOK)
	in, out, ok := p.Lookup("openai/gpt-5.6-terra-2026-01-01")
	if !ok || in != 2.5 || out != 10.0 {
		t.Fatalf("prefix Lookup = %v,%v,%v want 2.5,10.0,true", in, out, ok)
	}
}

func TestOpenRouterPricing_NotFound(t *testing.T) {
	p := newTestPricing(t, testCatalog, http.StatusOK)
	if _, _, ok := p.Lookup("no/such-model"); ok {
		t.Fatal("unknown model must not match")
	}
}

func TestOpenRouterPricing_BadRowsSkipped(t *testing.T) {
	p := newTestPricing(t, testCatalog, http.StatusOK)
	for _, id := range []string{"bad/missing-pricing", "evil/negative", "evil/nan", "evil/inf"} {
		if _, _, ok := p.Lookup(id); ok {
			t.Fatalf("row %s must be skipped", id)
		}
	}
}

func TestOpenRouterPricing_LookupNeverBlocksOnFetch(t *testing.T) {
	// A lookup with a cold cache returns not-found immediately and kicks an
	// async refresh — the LLM cost path never stalls on the fetch.
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release // hang until the test lets the fetch complete
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, testCatalog)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(unblock) // runs before srv.Close (LIFO) so the handler never hangs
	p := NewOpenRouterPricing()
	p.url = srv.URL

	start := time.Now()
	if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); ok {
		t.Fatal("cold cache must return not-found, not block on the fetch")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Lookup blocked %v on the fetch", elapsed)
	}
	unblock()
	// The kicked fetch populates the cache for the next lookup.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("async refresh never populated the cache (hits=%d)", hits.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOpenRouterPricing_FailedRefreshThrottled(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenRouterPricing()
	p.url = srv.URL

	p.Refresh(context.Background()) // sync attempt — fails, hits=1
	if _, _, ok := p.Lookup("x"); ok {
		t.Fatal("failed fetch must return not-found")
	}
	// Lookup's stale catalog spawns one async retry; wait for it to land.
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected the async retry to fire once, hits=%d", hits.Load())
	}
	// Further lookups inside the retry interval spawn nothing.
	for i := 0; i < 3; i++ {
		p.Lookup("x")
	}
	time.Sleep(20 * time.Millisecond)
	if got := hits.Load(); got != 2 {
		t.Fatalf("retries must throttle to the retry interval, hits=%d", got)
	}
}

func TestOpenRouterPricing_StaleCacheServedOnFailure(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, testCatalog)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenRouterPricing()
	p.url = srv.URL
	p.Refresh(context.Background())

	if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); !ok {
		t.Fatal("initial lookup should hit the fresh catalog")
	}
	fail.Store(true)
	p.mu.Lock()
	p.fetchedAt = p.fetchedAt.Add(-openRouterPricingTTL - 1) // force stale
	p.lastAttempt = time.Now().Add(-openRouterRetryInterval - 1)
	p.mu.Unlock()
	in, _, ok := p.Lookup("openai/gpt-5.6-terra")
	if !ok || in != 2.5 {
		t.Fatalf("stale cache should still serve, got %v,%v", in, ok)
	}
}
