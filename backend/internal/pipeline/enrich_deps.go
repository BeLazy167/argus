// Package pipeline — enrich_deps.go holds the minimal store surface the
// per-finding memory-enrichment pass (the Enricher, enricher.go) calls out to.
// Production gets a default adapter over the concrete *store.Store; tests set
// Orchestrator.enrichStoreOverride — or construct an Enricher directly with a
// fake linker — to route through an in-memory fake so the enrich path can be
// exercised without a database.
//
// The interface carries ONLY the four methods the Enricher invokes, so a test
// fake satisfies it in a few dozen lines (the crosspr_stage_deps.go idiom:
// consumer-declared, *store.Store satisfies it implicitly).
package pipeline

import (
	"context"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// PatternLinker is the narrow store surface the Enricher consumes to link a
// pattern search hit to its patterns-table row, bump its match counter, and
// read the repo's auto-suppressed categories. Method names + signatures mirror
// *store.Store verbatim so defaultEnrichStore is one-line pass-throughs.
type PatternLinker interface {
	GetPatternIDByCustomID(ctx context.Context, customID string) (int64, error)
	GetPatternIDBySupermemoryID(ctx context.Context, supermemoryID string) (int64, error)
	IncrementPatternMatch(ctx context.Context, patternID int64) error
	GetAutoSuppressedCategories(ctx context.Context, repoID int64) (map[string]bool, error)
}

// defaultEnrichStore wraps *store.Store into the PatternLinker interface. Pure
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

// enrichStoreDep returns the PatternLinker the Enricher uses. If
// Orchestrator.enrichStoreOverride is non-nil, that fake is returned — otherwise
// a default adapter over o.st is constructed on the fly.
func (o *Orchestrator) enrichStoreDep() PatternLinker {
	if o.enrichStoreOverride != nil {
		return o.enrichStoreOverride
	}
	return defaultEnrichStore{st: o.st}
}
