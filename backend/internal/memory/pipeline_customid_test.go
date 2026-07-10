package memory

import (
	"encoding/json"
	"testing"
)

func TestPipelinePatternCustomID(t *testing.T) {
	cat := "style"
	tests := []struct {
		name    string
		repo    string
		source  string
		content string
		cat     *string
		shared  bool
		want    string
	}{
		{"scoring_confirmed", "webapp", "scoring_confirmed", "body", nil, false, PatternCustomID("", "webapp", "confirmed", "body")},
		{"auto_learn repo", "webapp", "auto_learn", "p", nil, false, PatternCustomID("", "webapp", "learned", "p")},
		{"auto_learn shared", "webapp", "auto_learn", "p", nil, true, PatternCustomID("", "", "org_learned", "p")},
		{"convention", "webapp", "convention", "Convention [style]: use tabs", &cat, false, PatternCustomID("", "webapp", "convention", "use tabs")},
		{"unknown -> empty", "webapp", "manual", "p", nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PipelinePatternCustomID(tt.repo, tt.source, tt.content, tt.cat, tt.shared); got != tt.want {
				t.Errorf("PipelinePatternCustomID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRawConvention(t *testing.T) {
	cat := "style"
	if got := RawConvention("Convention [style]: use tabs", &cat); got != "use tabs" {
		t.Errorf("RawConvention = %q, want 'use tabs'", got)
	}
	if got := RawConvention("no wrapper", &cat); got != "no wrapper" {
		t.Errorf("RawConvention(no wrapper) = %q, want unchanged", got)
	}
	if got := RawConvention("Convention []: x", nil); got != "x" {
		t.Errorf("RawConvention(nil cat) = %q, want 'x'", got)
	}
}

func TestScenarioCustomID(t *testing.T) {
	// Must match the repoIDSegment-based scheme the pipeline writes.
	if got, want := ScenarioCustomID("webapp", 7), "webapp--scenario--7"; got != want {
		t.Errorf("ScenarioCustomID = %q, want %q", got, want)
	}
	// Lossy repo names get the disambiguating hash on the segment.
	if ScenarioCustomID("sdk.js", 1) == "sdk-js--scenario--1" {
		t.Error("lossy repo name should carry a disambiguating hash, not bare sanitize")
	}
}

func TestBatchAddResponse_DocIDs(t *testing.T) {
	// Documented shape: results[].id in order.
	var r BatchAddResponse
	if err := json.Unmarshal([]byte(`{"results":[{"id":"a","status":"done"},{"id":"","status":"error"},{"id":"c","status":"queued"}],"success":2,"failed":1}`), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := r.DocIDs()
	want := []string{"a", "", "c"}
	if len(got) != len(want) {
		t.Fatalf("DocIDs len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DocIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Legacy fallback: {ids:[...]} with no results.
	var legacy BatchAddResponse
	if err := json.Unmarshal([]byte(`{"ids":["x","y"]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if ids := legacy.DocIDs(); len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Errorf("legacy DocIDs = %v, want [x y]", ids)
	}
}

func TestListResponse_Pagination(t *testing.T) {
	var r ListResponse
	if err := json.Unmarshal([]byte(`{"memories":[],"pagination":{"currentPage":1,"totalItems":538,"totalPages":6,"limit":100}}`), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Pagination == nil || r.Pagination.TotalItems != 538 || r.Pagination.TotalPages != 6 {
		t.Errorf("pagination = %+v, want totalItems=538 totalPages=6", r.Pagination)
	}
}
