package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
	"github.com/jackc/pgx/v5"
)

// errFakeSearch is the injected search failure for the enrich error-path tests.
var errFakeSearch = errors.New("supermemory search failed")

// patternLeg answers only the pattern leg (Scope=both, Type=pattern) with
// (matches, err); the rule + dismissal legs return a successful empty result.
// The reader seam collapsed to one Search method, so a per-finding enrich runs
// three Search calls (pattern, rule, dismissed) distinguished by q.Type.
func patternLeg(matches []memory.PatternMatch, err error) func(memory.MemoryQuery) ([]memory.PatternMatch, error) {
	return func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
		if q.Type == memory.TypePattern {
			return matches, err
		}
		return nil, nil
	}
}

// fakeEnrichStore is an in-memory PatternLinker for the DB-less enrich read
// tests. Lookups miss with pgx.ErrNoRows (the non-fatal "no such row" the read
// path treats as a benign miss); IncrementPatternMatch records its calls.
type fakeEnrichStore struct {
	byCustomID      map[string]int64
	bySupermemoryID map[string]int64
	autoSuppressed  map[string]bool // categories the repo auto-suppressed; nil = none

	mu          sync.Mutex
	incremented []int64
}

func (f *fakeEnrichStore) GetAutoSuppressedCategories(context.Context, int64) (map[string]bool, error) {
	if f.autoSuppressed != nil {
		return f.autoSuppressed, nil
	}
	return map[string]bool{}, nil
}

func (f *fakeEnrichStore) GetPatternIDByCustomID(_ context.Context, customID string) (int64, error) {
	if id, ok := f.byCustomID[customID]; ok {
		return id, nil
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeEnrichStore) GetPatternIDBySupermemoryID(_ context.Context, smID string) (int64, error) {
	if id, ok := f.bySupermemoryID[smID]; ok {
		return id, nil
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeEnrichStore) IncrementPatternMatch(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incremented = append(f.incremented, id)
	return nil
}

// newTestEnricher builds an Enricher over the memory Fake + PatternLinker with
// the repo coordinates the DB-less tests share. repoID is left 0 so the
// auto-suppressed lookup is skipped; publish is nil so no events fire unless a
// test wires one.
func newTestEnricher(fake *memorytest.Fake, linker PatternLinker) *Enricher {
	return &Enricher{
		reader:       fake,
		linker:       linker,
		logger:       slog.Default(),
		thresholds:   memory.Thresholds{}.WithDefaults(),
		repo:         "widget",
		repoFullName: "acme/widget",
		prNumber:     1,
	}
}

// enrichOneComment drives the Enricher over a single warning-severity comment
// and returns the enriched comment — the DB-less positive/error/novelty paths
// exercised end-to-end through Enricher.Run.
func enrichOneComment(t *testing.T, fake *memorytest.Fake, linker PatternLinker) FileComment {
	t.Helper()
	reviews := []FileReview{
		{
			Path: "handler.go",
			Comments: []FileComment{
				{Severity: SeverityWarning, Category: CategoryBug, Line: 10, Body: "possible nil deref"},
			},
		},
	}
	newTestEnricher(fake, linker).Run(context.Background(), reviews)
	return reviews[0].Comments[0]
}

// (a) A successful pattern hit resolvable to a patterns row: not novel, score +
// id persisted, IncrementPatternMatch fired. This is the previously-untested
// positive read path that never matched anything in prod.
func TestEnrichFindings_PositiveMatch(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg([]memory.PatternMatch{{Score: 0.7, ID: "doc1"}}, nil),
	}
	store := &fakeEnrichStore{bySupermemoryID: map[string]int64{"doc1": 99}}
	c := enrichOneComment(t, fake, store)

	if c.IsNewFinding {
		t.Errorf("a pattern match must not be marked novel")
	}
	if c.MatchedPatternScore != 0.7 {
		t.Errorf("MatchedPatternScore = %v, want 0.7", c.MatchedPatternScore)
	}
	if c.MatchedPatternID != 99 {
		t.Errorf("MatchedPatternID = %d, want 99", c.MatchedPatternID)
	}
	if len(store.incremented) != 1 || store.incremented[0] != 99 {
		t.Errorf("IncrementPatternMatch calls = %v, want [99]", store.incremented)
	}
}

// (b) A failed (errored) search must NOT mark the finding novel — the core
// novel-on-error conflation this change removes.
func TestEnrichFindings_SearchError_NotNovel(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg(nil, errFakeSearch), // pattern search errored
	}
	c := enrichOneComment(t, fake, &fakeEnrichStore{})

	if c.IsNewFinding {
		t.Errorf("a finding must NOT be marked novel when the pattern search errored")
	}
	if c.MatchedPatternScore != 0 {
		t.Errorf("MatchedPatternScore = %v, want 0 on error", c.MatchedPatternScore)
	}
}

// (c) A SUCCESSFUL empty search is the only thing that marks a finding novel.
func TestEnrichFindings_EmptyMatch_Novel(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg(nil, nil), // successful, no hit on every leg
	}
	c := enrichOneComment(t, fake, &fakeEnrichStore{})

	if !c.IsNewFinding {
		t.Errorf("a successful empty search must mark the finding novel")
	}
}

// (b2) A rule-search error (pattern search successfully empty) must NOT mark the
// finding novel either — this closes the #128 gate gap where the rule leg
// swallowed errors and returned "", conflating a broken rule lookup with "no
// rule matched" and letting a finding be miscounted as novel.
func TestEnrichFindings_RuleSearchError_NotNovel(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
			if q.Type == memory.TypeRule {
				return nil, errFakeSearch // rule search errored
			}
			return nil, nil // pattern search: successful empty
		},
	}
	c := enrichOneComment(t, fake, &fakeEnrichStore{})

	if c.IsNewFinding {
		t.Errorf("a finding must NOT be marked novel when the rule search errored")
	}
}

// (d) customId-preferred resolution: the hit's own ID is a chunk id that matches
// no supermemory_id, but the mirrored custom_id resolves the patterns row.
func TestEnrichFindings_ResolveByCustomID(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg([]memory.PatternMatch{{
			Score:    0.7,
			ID:       "chunk_zzz", // not present in bySupermemoryID
			Metadata: map[string]string{"custom_id": "cid-1"},
		}}, nil),
	}
	store := &fakeEnrichStore{byCustomID: map[string]int64{"cid-1": 42}}
	c := enrichOneComment(t, fake, store)

	if c.MatchedPatternID != 42 {
		t.Errorf("MatchedPatternID = %d, want 42 (resolved by custom_id)", c.MatchedPatternID)
	}
	if len(store.incremented) != 1 || store.incremented[0] != 42 {
		t.Errorf("IncrementPatternMatch = %v, want [42]", store.incremented)
	}
}

// (d, fallback) custom_id present but not stored (legacy row) → resolution falls
// back to matching the hit's own ID against supermemory_id.
func TestEnrichFindings_ResolveFallbackToSupermemoryID(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg([]memory.PatternMatch{{
			Score:    0.7,
			ID:       "mem_1",
			Metadata: map[string]string{"custom_id": "cid-unknown"},
		}}, nil),
	}
	store := &fakeEnrichStore{bySupermemoryID: map[string]int64{"mem_1": 7}}
	c := enrichOneComment(t, fake, store)

	if c.MatchedPatternID != 7 {
		t.Errorf("MatchedPatternID = %d, want 7 (fallback to supermemory_id)", c.MatchedPatternID)
	}
	if len(store.incremented) != 1 || store.incremented[0] != 7 {
		t.Errorf("IncrementPatternMatch = %v, want [7]", store.incremented)
	}
}
