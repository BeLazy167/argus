package api

import (
	"encoding/json"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestReviewDetailResponseIncludesStructuredMinorNotes(t *testing.T) {
	payload, err := json.Marshal(ReviewDetailResponse{MinorNotes: []store.ReviewMinorNote{{FilePath: "a.go", Line: 7, Severity: "suggestion", Title: "name this timeout"}}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	var notes []map[string]any
	if err := json.Unmarshal(wire["minor_notes"], &notes); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0]["file_path"] != "a.go" || notes[0]["title"] != "name this timeout" {
		t.Fatalf("minor_notes wire = %s", wire["minor_notes"])
	}
}
