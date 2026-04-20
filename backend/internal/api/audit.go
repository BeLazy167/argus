package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// auditLogger is a thin wrapper over the structured logger. Historically it
// owned its own PostHog client and fired `settings_changed` directly. The
// whole PostHog path now goes through obs.Handler — every slog record with
// an `event=` attr is forwarded centrally. This type survives only because
// Server.audit is wired from NewServer and is the carrier for the settings
// slog call; there is no separate PostHog client here any more.
type auditLogger struct {
	logger *slog.Logger
}

// newAuditLogger builds an auditLogger bound to the app logger. Non-nil even
// when POSTHOG_API_KEY is unset — the kill switch lives in obs.Handler, not
// here, so disabling PostHog must not disable activity_log or settings slog
// events.
func newAuditLogger(logger *slog.Logger) *auditLogger {
	return &auditLogger{logger: logger}
}

// logSettingsChange emits a structured `settings.changed` slog record. The
// record is forwarded to PostHog by obs.Handler and to stdout by the inner
// JSON handler. Properties flow through obs.FilterAttrs — any key outside
// obs.AllowedKeys is dropped before it leaves the process, and obs.DenyKeys
// takes precedence (api_key, prompt_text, etc.). We still pre-filter here
// against DenyKeys so we never serialize a denied value into a slog.Attr at
// all, matching the invariant that a denied key never appears on a Record.
func (a *auditLogger) logSettingsChange(ctx context.Context, clerkUserID string, installationID int64, action string, properties map[string]any) {
	if a == nil {
		return
	}
	attrs := make([]slog.Attr, 0, 5+len(properties))
	attrs = append(attrs,
		slog.String("event", "settings.changed"),
		slog.String("action", action),
		slog.Int64("installation_id", installationID),
		slog.String("user_id", clerkUserID),
	)
	for k, v := range properties {
		if _, denied := obs.DenyKeys[k]; denied {
			continue
		}
		if _, allowed := obs.AllowedKeys[k]; !allowed {
			// Silently dropped; the handler would drop it anyway, and
			// attaching would inflate the record for the stdout JSON
			// sink without landing anywhere useful.
			continue
		}
		attrs = append(attrs, slog.Any(k, v))
	}
	a.logger.LogAttrs(ctx, slog.LevelInfo, "settings changed", attrs...)
}

// close is kept for API compatibility with Server.Close(); no-op now that the
// PostHog client is owned centrally by obs.Handler.
func (a *auditLogger) close() {}

// auditSettings logs a settings change to both the structured slog path
// (→ PostHog via obs.Handler) and activity_log.
// installationID can be 0 for repo-scoped operations (looked up from context).
func (s *Server) auditSettings(r *http.Request, installationID int64, action string, props map[string]any) {
	userID := getUserID(r.Context())

	s.audit.logSettingsChange(r.Context(), userID, installationID, action, props)

	metadata, _ := json.Marshal(props)
	instPtr := &installationID
	if installationID == 0 {
		instPtr = nil
	}
	if err := s.store.LogActivity(r.Context(), instPtr, "settings."+action, userID, "", metadata); err != nil {
		s.logger.Warn("audit activity_log write failed", "error", err, "action", action)
	}
}
