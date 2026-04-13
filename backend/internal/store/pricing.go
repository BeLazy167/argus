package store

import (
	"context"
	"sync"
	"time"
)

// ModelPricing holds per-1M-token pricing for a model pattern.
type ModelPricing struct {
	Pattern        string  `json:"model_pattern"`
	InputPerMil    float64 `json:"input_per_million"`
	OutputPerMil   float64 `json:"output_per_million"`
}

// PricingCache loads model pricing from DB and caches it with a TTL.
type PricingCache struct {
	mu      sync.RWMutex
	entries []ModelPricing
	loaded  time.Time
	ttl     time.Duration
	store   *Store
}

func NewPricingCache(st *Store) *PricingCache {
	return &PricingCache{store: st, ttl: 10 * time.Minute}
}

func (pc *PricingCache) get(ctx context.Context) []ModelPricing {
	pc.mu.RLock()
	if time.Since(pc.loaded) < pc.ttl && len(pc.entries) > 0 {
		e := pc.entries
		pc.mu.RUnlock()
		return e
	}
	pc.mu.RUnlock()

	pc.mu.Lock()
	defer pc.mu.Unlock()
	// Double-check after acquiring write lock
	if time.Since(pc.loaded) < pc.ttl && len(pc.entries) > 0 {
		return pc.entries
	}

	rows, err := pc.store.Pool.Query(ctx, `SELECT model_pattern, input_per_million, output_per_million FROM model_pricing ORDER BY length(model_pattern) DESC`)
	if err != nil {
		return pc.entries // return stale on error
	}
	defer rows.Close()

	var entries []ModelPricing
	for rows.Next() {
		var p ModelPricing
		if err := rows.Scan(&p.Pattern, &p.InputPerMil, &p.OutputPerMil); err != nil {
			continue
		}
		entries = append(entries, p)
	}
	if len(entries) > 0 {
		pc.entries = entries
		pc.loaded = time.Now()
	}
	return pc.entries
}

// Lookup finds pricing for a model. Tries exact match first, then prefix match.
// Returns (inputPer1M, outputPer1M, found).
func (pc *PricingCache) Lookup(ctx context.Context, model string) (float64, float64, bool) {
	entries := pc.get(ctx)
	// Exact match
	for _, e := range entries {
		if e.Pattern == model {
			return e.InputPerMil, e.OutputPerMil, true
		}
	}
	// Prefix match (entries sorted longest-first so more specific patterns win)
	for _, e := range entries {
		if len(model) > len(e.Pattern) && model[:len(e.Pattern)] == e.Pattern {
			next := model[len(e.Pattern)]
			if next == '-' || next == '.' || next == '/' {
				return e.InputPerMil, e.OutputPerMil, true
			}
		}
	}
	return 0, 0, false
}
