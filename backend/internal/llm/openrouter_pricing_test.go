package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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
	return p
}

const testCatalog = `{"data":[
	{"id":"openai/gpt-5.6-terra","pricing":{"prompt":"0.0000025","completion":"0.00001"}},
	{"id":"anthropic/claude-x","pricing":{"prompt":"0.000003","completion":"0.000015"}},
	{"id":"bad/missing-pricing","pricing":{"prompt":"","completion":""}},
	{"id":"evil/negative","pricing":{"prompt":"-1","completion":"-2"}}
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
	in, _, ok := p.Lookup("claude-x")
	if !ok || in != 3.0 {
		t.Fatalf("Lookup = %v,%v want 3.0,true", in, ok)
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
	in, _, ok := p.Lookup("openai/gpt-5.6-terra-2026-01-01")
	if !ok || in != 2.5 {
		t.Fatalf("prefix Lookup = %v,%v want 2.5,true", in, ok)
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
	if _, _, ok := p.Lookup("bad/missing-pricing"); ok {
		t.Fatal("empty pricing row must be skipped")
	}
	if _, _, ok := p.Lookup("evil/negative"); ok {
		t.Fatal("negative pricing row must be skipped")
	}
}

func TestOpenRouterPricing_FetchFailureNotFound(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenRouterPricing()
	p.url = srv.URL
	if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); ok {
		t.Fatal("failed fetch must return not-found")
	}
	// A down endpoint must not be retried per call — the failed attempt
	// stamps 'loaded' and throttles retries to once per TTL window.
	if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); ok {
		t.Fatal("failed fetch must return not-found")
	}
	if hits != 1 {
		t.Fatalf("expected 1 fetch attempt within TTL window, got %d", hits)
	}
}

func TestOpenRouterPricing_StaleCacheServedOnFailure(t *testing.T) {
	var fail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, testCatalog)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenRouterPricing()
	p.url = srv.URL

	if _, _, ok := p.Lookup("openai/gpt-5.6-terra"); !ok {
		t.Fatal("initial lookup should hit the fresh catalog")
	}
	fail = true
	p.loaded = p.loaded.Add(-openRouterPricingTTL - 1) // force stale
	in, _, ok := p.Lookup("openai/gpt-5.6-terra")
	if !ok || in != 2.5 {
		t.Fatalf("stale cache should still serve, got %v,%v", in, ok)
	}
}
