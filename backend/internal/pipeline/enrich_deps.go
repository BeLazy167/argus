// Package pipeline — enrich_deps.go holds the minimal store surface the
// per-finding memory-enrichment read (enrichFindings) calls out to. Production
// gets a default adapter over the concrete *store.Store; tests set
// Orchestrator.enrichStoreOverride to route through an in-memory fake so the
// enrich read path can be exercised without a database.
//
// The interface carries ONLY the method subset enrichFindings invokes, so a
// test fake satisfies it in a few dozen lines.
package pipeline

import (
	"context"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// enrichStore is the store surface enrichFindings consumes. Method names +
// signatures mirror *store.Store verbatim so defaultEnrichStore is one-line
// pass-throughs.
type enrichStore interface {
	GetAutoSuppressedCategories(ctx context.Context, repoID int64) (map[string]bool, error)
	GetPatternIDBySupermemoryID(ctx context.Context, supermemoryID string) (int64, error)
	GetPatternIDByCustomID(ctx context.Context, customID string) (int64, error)
	IncrementPatternMatch(ctx context.Context, patternID int64) error
}

// defaultEnrichStore wraps *store.Store into the enrichStore interface. Pure
// delegation — no business logic here.
type defaultEnrichStore struct{ st *store.Store }

func (d defaultEnrichStore) GetAutoSuppressedCategories(ctx context.Context, repoID int64) (map[string]bool, error) {
	return d.st.GetAutoSuppressedCategories(ctx, repoID)
}
func (d defaultEnrichStore) GetPatternIDBySupermemoryID(ctx context.Context, supermemoryID string) (int64, error) {
	return d.st.GetPatternIDBySupermemoryID(ctx, supermemoryID)
}
func (d defaultEnrichStore) GetPatternIDByCustomID(ctx context.Context, customID string) (int64, error) {
	return d.st.GetPatternIDByCustomID(ctx, customID)
}
func (d defaultEnrichStore) IncrementPatternMatch(ctx context.Context, patternID int64) error {
	return d.st.IncrementPatternMatch(ctx, patternID)
}

// enrichStoreDep returns the store surface enrichFindings uses. If
// Orchestrator.enrichStoreOverride is non-nil, that fake is returned — otherwise
// a default adapter over o.st is constructed on the fly.
func (o *Orchestrator) enrichStoreDep() enrichStore {
	if o.enrichStoreOverride != nil {
		return o.enrichStoreOverride
	}
	return defaultEnrichStore{st: o.st}
}
