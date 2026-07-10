package pipeline

import "testing"

// TestShouldIndexReviewMemory drives the full severity × score × scoringSkipped
// matrix for the reviews-container write floor.
func TestShouldIndexReviewMemory(t *testing.T) {
	tests := []struct {
		name        string
		severity    Severity
		score       int
		scoringSkip bool
		wantIndexed bool
	}{
		// critical / warning always index, regardless of score or scoring state.
		{"critical scored", SeverityCritical, 0, false, true},
		{"critical zero score", SeverityCritical, 0, false, true},
		{"critical scoring skipped", SeverityCritical, 0, true, true},
		{"warning scored", SeverityWarning, 50, false, true},
		{"warning scoring skipped", SeverityWarning, 0, true, true},

		// suggestions gate on the score floor when scoring ran.
		{"suggestion above floor", SeveritySuggestion, 70, false, true},
		{"suggestion well above floor", SeveritySuggestion, 95, false, true},
		{"suggestion below floor", SeveritySuggestion, 69, false, false},
		{"suggestion zero score", SeveritySuggestion, 0, false, false},

		// suggestions drop entirely when scoring was skipped (no score to gate on).
		{"suggestion scoring skipped high implied", SeveritySuggestion, 90, true, false},
		{"suggestion scoring skipped", SeveritySuggestion, 0, true, false},

		// praise is never indexed to the reviews container.
		{"praise scored high", SeverityPraise, 100, false, false},
		{"praise scoring skipped", SeverityPraise, 0, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldIndexReviewMemory(tc.severity, tc.score, tc.scoringSkip)
			if got != tc.wantIndexed {
				t.Errorf("shouldIndexReviewMemory(%s, %d, skip=%v) = %v, want %v",
					tc.severity, tc.score, tc.scoringSkip, got, tc.wantIndexed)
			}
		})
	}
}
