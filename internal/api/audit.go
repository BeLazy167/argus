package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/posthog/posthog-go"
)

// auditLogger wraps PostHog client for settings audit events.
// Compliance: NEVER send api_key, api_key_enc, prompt_text, or custom_persona_prompt.
type auditLogger struct {
	client posthog.Client
	logger *slog.Logger
}

// newAuditLogger creates a PostHog-backed audit logger.
// Returns nil (no-op) if POSTHOG_API_KEY is not set.
func newAuditLogger(logger *slog.Logger) *auditLogger {
	apiKey := os.Getenv("POSTHOG_API_KEY")
	if apiKey == "" {
		logger.Info("POSTHOG_API_KEY not set, audit logging disabled")
		return nil
	}
	client, err := posthog.NewWithConfig(apiKey, posthog.Config{
		Endpoint: "https://us.i.posthog.com",
	})
	if err != nil {
		logger.Error("failed to create PostHog client", "error", err)
		return nil
	}
	return &auditLogger{client: client, logger: logger}
}

// logSettingsChange sends a settings_changed event to PostHog.
// Properties must NOT contain secrets (api keys, prompts, persona prompts).
func (a *auditLogger) logSettingsChange(ctx context.Context, clerkUserID string, installationID int64, action string, properties map[string]interface{}) {
	if a == nil {
		return
	}
	props := posthog.NewProperties()
	props.Set("installation_id", installationID)
	props.Set("action", action)
	for k, v := range properties {
		props.Set(k, v)
	}
	err := a.client.Enqueue(posthog.Capture{
		DistinctId: clerkUserID,
		Event:      "settings_changed",
		Properties: props,
	})
	if err != nil {
		a.logger.Warn("posthog audit event failed", "error", err, "action", action)
	}
}

// close flushes pending events and shuts down the PostHog client.
func (a *auditLogger) close() {
	if a == nil {
		return
	}
	a.client.Close()
}

// posthogTracker implements pipeline.EventTracker using PostHog.
type posthogTracker struct {
	audit *auditLogger
}

func (t *posthogTracker) TrackReviewStarted(installationID int64, repo string, prNumber int, reviewID string, isIncremental bool, deepReview bool) {
	t.audit.logSettingsChange(context.Background(), "system", installationID, "review.started", map[string]interface{}{
		"repo": repo, "pr_number": prNumber, "review_id": reviewID,
		"is_incremental": isIncremental, "deep_review": deepReview,
	})
}

func (t *posthogTracker) TrackStageCompleted(installationID int64, repo string, prNumber int, reviewID string, stage string, durationMs int64) {
	t.audit.logSettingsChange(context.Background(), "system", installationID, "stage.completed", map[string]interface{}{
		"repo": repo, "pr_number": prNumber, "review_id": reviewID,
		"stage": stage, "duration_ms": durationMs,
	})
}

func (t *posthogTracker) TrackReviewCompleted(installationID int64, repo string, prNumber int, reviewID string, score int, commentCount int, durationMs int64) {
	t.audit.logSettingsChange(context.Background(), "system", installationID, "review.completed", map[string]interface{}{
		"repo": repo, "pr_number": prNumber, "review_id": reviewID,
		"score": score, "comment_count": commentCount, "duration_ms": durationMs,
	})
}

func (t *posthogTracker) TrackReviewFailed(installationID int64, repo string, prNumber int, reviewID string, stage string, errMsg string) {
	t.audit.logSettingsChange(context.Background(), "system", installationID, "review.failed", map[string]interface{}{
		"repo": repo, "pr_number": prNumber, "review_id": reviewID,
		"failed_stage": stage, "error": errMsg,
	})
}

// newEventTracker creates a pipeline.EventTracker backed by PostHog.
func newEventTracker(audit *auditLogger) *posthogTracker {
	return &posthogTracker{audit: audit}
}

// auditSettings logs a settings change to both PostHog and activity_log.
// installationID can be 0 for repo-scoped operations (looked up from context).
func (s *Server) auditSettings(r *http.Request, installationID int64, action string, props map[string]interface{}) {
	userID := getUserID(r.Context())

	// PostHog
	s.audit.logSettingsChange(r.Context(), userID, installationID, action, props)

	// activity_log
	metadata, _ := json.Marshal(props)
	instPtr := &installationID
	if installationID == 0 {
		instPtr = nil
	}
	if err := s.store.LogActivity(r.Context(), instPtr, "settings."+action, userID, "", metadata); err != nil {
		s.logger.Warn("audit activity_log write failed", "error", err, "action", action)
	}
}
