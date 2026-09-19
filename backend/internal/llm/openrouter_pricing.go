package llm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// openRouterModelsURL is OpenRouter's public model catalog — no auth.
	openRouterModelsURL = "https://openrouter.ai/api/v1/models"
	// openRouterPricingTTL bounds how long a successful fetch is reused.
	// Prices drift slowly; a multi-hour cache keeps HTTP off the call path.
	openRouterPricingTTL = 6 * time.Hour
	// openRouterRetryInterval bounds failed-attempt retries — a down endpoint
	// retries in minutes, never inside an LLM call's critical path.
	openRouterRetryInterval = 5 * time.Minute
	// openRouterCatalogMaxBytes bounds the catalog read (~3MB today).
	openRouterCatalogMaxBytes = 16 << 20
	// openRouterFetchTimeout bounds the catalog fetch.
	openRouterFetchTimeout = 15 * time.Second
)

// OpenRouterPricing resolves per-1M-token pricing from OpenRouter's public
// model catalog — the fallback when model_pricing has no manual row. The
// catalog is fetched in the background (Warm at startup, then on TTL
// expiry); Lookup never blocks the cost path on a fetch and serves the last
// good copy — or not-found — while a refresh runs.
type OpenRouterPricing struct {
	mu          sync.RWMutex
	prices      map[string][2]float64 // model id → [inputPer1M, outputPer1M]
	fetchedAt   time.Time             // last successful fetch
	lastAttempt time.Time             // throttles refresh spawns
	inFlight    atomic.Bool           // one fetch at a time
	client      *http.Client
	url         string // overridable in tests
}

// NewOpenRouterPricing returns an empty catalog resolver. Call Warm at
// startup so the first Lookup has data; Lookup also self-heals on staleness.
func NewOpenRouterPricing() *OpenRouterPricing {
	return &OpenRouterPricing{
		client: &http.Client{Timeout: openRouterFetchTimeout},
		url:    openRouterModelsURL,
	}
}

type openRouterCatalogEntry struct {
	ID      string `json:"id"`
	Pricing struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// Warm kicks an asynchronous catalog fetch — startup warming only; the fetch
// itself is throttled and single-flighted by maybeRefresh.
func (o *OpenRouterPricing) Warm(ctx context.Context) { o.maybeRefresh(ctx) }

// Refresh fetches the catalog synchronously and swaps the price map on
// success. A failure leaves the stale map in place. Callers needing
// single-flight/throttling use maybeRefresh instead.
func (o *OpenRouterPricing) Refresh(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url, nil)
	if err != nil {
		return
	}
	resp, err := o.client.Do(req)
	if err != nil {
		slog.Debug("openrouter pricing fetch failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Debug("openrouter pricing fetch non-200", "status", resp.StatusCode)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, openRouterCatalogMaxBytes+1))
	if err != nil || len(body) > openRouterCatalogMaxBytes {
		slog.Debug("openrouter pricing read failed", "error", err, "bytes", len(body))
		return
	}
	var catalog struct {
		Data []openRouterCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		slog.Debug("openrouter pricing decode failed", "error", err)
		return
	}
	prices := make(map[string][2]float64, len(catalog.Data))
	for _, m := range catalog.Data {
		in, err1 := strconv.ParseFloat(m.Pricing.Prompt, 64)
		out, err2 := strconv.ParseFloat(m.Pricing.Completion, 64)
		// ParseFloat accepts "NaN"/"Inf" without error — non-finite or
		// negative prices would poison cost totals, so skip the row.
		if err1 != nil || err2 != nil || in < 0 || out < 0 ||
			math.IsNaN(in) || math.IsInf(in, 0) || math.IsNaN(out) || math.IsInf(out, 0) {
			continue
		}
		prices[m.ID] = [2]float64{in * 1_000_000, out * 1_000_000}
	}
	if len(prices) == 0 {
		return
	}
	o.mu.Lock()
	o.prices = prices
	o.fetchedAt = time.Now()
	o.mu.Unlock()
}

// catalog returns the current price map, spawning an async refresh when the
// last success is older than the TTL (or nothing has loaded yet).
func (o *OpenRouterPricing) catalog(ctx context.Context) map[string][2]float64 {
	o.mu.RLock()
	p, fresh := o.prices, o.fetchedAt
	o.mu.RUnlock()
	if time.Since(fresh) > openRouterPricingTTL {
		o.maybeRefresh(ctx)
	}
	return p
}

// maybeRefresh spawns a background fetch: one in flight, at most one spawn
// per openRouterRetryInterval. The spawn interval is intentionally shorter
// than the TTL so a transient failure retries in minutes while a healthy
// cache only refreshes on staleness.
func (o *OpenRouterPricing) maybeRefresh(ctx context.Context) {
	o.mu.Lock()
	if time.Since(o.lastAttempt) < openRouterRetryInterval {
		o.mu.Unlock()
		return
	}
	o.lastAttempt = time.Now()
	o.mu.Unlock()
	if !o.inFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer o.inFlight.Store(false)
		o.Refresh(ctx)
	}()
}

// Lookup resolves model pricing against the OpenRouter catalog. Order:
// exact id match → unique "/"-suffix match (bare provider model names like
// "gpt-5.6-terra" resolve to "openai/gpt-5.6-terra" when unambiguous) →
// prefix match with the same next-char rule the DB cache uses. Ambiguous
// suffix matches return not-found — never guess a price.
func (o *OpenRouterPricing) Lookup(model string) (float64, float64, bool) {
	return o.LookupCtx(context.Background(), model)
}

// LookupCtx is Lookup with a caller context: a spawned refresh inherits it,
// so lookups made under the app ctx cancel on shutdown.
func (o *OpenRouterPricing) LookupCtx(ctx context.Context, model string) (float64, float64, bool) {
	prices := o.catalog(ctx)
	if len(prices) == 0 {
		return 0, 0, false
	}
	if p, ok := prices[model]; ok {
		return p[0], p[1], true
	}
	var matches []string
	suffix := "/" + model
	for id := range prices {
		if strings.HasSuffix(id, suffix) {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		p := prices[matches[0]]
		return p[0], p[1], true
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		slog.Debug("openrouter pricing ambiguous suffix match",
			"model", model, "candidates", matches)
		return 0, 0, false
	}
	// Prefix match — catalog ids sorted longest-first so specific wins.
	ids := make([]string, 0, len(prices))
	for id := range prices {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		if len(model) > len(id) && model[:len(id)] == id {
			next := model[len(id)]
			if next == '-' || next == '.' || next == '/' {
				p := prices[id]
				return p[0], p[1], true
			}
		}
	}
	return 0, 0, false
}
