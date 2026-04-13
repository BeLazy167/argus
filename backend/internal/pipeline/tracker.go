package pipeline

// EventTracker captures pipeline lifecycle events for observability (PostHog, etc.).
// All methods are no-ops if the implementation is nil.
type EventTracker interface {
	// TrackReviewStarted fires when a review pipeline begins.
	TrackReviewStarted(installationID int64, repo string, prNumber int, reviewID string, isIncremental bool, deepReview bool)
	// TrackStageCompleted fires when a pipeline stage finishes.
	TrackStageCompleted(installationID int64, repo string, prNumber int, reviewID string, stage string, durationMs int64)
	// TrackReviewCompleted fires when a review finishes successfully.
	TrackReviewCompleted(installationID int64, repo string, prNumber int, reviewID string, score int, commentCount int, durationMs int64)
	// TrackReviewFailed fires when a review pipeline fails.
	TrackReviewFailed(installationID int64, repo string, prNumber int, reviewID string, stage string, errMsg string)
}

// noopTracker satisfies EventTracker with no-ops.
type noopTracker struct{}

func (noopTracker) TrackReviewStarted(int64, string, int, string, bool, bool)      {}
func (noopTracker) TrackStageCompleted(int64, string, int, string, string, int64)   {}
func (noopTracker) TrackReviewCompleted(int64, string, int, string, int, int, int64) {}
func (noopTracker) TrackReviewFailed(int64, string, int, string, string, string)    {}
