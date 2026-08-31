package memory

import (
	"context"
	"strings"
	"testing"
)

func TestBriefingRendersDisputeWithoutEnforcement(t *testing.T) {
	q := BriefingQuery{Repo: "api", Options: BriefingOptions{Profile: ProfileReview, CharCap: 3200}, Disputed: []ConventionConflict{{LeftContent: "Convention [testing]: use testify", RightContent: "Convention [testing]: use stdlib", Category: "testing"}}}
	got, err := briefingWith(t.Context(), func(_ context.Context, _ SearchRequest) ([]PatternMatch, error) { return nil, nil }, nil, q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## Disputed — do not enforce") || !strings.Contains(got, "team is split on Convention [testing]: use testify vs Convention [testing]: use stdlib — do not enforce either side") {
		t.Fatalf("got %q", got)
	}
}
