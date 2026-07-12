package memory

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestMemoryQueryRequest is the round-trip check that a MemoryQuery lowers to the
// SearchRequest shape each reader adapter depends on: the type filter and extra
// conditions collapse to a single AND group, Enrich toggles the related+summary
// include, and the retrieval knobs pass through verbatim.
func TestMemoryQueryRequest(t *testing.T) {
	q := MemoryQuery{
		Query:     "nil deref",
		Type:      TypeFeedback,
		Filters:   []FilterCondition{{Key: "action", Value: "dismissed"}},
		Limit:     5,
		Threshold: 0.5,
		Rerank:    true,
		Enrich:    true,
	}
	req := q.request()

	if req.Query != "nil deref" || req.SearchMode != "hybrid" || req.Limit != 5 || req.Threshold != 0.5 || !req.Rerank {
		t.Fatalf("request knobs not passed through: %+v", req)
	}
	if req.Filters == nil || len(req.Filters.AND) != 2 {
		t.Fatalf("want 2 AND conditions (type + action), got %+v", req.Filters)
	}
	if req.Filters.AND[0].Key != "type" || req.Filters.AND[0].Value != string(TypeFeedback) {
		t.Errorf("first condition must be the type filter, got %+v", req.Filters.AND[0])
	}
	if req.Filters.AND[1].Key != "action" || req.Filters.AND[1].Value != "dismissed" {
		t.Errorf("second condition must be the extra filter, got %+v", req.Filters.AND[1])
	}
	if req.Include == nil || !req.Include.RelatedMemories || !req.Include.Summaries {
		t.Errorf("Enrich must request related memories + summaries, got %+v", req.Include)
	}
}

// TestMemoryQueryRequest_Untyped: an untyped, unfiltered, non-enriched query
// carries no Filters and no Include (a lean plain search — the SearchScored shape).
func TestMemoryQueryRequest_Untyped(t *testing.T) {
	req := MemoryQuery{Query: "q", Limit: 5, Threshold: 0.5}.request()
	if req.Filters != nil {
		t.Errorf("untyped/unfiltered query must carry no Filters, got %+v", req.Filters)
	}
	if req.Include != nil {
		t.Errorf("non-enriched query must carry no Include, got %+v", req.Include)
	}
}

// TestContainerTags maps each scope to its concrete container tags, treats an
// empty repo on a repo scope as the disabled no-op (nil, nil), and rejects an
// unknown scope.
func TestContainerTags(t *testing.T) {
	repoTag := RepoTagNew("widget")

	tests := []struct {
		name     string
		q        MemoryQuery
		wantTags []string
		wantErr  bool
	}{
		{"shared", MemoryQuery{Scope: ScopeShared}, []string{SharedTag}, false},
		{"repo", MemoryQuery{Scope: ScopeRepo, Repo: "widget"}, []string{repoTag}, false},
		{"both", MemoryQuery{Scope: ScopeBoth, Repo: "widget"}, []string{repoTag, SharedTag}, false},
		{"repo empty-repo disabled", MemoryQuery{Scope: ScopeRepo}, nil, false},
		{"both empty-repo disabled", MemoryQuery{Scope: ScopeBoth}, nil, false},
		{"unknown scope errors", MemoryQuery{Scope: ContainerScope("bogus")}, nil, true},
		{"zero-value scope errors", MemoryQuery{}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.q.containerTags()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !equalStrings(got, tt.wantTags) {
				t.Errorf("tags = %v, want %v", got, tt.wantTags)
			}
		})
	}
}

func TestBestMatch(t *testing.T) {
	got := BestMatch(
		PatternMatch{ID: "a", Score: 0.4},
		PatternMatch{ID: "b", Score: 0.9},
		PatternMatch{ID: "c", Score: 0.7},
	)
	if got.ID != "b" {
		t.Errorf("BestMatch picked %q, want the highest-scoring b", got.ID)
	}
	if m := BestMatch(); m.ID != "" || m.Score != 0 {
		t.Errorf("BestMatch() with no candidates must be the zero match, got %+v", m)
	}
	if m := BestMatch(PatternMatch{ID: "z", Score: 0}); m.ID != "" || m.Score != 0 {
		t.Errorf("a zero-score candidate must not beat the zero match, got %+v", m)
	}
}

func TestHintStrings(t *testing.T) {
	got := HintStrings([]PatternMatch{
		{RichContent: "first hit\nSummary: s"},
		{RichContent: ""}, // dropped
		{RichContent: "second hit"},
	})
	want := []string{"first hit\nSummary: s", "second hit"}
	if !equalStrings(got, want) {
		t.Errorf("HintStrings = %v, want %v", got, want)
	}
	if got := HintStrings(nil); len(got) != 0 {
		t.Errorf("HintStrings(nil) = %v, want empty", got)
	}
	// Over-long RichContent is truncated to the 500-char render budget (+ the
	// util.Truncate ellipsis), preserving the pre-seam SearchHints behavior.
	long := HintStrings([]PatternMatch{{RichContent: strings.Repeat("x", 900)}})
	if len(long) != 1 || long[0] != strings.Repeat("x", 500)+"..." {
		t.Errorf("HintStrings must truncate to 500 chars + ellipsis, got len %d", len(long[0]))
	}
}

func TestTopContent(t *testing.T) {
	got := TopContent([]PatternMatch{{Content: ""}, {Content: "rule body"}}, 300)
	if got != "rule body" {
		t.Errorf("TopContent = %q, want the first non-empty content", got)
	}
	if got := TopContent(nil, 300); got != "" {
		t.Errorf("TopContent(nil) = %q, want empty", got)
	}
	if got := TopContent([]PatternMatch{{Content: strings.Repeat("y", 400)}}, 300); got != strings.Repeat("y", 300)+"..." {
		t.Errorf("TopContent must cap at maxChars + ellipsis, got len %d", len(got))
	}
}

func TestScenarioResults(t *testing.T) {
	matches := []PatternMatch{
		{Content: "a", Score: 0.9, Metadata: map[string]string{"scenario_id": "5"}},
		{Content: "missing id", Score: 0.8, Metadata: map[string]string{}},            // skipped
		{Content: "dup", Score: 0.7, Metadata: map[string]string{"scenario_id": "5"}}, // deduped
		{Content: "b", Score: 0.6, Metadata: map[string]string{"scenario_id": "6"}},
	}
	got := ScenarioResults(matches, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 results (missing-id skipped, dup deduped), got %d: %+v", len(got), got)
	}
	if got[0].ID != 5 || got[0].Content != "a" || got[0].Similarity != 0.9 {
		t.Errorf("first result = %+v, want {ID:5 Content:a Similarity:0.9}", got[0])
	}
	if got[1].ID != 6 {
		t.Errorf("second result ID = %d, want 6", got[1].ID)
	}
	// Limit caps the output.
	if capped := ScenarioResults(matches, 1); len(capped) != 1 {
		t.Errorf("limit=1 must cap at 1 result, got %d", len(capped))
	}
}

// TestBestEffort proves the single degrade-policy decorator: a failed read logs
// once at Warn with the standard fields and returns the zero value; a successful
// read returns the value and logs nothing.
func TestBestEffort(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	got := BestEffort(logger, "dismissal", "widget-container", 42, func() ([]PatternMatch, error) {
		return nil, errors.New("boom")
	})
	if got != nil {
		t.Errorf("degraded read must return the zero value, got %v", got)
	}
	logged := buf.String()
	for _, want := range []string{"memory read degraded", "caller=dismissal", "container=widget-container", "query_len=42", "boom"} {
		if !strings.Contains(logged, want) {
			t.Errorf("Warn log missing %q; got: %s", want, logged)
		}
	}

	buf.Reset()
	ok := BestEffort(logger, "dismissal", "c", 1, func() ([]PatternMatch, error) {
		return []PatternMatch{{ID: "x"}}, nil
	})
	if len(ok) != 1 || ok[0].ID != "x" {
		t.Errorf("successful read must pass its value through, got %v", ok)
	}
	if buf.Len() != 0 {
		t.Errorf("a successful read must not log, got: %s", buf.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
