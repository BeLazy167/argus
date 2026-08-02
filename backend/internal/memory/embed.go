package memory

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Embedder turns text into vectors for the memories store. Implementations
// must be safe for concurrent use; the pipeline embeds from multiple
// goroutines (index sinks and search legs run in parallel).
type Embedder interface {
	// Embed returns one vector per input, in input order. A single call is a
	// single upstream request (batched), so callers should pass all texts they
	// have at once — IndexReviewCommentsBatch stays one API call.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	// Model identifies the embedding space. Rows persist it in
	// memories.embedding_model so a model change can gate search and drive
	// re-embedding instead of silently mixing incomparable vectors.
	Model() string
}

// maxEmbedBatch caps inputs per upstream request. OpenAI accepts up to 2048
// inputs, but the RESPONSE is the constraint: 2048 x 1536-dim float JSON is
// ~19-37MB (gate-verified by executed repro). 256 keeps worst-case responses
// ~2-5MB (and ~9MB at 3072-dim BYOK models) — far inside maxEmbedResponse —
// while still amortizing per-request overhead. Larger input sets are chunked
// transparently.
const maxEmbedBatch = 256

// maxEmbedResponse caps how much of an embeddings response is read (guards a
// hostile/broken endpoint, not legitimate traffic — legitimate worst case
// with maxEmbedBatch=256 stays an order of magnitude below).
const maxEmbedResponse = 1 << 27 // 128MB

// flightTimeout bounds a detached singleflight embed call independently of
// any caller deadline (matches the HTTP client timeout plus retry headroom).
const flightTimeout = 60 * time.Second

// embedCacheSize bounds the per-embedder query LRU. Sized for query-path reuse
// (briefing legs and enrich searches repeat identical query strings within a
// run), not for the write path, whose inputs rarely repeat.
const embedCacheSize = 1024

// HTTPEmbedder is an OpenAI-compatible embeddings client (POST
// {base}/embeddings). It reuses the package backoff policy for 429/5xx and
// keeps a small LRU so repeated query texts inside one review don't re-bill.
type HTTPEmbedder struct {
	apiKey  string
	baseURL string
	model   string
	dims    int // requested output dims; emitted only for models honoring `dimensions`
	http    *http.Client
	backoff BackoffPolicy

	mu    sync.Mutex
	cache map[string]*list.Element
	order *list.List // front = most recent; values are *embedCacheEntry

	// flight dedupes concurrent identical batches: parallel briefing/search
	// legs embed the same query text at the same time; only one upstream
	// request is made and the result is shared (copied per caller).
	flight singleflight.Group
}

type embedCacheEntry struct {
	key string
	vec []float32
}

// NewEmbedder builds an HTTPEmbedder. baseURL is the API root without the
// /embeddings suffix (e.g. https://api.voyageai.com/v1, api.openai.com/v1, or
// an Ollama/TEI endpoint for self-hosters). dims is the storage dimensionality
// (memories.embedding); it is sent as the `dimensions` request param only for
// OpenAI text-embedding-3-* models (which Matryoshka-truncate natively) —
// Voyage's default output is already 1024 and custom endpoints must serve the
// storage dims natively (the write path validates vector length).
func NewEmbedder(apiKey, baseURL, model string, dims int) *HTTPEmbedder {
	return &HTTPEmbedder{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		dims:    dims,
		http:    &http.Client{Timeout: 30 * time.Second},
		backoff: DefaultBackoff,
		cache:   make(map[string]*list.Element, embedCacheSize),
		order:   list.New(),
	}
}

// Model implements Embedder.
func (e *HTTPEmbedder) Model() string { return e.model }

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// OpenAI text-embedding-3-* Matryoshka truncation; omitted otherwise
	// (Voyage uses output_dimension with a 1024 default; TEI/Ollama have no
	// such param and must serve the right model).
	Dimensions int `json:"dimensions,omitempty"`
}

type embedResponseItem struct {
	// Pointer: a malformed response OMITTING index must be rejected, not
	// silently accepted as index 0 (zero-value ambiguity).
	Index     *int      `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type embedResponse struct {
	Data []embedResponseItem `json:"data"`
}

// Embed implements Embedder: cache lookup, one chunked upstream call for the
// misses, cache fill, and vectors returned in input order.
func (e *HTTPEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	out := make([][]float32, len(inputs))
	var missIdx []int
	var missTexts []string
	for i, text := range inputs {
		if vec, ok := e.cacheGet(cacheKey(e.model, text)); ok {
			out[i] = vec
			continue
		}
		missIdx = append(missIdx, i)
		missTexts = append(missTexts, text)
	}

	for start := 0; start < len(missTexts); start += maxEmbedBatch {
		end := min(start+maxEmbedBatch, len(missTexts))
		vecs, err := e.embedBatchShared(ctx, missTexts[start:end])
		if err != nil {
			return nil, err
		}
		for j, vec := range vecs {
			i := missIdx[start+j]
			out[i] = vec
			e.cachePut(cacheKey(e.model, inputs[i]), vec)
		}
	}
	return out, nil
}

// embedBatchShared wraps embedBatch in a singleflight keyed by the batch
// content so concurrent identical batches share one upstream call. Shared
// results are deep-copied per caller — Embed's cache-fill and callers both
// touch the slices.
//
// Key construction LENGTH-PREFIXES every text: a bare separator byte would be
// ambiguous (["a\x00b"] vs ["a","b"] would collide — embedded texts are
// PR-derived and may contain any byte), and a collision hands a caller a
// result of the wrong length. Defense in depth: the result length is verified
// before use regardless.
//
// The flight function runs on a context DETACHED from the initiating caller
// (bounded by its own timeout): parallel briefing legs each carry a 5s
// deadline, and the flight owner's cancellation must not poison the shared
// result for joiners with budget remaining. DoChan (not Do) lets each caller
// stop waiting when its OWN ctx expires.
func (e *HTTPEmbedder) embedBatchShared(ctx context.Context, texts []string) ([][]float32, error) {
	h := sha256.New()
	h.Write([]byte(e.model))
	var lenBuf [8]byte
	for _, t := range texts {
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(t)))
		h.Write(lenBuf[:])
		h.Write([]byte(t))
	}
	key := string(h.Sum(nil))

	ch := e.flight.DoChan(key, func() (interface{}, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flightTimeout)
		defer cancel()
		return e.embedBatch(fctx, texts)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		vecs := res.Val.([][]float32)
		if len(vecs) != len(texts) {
			return nil, fmt.Errorf("embed flight: want %d vectors, got %d", len(texts), len(vecs))
		}
		if res.Shared {
			out := make([][]float32, len(vecs))
			for i, vec := range vecs {
				c := make([]float32, len(vec))
				copy(c, vec)
				out[i] = c
			}
			return out, nil
		}
		return vecs, nil
	}
}

// embedBatch performs one POST /embeddings for up to maxEmbedBatch inputs,
// retrying transient statuses under the package backoff policy. The response's
// data items are matched by their index field (the API may reorder them), so a
// missing or duplicate index is a hard error rather than a silently shifted
// vector assigned to the wrong text.
func (e *HTTPEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqPayload := embedRequest{Model: e.model, Input: texts}
	if e.dims > 0 && strings.HasPrefix(e.model, "text-embedding-3") {
		reqPayload.Dimensions = e.dims
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	var vecs [][]float32
	err = retryWithBackoff(ctx, e.backoff, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build embed request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if e.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.apiKey)
		}

		resp, err := e.http.Do(req)
		if err != nil {
			// Transport-level failures (conn reset, DNS blip, timeout) are as
			// transient as a 503 — retry them under the same policy.
			return &retryableError{StatusCode: 0, Body: []byte("transport: " + err.Error())}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponse))
		if err != nil {
			return fmt.Errorf("read embeddings response: %w", err)
		}
		// Unlike Supermemory (which uses 500 for caller errors), embedding
		// providers' 500s are genuinely transient (matches the official
		// OpenAI SDK retry policy: 408/429/5xx).
		if isRetryableStatus(resp.StatusCode) || resp.StatusCode == 500 || resp.StatusCode == 408 {
			return &retryableError{
				StatusCode: resp.StatusCode,
				Body:       respBody,
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			}
		}
		if resp.StatusCode != http.StatusOK {
			snippet := respBody
			if len(snippet) > 256 {
				snippet = snippet[:256]
			}
			return fmt.Errorf("embeddings API status %d: %s", resp.StatusCode, snippet)
		}

		var parsed embedResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return fmt.Errorf("decode embeddings response: %w", err)
		}
		if len(parsed.Data) != len(texts) {
			return fmt.Errorf("embeddings response: want %d vectors, got %d", len(texts), len(parsed.Data))
		}
		for _, item := range parsed.Data {
			if item.Index == nil {
				return fmt.Errorf("embeddings response: item missing index field")
			}
			// cubic P2: an empty embedding must never be cached or persisted.
			if len(item.Embedding) == 0 {
				return fmt.Errorf("embeddings response: empty embedding at index %d", *item.Index)
			}
		}
		sort.Slice(parsed.Data, func(i, j int) bool { return *parsed.Data[i].Index < *parsed.Data[j].Index })
		vecs = make([][]float32, len(texts))
		for i, item := range parsed.Data {
			if *item.Index != i {
				return fmt.Errorf("embeddings response: missing or duplicate index %d", i)
			}
			vec := make([]float32, len(item.Embedding))
			for k, f := range item.Embedding {
				vec[k] = float32(f)
			}
			vecs[i] = vec
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vecs, nil
}

// cacheKey binds a text to its embedding space; the same text under a
// different model must never share a cache slot.
func cacheKey(model, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + text))
	return string(sum[:])
}

// cacheGet returns a COPY of the cached vector: callers may mutate returned
// slices (normalization, truncation) and must not be able to poison the cache
// for later callers. ~6KB per hit at 1536 dims — trivial at query volume.
func (e *HTTPEmbedder) cacheGet(key string) ([]float32, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	el, ok := e.cache[key]
	if !ok {
		return nil, false
	}
	e.order.MoveToFront(el)
	src := el.Value.(*embedCacheEntry).vec
	out := make([]float32, len(src))
	copy(out, src)
	return out, true
}

// cachePut stores a COPY: the caller's returned slice (Embed's out[i]) and
// the cached entry must never alias, or caller mutation corrupts the cache.
func (e *HTTPEmbedder) cachePut(key string, vec []float32) {
	c := make([]float32, len(vec))
	copy(c, vec)
	vec = c
	e.mu.Lock()
	defer e.mu.Unlock()
	if el, ok := e.cache[key]; ok {
		e.order.MoveToFront(el)
		el.Value.(*embedCacheEntry).vec = vec
		return
	}
	e.cache[key] = e.order.PushFront(&embedCacheEntry{key: key, vec: vec})
	if e.order.Len() > embedCacheSize {
		oldest := e.order.Back()
		e.order.Remove(oldest)
		delete(e.cache, oldest.Value.(*embedCacheEntry).key)
	}
}
