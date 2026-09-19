package llm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// openRouterModelsURL is OpenRouter's public model catalog — no auth.
	openRouterModelsURL = "https://openrouter.ai/api/v1/models"
	// openRouterPricingTTL bounds how long the catalog is reused. Prices drift
	// slowly; a multi-hour cache keeps one HTTP fetch off every LLM call path.
	openRouterPricingTTL = 6 * time.Hour
	// openRouterCatalogMaxBytes bounds the catalog read (~3MB today).
	openRouterCatalogMaxBytes = 16 << 20
	// openRouterFetchTimeout bounds the catalog fetch.
	openRouterFetchTimeout = 15 * time.Second
)

// OpenRouterPricing resolves per-1M-token pricing from OpenRouter's public
// model catalog — the fallback when model_pricing has no manual row. The
// catalog is fetched once and cached; a failed refresh serves the stale copy.
type OpenRouterPricing struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex            // serializes catalog fetches
	prices    map[string][2]float64 // model id → [inputPer1M, outputPer1M]
	loaded    time.Time
	client    *http.Client
	url       string // overridable in tests
}

// NewOpenRouterPricing returns a lazily-populated catalog resolver.
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

// refresh fetches the catalog and swaps the price map. On any failure the
// stale map stays in place but 'loaded' still advances, so a down endpoint
// isn't retried once per LLM call.
func (o *OpenRouterPricing) refresh() {
	o.refreshMu.Lock()
	defer o.refreshMu.Unlock()
	// Double-check under the refresh lock — a waiter behind us may have
	// just fetched a fresh copy. 'loaded' stamps attempts, not successes:
	// a failed fetch throttles retries to once per TTL window.
	o.mu.RLock()
	recent := time.Since(o.loaded) < openRouterPricingTTL
	o.mu.RUnlock()
	if recent {
		return
	}
	defer func() {
		o.mu.Lock()
		o.loaded = time.Now()
		o.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), openRouterFetchTimeout)
	defer cancel()
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
		if err1 != nil || err2 != nil || in < 0 || out < 0 {
			continue
		}
		prices[m.ID] = [2]float64{in * 1_000_000, out * 1_000_000}
	}
	if len(prices) == 0 {
		return
	}
	o.mu.Lock()
	o.prices = prices
	o.mu.Unlock()
}

// catalog returns the price map, refreshing when the last fetch attempt is
// older than the TTL — including after failures, so a down endpoint is
// retried at most once per TTL window.
func (o *OpenRouterPricing) catalog() map[string][2]float64 {
	o.mu.RLock()
	recent := time.Since(o.loaded) < openRouterPricingTTL
	p := o.prices
	o.mu.RUnlock()
	if !recent {
		o.refresh()
		o.mu.RLock()
		p = o.prices
		o.mu.RUnlock()
	}
	return p
}

// Lookup resolves model pricing against the OpenRouter catalog. Order:
// exact id match → unique "/"-suffix match (bare provider model names like
// "gpt-5.6-terra" resolve to "openai/gpt-5.6-terra" when unambiguous) →
// prefix match with the same next-char rule the DB cache uses. Ambiguous
// suffix matches return not-found — never guess a price.
func (o *OpenRouterPricing) Lookup(model string) (float64, float64, bool) {
	prices := o.catalog()
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
