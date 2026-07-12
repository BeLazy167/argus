package pipeline

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
)

// The reader seam collapsed to two deep, error-honest methods (Search +
// Briefing). These tests pin the per-call-site failure POLICY over the Fake:
// enrich propagates (a broken search must not decide novelty), while briefing,
// hints, scenario dedup and dismissal suppression degrade to their empty value.

// --- Briefing degrades to empty on retrieval failure ---

func TestSpecialistBriefing_DegradesOnError(t *testing.T) {
	fake := &memorytest.Fake{
		BriefingFn: func(memory.BriefingQuery) (string, error) { return "", errFakeSearch },
	}
	got := specialistBriefing(context.Background(), fake, "acme", "widget", SpecialistBugHunter, "handler.go", memory.Thresholds{})
	if got != "" {
		t.Errorf("a failed Briefing must degrade to empty, got %q", got)
	}
}

func TestReviewBriefing_DegradesOnError(t *testing.T) {
	fake := &memorytest.Fake{
		BriefingFn: func(memory.BriefingQuery) (string, error) { return "", errFakeSearch },
	}
	got := reviewBriefing(context.Background(), fake, "acme", "widget", "handler.go", memory.Thresholds{})
	if got != "" {
		t.Errorf("a failed Briefing must degrade to empty, got %q", got)
	}
}

// The wrappers must lower to the right BriefingQuery (profile + cap + query) and
// pass a successful block through verbatim.
func TestBriefing_WrappersBuildQuery(t *testing.T) {
	var gotSpecialist, gotReview memory.BriefingQuery
	specialistFake := &memorytest.Fake{
		BriefingFn: func(q memory.BriefingQuery) (string, error) { gotSpecialist = q; return "SPEC", nil },
	}
	reviewFake := &memorytest.Fake{
		BriefingFn: func(q memory.BriefingQuery) (string, error) { gotReview = q; return "REVIEW", nil },
	}

	if got := specialistBriefing(context.Background(), specialistFake, "acme", "widget", SpecialistBugHunter, "handler.go", memory.Thresholds{}); got != "SPEC" {
		t.Errorf("specialistBriefing passthrough = %q, want SPEC", got)
	}
	if gotSpecialist.Options.Profile != memory.ProfileSpecialist || gotSpecialist.Options.CharCap != 2400 || !gotSpecialist.Options.EmphasizeFalsePositives {
		t.Errorf("specialist query wrong: %+v", gotSpecialist.Options)
	}
	if gotSpecialist.Repo != "widget" || gotSpecialist.FilePath != "handler.go" || gotSpecialist.Query == "" {
		t.Errorf("specialist query coords wrong: %+v", gotSpecialist)
	}

	if got := reviewBriefing(context.Background(), reviewFake, "acme", "widget", "handler.go", memory.Thresholds{}); got != "REVIEW" {
		t.Errorf("reviewBriefing passthrough = %q, want REVIEW", got)
	}
	if gotReview.Options.Profile != memory.ProfileReview || gotReview.Options.CharCap != 3200 {
		t.Errorf("review query wrong: %+v", gotReview.Options)
	}
}

// --- Hints degrade to nil on retrieval failure, shape on success ---

func TestSearchHints_DegradesOnError(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: func(memory.MemoryQuery) ([]memory.PatternMatch, error) { return nil, errFakeSearch },
	}
	got := searchHints(context.Background(), fake, "triage-pattern", "widget", memory.NewThresholds(), memory.MemoryQuery{
		Query: "q", Repo: "widget", Scope: memory.ScopeRepo, Type: memory.TypePattern, Limit: 5,
	})
	if got != nil {
		t.Errorf("a failed hint search must degrade to nil, got %v", got)
	}
}

func TestSearchHints_ShapesOnSuccess(t *testing.T) {
	var seen memory.MemoryQuery
	fake := &memorytest.Fake{
		SearchFn: func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
			seen = q
			return []memory.PatternMatch{{RichContent: "hit one"}, {RichContent: ""}, {RichContent: "hit two"}}, nil
		},
	}
	got := searchHints(context.Background(), fake, "scoring-pattern", "widget", memory.NewThresholds(), memory.MemoryQuery{
		Query: "q", Repo: "widget", Scope: memory.ScopeRepo, Type: memory.TypePattern, Limit: 5,
	})
	want := []string{"hit one", "hit two"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("searchHints = %v, want %v (RichContent shaped, empties dropped)", got, want)
	}
	// searchHints stamps the hint retrieval knobs regardless of caller input.
	if seen.Threshold != 0.5 || !seen.Rerank || !seen.Enrich {
		t.Errorf("hint knobs not stamped: threshold=%v rerank=%v enrich=%v", seen.Threshold, seen.Rerank, seen.Enrich)
	}
}

// --- Scenario dedup degrades to nil on failure, parses ids on success ---

func TestScenarioSearch_DegradesOnError(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: func(memory.MemoryQuery) ([]memory.PatternMatch, error) { return nil, errFakeSearch },
	}
	got := scenarioSearch(context.Background(), fake, slog.Default(), "widget", "some scenario", "", 1)
	if got != nil {
		t.Errorf("a failed scenario search must degrade to nil, got %v", got)
	}
}

func TestScenarioSearch_ParsesOnSuccess(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
			if q.Type != memory.TypeScenario {
				t.Errorf("scenarioSearch must pin type=scenario, got %q", q.Type)
			}
			return []memory.PatternMatch{{Content: "sc", Score: 0.9, Metadata: map[string]string{"scenario_id": "7"}}}, nil
		},
	}
	got := scenarioSearch(context.Background(), fake, slog.Default(), "widget", "some scenario", "", 1)
	if len(got) != 1 || got[0].ID != 7 || got[0].Similarity != 0.9 {
		t.Errorf("scenarioSearch = %+v, want one result {ID:7 Similarity:0.9}", got)
	}
}

// --- Dismissal suppression degrades at the enrich call site ---

// A dismissal-search error must degrade (via BestEffort) to no dismissals, so
// the finding is neither suppressed nor able to crash the enrich pass — the
// non-fatal, memory-gated contract for suppression.
func TestEnrichFindings_DismissalSearchError_NoSuppression(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
			if q.Type == memory.TypeFeedback { // the dismissal leg
				return nil, errFakeSearch
			}
			return nil, nil
		},
	}
	c := enrichOneComment(t, fake, &fakeEnrichStore{})
	if c.Suppressed {
		t.Errorf("a failed dismissal search must degrade to no suppression, got Suppressed=true")
	}
}

// dismissalSearch itself PROPAGATES the error (the degrade lives at the call
// site) and short-circuits an empty body without touching the indexer.
func TestDismissalSearch_PropagatesAndGuardsEmpty(t *testing.T) {
	called := false
	fake := &memorytest.Fake{
		SearchFn: func(memory.MemoryQuery) ([]memory.PatternMatch, error) {
			called = true
			return nil, errFakeSearch
		},
	}
	if _, err := dismissalSearch(context.Background(), fake, "widget", "body", 0.5); err == nil {
		t.Error("dismissalSearch must propagate the search error")
	}
	called = false
	if got, err := dismissalSearch(context.Background(), fake, "widget", "", 0.5); err != nil || got != nil || called {
		t.Errorf("empty body must short-circuit to (nil,nil) without a search; called=%v", called)
	}
}
