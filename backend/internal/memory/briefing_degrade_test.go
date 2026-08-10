package memory

import (
	"context"
	"errors"
	"testing"
)

// degradeRun returns a runSearchFn that fails the legs matching failWhen and
// answers every other leg with one high-score pattern hit.
//
// The seam under test IS runSearchFn: assembleBriefingWith and
// specialistBlockWith take it as a parameter precisely so per-leg degradation
// can be exercised without a backend. An earlier version of this file stubbed
// HTTP responses instead, which only tested the same logic through an extra
// layer of transport fiction.
func degradeRun(failWhen func(SearchRequest) bool) runSearchFn {
	return func(_ context.Context, req SearchRequest) ([]PatternMatch, error) {
		if failWhen != nil && failWhen(req) {
			return nil, errors.New("boom")
		}
		return []PatternMatch{{
			ID:       "doc1",
			Score:    0.9,
			Content:  "pattern: check WHERE clauses",
			Metadata: map[string]string{"type": "pattern"},
		}}, nil
	}
}

// filtersOn reports whether any AND/OR condition on the request carries value
// v — the leg selector, since each leg is distinguished by its type filter.
func filtersOn(req SearchRequest, v string) bool {
	if req.Filters == nil {
		return false
	}
	for _, c := range req.Filters.AND {
		if c.Value == v {
			return true
		}
	}
	for _, c := range req.Filters.OR {
		if c.Value == v {
			return true
		}
	}
	return false
}

// An OPTIONAL leg failure (rules side-search) must not blank the briefing:
// the core block renders, the failed section is omitted, no error returned.
func TestAssembleBriefingOptionalLegFailureKeepsCore(t *testing.T) {
	run := degradeRun(func(req SearchRequest) bool { return filtersOn(req, "rule") })
	b, err := assembleBriefingWith(context.Background(), run, discardLogger(), BriefingQuery{
		Repo: "acme/widgets", FilePath: "a.go", Query: "invoice rounding",
	})
	if err != nil {
		t.Fatalf("optional-leg failure must not error the briefing: %v", err)
	}
	if len(b.Rules) != 0 {
		t.Errorf("failed rules leg must render empty, got %v", b.Rules)
	}
	if len(b.Patterns) == 0 && len(b.PastReviews) == 0 && b.Synthesis == "" {
		t.Error("successful legs must survive an optional-leg failure")
	}
}

// When EVERY leg fails there is nothing usable — the briefing errors.
func TestAssembleBriefingAllLegsFailErrors(t *testing.T) {
	run := degradeRun(func(SearchRequest) bool { return true })
	_, err := assembleBriefingWith(context.Background(), run, discardLogger(), BriefingQuery{
		Repo: "acme/widgets", FilePath: "a.go", Query: "q",
	})
	if err == nil {
		t.Fatal("all-legs failure must return an error")
	}
}

// specialistBlock keeps surviving legs when one of its three legs fails.
func TestSpecialistBlockPartialLegFailureKeepsRest(t *testing.T) {
	run := degradeRun(func(req SearchRequest) bool { return filtersOn(req, "synthesis") })
	block, err := specialistBlockWith(context.Background(), run, discardLogger(), "acme/widgets", "a.go", "q", NewThresholds())
	if err != nil {
		t.Fatalf("partial specialist-leg failure must not error: %v", err)
	}
	if len(block.Repo) == 0 && len(block.Shared) == 0 {
		t.Error("surviving specialist legs must be kept")
	}
}
