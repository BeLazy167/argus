package pipeline

// reviewSuggestionScoreFloor is the minimum judge score a suggestion-severity
// finding must reach before it is written to the reviews memory container.
// Below it, suggestions are low-signal noise and stay out of retrieval.
const reviewSuggestionScoreFloor = 70

// shouldIndexReviewMemory reports whether a review finding is written to the
// reviews-container in Supermemory. The write floor:
//
//   - critical / warning     → always indexed
//   - suggestion             → only when scored >= reviewSuggestionScoreFloor
//   - praise (and any other) → never indexed
//
// When scoring was skipped the judge score is unavailable, so only the severity
// floor applies: critical/warning index, everything else (suggestions included)
// is dropped.
func shouldIndexReviewMemory(severity Severity, score int, scoringSkipped bool) bool {
	switch severity {
	case SeverityCritical, SeverityWarning:
		return true
	case SeveritySuggestion:
		if scoringSkipped {
			return false
		}
		return score >= reviewSuggestionScoreFloor
	default:
		return false
	}
}
