package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// embedServer fakes an OpenAI-compatible /embeddings endpoint. Each input text
// maps deterministically to a distinguishable vector; responses are returned
// index-REVERSED to prove the client reorders by the index field.
func embedServer(t *testing.T, calls *atomic.Int64, failFirstWith int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q", got)
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if failFirstWith != 0 && n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(failFirstWith)
			fmt.Fprint(w, `{"error":"transient"}`)
			return
		}
		items := make([]embedResponseItem, 0, len(req.Input))
		for i := len(req.Input) - 1; i >= 0; i-- { // reversed on purpose
			idx := i
			items = append(items, embedResponseItem{
				Index:     &idx,
				Embedding: []float64{float64(len(req.Input[idx])), float64(idx)},
			})
		}
		if err := json.NewEncoder(w).Encode(embedResponse{Data: items}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func testEmbedder(url string) *HTTPEmbedder {
	e := NewEmbedder("test-key", url, "test-model", 0)
	e.backoff = BackoffPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Multiplier: 2}
	return e
}

func TestEmbedBatchOrderAndValues(t *testing.T) {
	var calls atomic.Int64
	srv := embedServer(t, &calls, 0)
	defer srv.Close()

	e := testEmbedder(srv.URL)
	vecs, err := e.Embed(context.Background(), []string{"a", "bb", "ccc"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors", len(vecs))
	}
	// vector = [len(text), index] proves order restoration despite reversed response
	for i, wantLen := range []float32{1, 2, 3} {
		if vecs[i][0] != wantLen || vecs[i][1] != float32(i) {
			t.Fatalf("vec[%d] = %v, want [%v %d]", i, vecs[i], wantLen, i)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (batched)", calls.Load())
	}
}

func TestEmbedCacheHit(t *testing.T) {
	var calls atomic.Int64
	srv := embedServer(t, &calls, 0)
	defer srv.Close()

	e := testEmbedder(srv.URL)
	ctx := context.Background()
	if _, err := e.Embed(ctx, []string{"repeat me", "unique-1"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call: one cached, one new -> exactly one more upstream call whose
	// batch contains only the miss.
	vecs, err := e.Embed(ctx, []string{"repeat me", "unique-2"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls.Load())
	}
	if vecs[0][0] != float32(len("repeat me")) {
		t.Fatalf("cached vector wrong: %v", vecs[0])
	}
	// Fully cached call: no new upstream request.
	if _, err := e.Embed(ctx, []string{"repeat me"}); err != nil {
		t.Fatalf("third: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cache miss on fully-cached call: %d calls", calls.Load())
	}
}

func TestEmbedRetriesTransient(t *testing.T) {
	var calls atomic.Int64
	srv := embedServer(t, &calls, http.StatusTooManyRequests)
	defer srv.Close()

	e := testEmbedder(srv.URL)
	if _, err := e.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed after retry: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (429 then 200)", calls.Load())
	}
}

func TestEmbedTerminalErrorNoRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad model"}`)
	}))
	defer srv.Close()

	e := testEmbedder(srv.URL)
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("want error on 400")
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
}

// fakeEmbedResolver implements EmbedKeyResolver for registry tests.
type fakeEmbedResolver struct {
	key, base, model string
	found            bool
	err              error
	calls            int
}

func (f *fakeEmbedResolver) ResolveEmbeddingsKey(context.Context, int64) (string, string, string, bool, error) {
	f.calls++
	return f.key, f.base, f.model, f.found, f.err
}

func TestEmbedderRegistryResolution(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	ctx := context.Background()

	t.Run("BYOK overrides platform", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{key: "byok", base: "https://byok.example/v1", found: true},
			PlatformEmbeddings{APIKey: "platform"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("GetEmbedder: %v %v", e, err)
		}
		he := e.(*HTTPEmbedder)
		if he.apiKey != "byok" || he.baseURL != "https://byok.example/v1" {
			t.Fatalf("resolved %q @ %q", he.apiKey, he.baseURL)
		}
	})

	t.Run("platform fallback", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{}, PlatformEmbeddings{APIKey: "platform"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("GetEmbedder: %v %v", e, err)
		}
		if e.(*HTTPEmbedder).apiKey != "platform" {
			t.Fatal("platform key not used")
		}
		if e.Model() != "voyage-4" {
			t.Fatalf("default model = %q", e.Model())
		}
	})

	t.Run("no key anywhere: embeddings off", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{}, PlatformEmbeddings{}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if e != nil {
			t.Fatal("want nil embedder when no key configured")
		}
	})

	t.Run("keyless custom base proceeds (local endpoint)", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{}, PlatformEmbeddings{BaseURL: "http://localhost:11434/v1"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("keyless local endpoint should produce an embedder: %v %v", e, err)
		}
	})

	t.Run("keyless BYOK custom endpoint proceeds", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{key: "", base: "http://tei.corp:8080/v1", model: "bge-m3", found: true},
			PlatformEmbeddings{APIKey: "platform"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("keyless BYOK custom endpoint should produce an embedder: %v %v", e, err)
		}
		if e.Model() != "bge-m3" {
			t.Fatalf("model = %q", e.Model())
		}
	})

	t.Run("resolver error degrades to platform", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{err: fmt.Errorf("db down")},
			PlatformEmbeddings{APIKey: "platform"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("GetEmbedder: %v %v", e, err)
		}
		if e.(*HTTPEmbedder).apiKey != "platform" {
			t.Fatal("did not fall back to platform on resolver error")
		}
	})

	t.Run("BYOK model override respected", func(t *testing.T) {
		r := NewEmbedderRegistry(&fakeEmbedResolver{key: "k", base: "http://tei.local/v1", model: "bge-m3", found: true},
			PlatformEmbeddings{APIKey: "platform"}, logger)
		e, err := r.GetEmbedder(ctx, 1)
		if err != nil || e == nil {
			t.Fatalf("GetEmbedder: %v %v", e, err)
		}
		if e.Model() != "bge-m3" {
			t.Fatalf("model = %q, want BYOK override", e.Model())
		}
	})

	t.Run("embeddings-off result is cached (no repeat resolver hits)", func(t *testing.T) {
		res := &fakeEmbedResolver{}
		r := NewEmbedderRegistry(res, PlatformEmbeddings{}, logger)
		for range 3 {
			if e, err := r.GetEmbedder(ctx, 5); err != nil || e != nil {
				t.Fatalf("want cached nil embedder, got %v %v", e, err)
			}
		}
		if res.calls != 1 {
			t.Fatalf("resolver calls = %d, want 1 (negative result cached)", res.calls)
		}
	})

	t.Run("cache + invalidate", func(t *testing.T) {
		res := &fakeEmbedResolver{key: "k1", found: true}
		r := NewEmbedderRegistry(res, PlatformEmbeddings{}, logger)
		e1, _ := r.GetEmbedder(ctx, 7)
		res.key = "k2"
		e2, _ := r.GetEmbedder(ctx, 7)
		if e1 != e2 {
			t.Fatal("expected cached embedder before invalidation")
		}
		r.Invalidate(7)
		e3, _ := r.GetEmbedder(ctx, 7)
		if e3.(*HTTPEmbedder).apiKey != "k2" {
			t.Fatal("invalidate did not refresh the embedder")
		}
	})
}

func TestEmbedChunkingRemap(t *testing.T) {
	var calls atomic.Int64
	srv := embedServer(t, &calls, 0)
	defer srv.Close()

	e := testEmbedder(srv.URL)
	n := maxEmbedBatch*2 + 50 // 3 chunks
	inputs := make([]string, n)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("text-%04d-%s", i, string(rune('a'+i%26)))
	}
	vecs, err := e.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("upstream calls = %d, want 3 chunks", calls.Load())
	}
	// remap correctness: vec[0] = len(text) for every position
	for i, v := range vecs {
		if v[0] != float32(len(inputs[i])) {
			t.Fatalf("chunk remap wrong at %d: %v vs len %d", i, v, len(inputs[i]))
		}
	}
}

func TestEmbedRejectsMalformedResponses(t *testing.T) {
	cases := map[string]string{
		"duplicate index": `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`,
		"missing index":   `{"data":[{"embedding":[1]},{"index":1,"embedding":[2]}]}`,
		"empty embedding": `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[2]}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, payload)
			}))
			defer srv.Close()
			e := testEmbedder(srv.URL)
			if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
				t.Fatalf("%s: want error, got nil", name)
			}
		})
	}
}

func TestEmbedCacheNotAliased(t *testing.T) {
	var calls atomic.Int64
	srv := embedServer(t, &calls, 0)
	defer srv.Close()

	e := testEmbedder(srv.URL)
	ctx := context.Background()
	first, err := e.Embed(ctx, []string{"immutable"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	want := first[0][0]
	first[0][0] = -999 // caller mutates the returned slice
	second, err := e.Embed(ctx, []string{"immutable"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second[0][0] != want {
		t.Fatalf("cache poisoned by caller mutation: got %v, want %v", second[0][0], want)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (second call cached)", calls.Load())
	}
}

func TestEmbedDimensionsParamOnlyForOpenAIFamily(t *testing.T) {
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		idx := 0
		if err := json.NewEncoder(w).Encode(embedResponse{Data: []embedResponseItem{{Index: &idx, Embedding: []float64{1}}}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	oa := NewEmbedder("k", srv.URL, "text-embedding-3-small", 1024)
	if _, err := oa.Embed(ctx, []string{"x"}); err != nil {
		t.Fatalf("openai-family embed: %v", err)
	}
	if !bytes.Contains(lastBody, []byte(`"dimensions":1024`)) {
		t.Fatalf("dimensions param missing for OpenAI family: %s", lastBody)
	}

	vy := NewEmbedder("k", srv.URL, "voyage-4", 1024)
	if _, err := vy.Embed(ctx, []string{"y"}); err != nil {
		t.Fatalf("voyage embed: %v", err)
	}
	if bytes.Contains(lastBody, []byte("dimensions")) {
		t.Fatalf("dimensions param must be omitted for non-OpenAI models: %s", lastBody)
	}
}

// TestEmbedFlightKeyNotAmbiguous reproduces the gate's blocking finding: the
// batches ["a\x00b"] and ["a","b"] must NOT share a singleflight result (a
// bare-separator key would collide; texts are PR-derived and may contain any
// byte). Each caller must get vectors matching its OWN batch shape.
func TestEmbedFlightKeyNotAmbiguous(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond) // hold the flight open so the second call would join a colliding key
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		items := make([]embedResponseItem, len(req.Input))
		for i := range req.Input {
			idx := i
			items[i] = embedResponseItem{Index: &idx, Embedding: []float64{float64(len(req.Input[i]))}}
		}
		if err := json.NewEncoder(w).Encode(embedResponse{Data: items}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	e := testEmbedder(srv.URL)
	ctx := context.Background()
	type result struct {
		vecs [][]float32
		err  error
	}
	r1 := make(chan result, 1)
	r2 := make(chan result, 1)
	go func() {
		v, err := e.Embed(ctx, []string{"a\x00b"})
		r1 <- result{v, err}
	}()
	time.Sleep(30 * time.Millisecond)
	go func() {
		v, err := e.Embed(ctx, []string{"a", "b"})
		r2 <- result{v, err}
	}()
	res1, res2 := <-r1, <-r2
	if res1.err != nil || res2.err != nil {
		t.Fatalf("errs: %v %v", res1.err, res2.err)
	}
	if len(res1.vecs) != 1 || res1.vecs[0][0] != 3 {
		t.Fatalf("single-text batch corrupted: %v", res1.vecs)
	}
	if len(res2.vecs) != 2 || res2.vecs[0][0] != 1 || res2.vecs[1][0] != 1 {
		t.Fatalf("two-text batch corrupted: %v", res2.vecs)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (distinct batches must not share)", calls.Load())
	}
}

// TestEmbedFlightSurvivesOwnerCancel: the flight-initiating caller canceling
// must not poison the shared result for a joiner with budget remaining.
func TestEmbedFlightSurvivesOwnerCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		idx := 0
		if err := json.NewEncoder(w).Encode(embedResponse{Data: []embedResponseItem{{Index: &idx, Embedding: []float64{7}}}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	e := testEmbedder(srv.URL)
	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	ownerDone := make(chan error, 1)
	go func() {
		_, err := e.Embed(ownerCtx, []string{"shared-query"})
		ownerDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	joinerDone := make(chan error, 1)
	var joinerVecs [][]float32
	go func() {
		v, err := e.Embed(context.Background(), []string{"shared-query"})
		joinerVecs = v
		joinerDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	ownerCancel() // owner gives up
	if err := <-ownerDone; err == nil {
		t.Fatal("owner should observe its own cancellation")
	}
	close(release) // upstream completes
	if err := <-joinerDone; err != nil {
		t.Fatalf("joiner poisoned by owner cancellation: %v", err)
	}
	if len(joinerVecs) != 1 || joinerVecs[0][0] != 7 {
		t.Fatalf("joiner got wrong result: %v", joinerVecs)
	}
}

func TestHTTPEmbedderSpaceIDIdentifiesEndpointModelAndDimensions(t *testing.T) {
	base := NewEmbedder("secret-a", "HTTPS://API.Example.com:443/v1/", "model-a", 1024)
	same := NewEmbedder("secret-b", "https://api.example.com/v1", "model-a", 1024)
	if base.SpaceID() != same.SpaceID() {
		t.Fatal("API-key/default-port rotation changed the embedding space")
	}
	privateURL := NewEmbedder("key", "https://user:password@api.example.com/v1?token=secret#fragment", "model-a", 1024)
	if privateURL.SpaceID() != same.SpaceID() {
		t.Fatal("userinfo/query/fragment changed the coordinate-space identity")
	}

	rotations := []*HTTPEmbedder{
		NewEmbedder("secret-a", "https://other.example.com/v1", "model-a", 1024),
		NewEmbedder("secret-a", "https://api.example.com/v1", "model-b", 1024),
		NewEmbedder("secret-a", "https://api.example.com/v1", "model-a", 1536),
	}
	for i, rotated := range rotations {
		if base.SpaceID() == rotated.SpaceID() {
			t.Errorf("rotation %d did not change embedding-space identity", i)
		}
	}
	if strings.Contains(base.SpaceID(), "secret") || strings.Contains(base.SpaceID(), "example.com") {
		t.Fatalf("space id leaks endpoint or credentials: %q", base.SpaceID())
	}
}
