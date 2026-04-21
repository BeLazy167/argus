package obs

import "log/slog"

// AllowedKeys is the whitelist of slog attribute keys permitted to leave the
// process via PostHog. Anything not here is dropped in FilterAttrs. The
// allowlist_test AST walker fails CI if a new slog.String/Int/... call
// introduces a literal key that is not present here (or in DenyKeys).
var AllowedKeys = map[string]struct{}{
	// identity / scope
	"event": {}, "trace_id": {}, "review_id": {}, "installation_id": {}, "repo": {},
	"pr_number": {}, "user_id": {}, "github_login": {}, "delivery_id": {},

	// lifecycle
	"stage": {}, "status": {}, "action": {}, "trigger": {}, "deep_review": {},
	"score": {}, "comment_count": {}, "thread_id": {}, "threads_checked": {},
	"threads_attempted": {}, "threads_resolved": {}, "risks_found": {}, "linked_count": {},
	"issues_evaluated": {}, "reason": {}, "primary_review_id": {},

	// performance
	"duration_ms": {}, "tokens": {}, "prompt_tokens": {}, "completion_tokens": {},
	"cost_usd": {}, "rss_mb": {}, "age_minutes": {},

	// errors
	"error_class": {}, "status_code": {}, "kind": {}, "scope": {},
	"retry_after_ms": {}, "endpoint": {}, "reset_at": {},

	// provider
	"provider": {}, "model": {},

	// settings (redacted values only)
	"setting_key": {}, "new_value_redacted": {},
	// settings audit payloads — non-sensitive scalars surfaced on the
	// `settings.changed` event. Each maps to a concrete auditSettings call
	// site in handlers_config.go / handlers_features.go.
	"repo_id": {}, "key_id": {}, "key": {}, "persona": {},
	"issue_acceptance": {}, "cross_pr_checks": {}, "max_linked_prs": {},

	// system
	"signal": {}, "pending_reviews": {}, "rate_limit_kind": {},
	"panic_msg_redacted": {},
}

// DenyKeys are attribute keys we forcibly strip even when the caller believes
// the value is safe. Sourced from the original api/audit.go sensitiveKeys plus
// a few names that routinely leak prompt/response content.
var DenyKeys = map[string]struct{}{
	"api_key": {}, "api_key_enc": {}, "prompt_text": {},
	"custom_persona_prompt": {}, "password": {}, "secret": {},
	"token": {}, "key_hint": {},
}

// FilterAttrs walks the slog.Record's attrs and returns only keys in
// AllowedKeys and not in DenyKeys. The special "event" attr (used by the
// handler to promote a record into a named PostHog event) is included in the
// returned map so the caller can decide whether to strip it before emission.
// The record's msg field is never included — drop it at the call site.
//
// Nested slog.Group attrs are flattened one level: their subkeys are checked
// against the allowlist independently. Deeper nesting is not expected in our
// code and is intentionally not supported.
func FilterAttrs(r slog.Record) map[string]any {
	out := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		collectAttr(a, out)
		return true
	})
	return out
}

func collectAttr(a slog.Attr, out map[string]any) {
	if a.Value.Kind() == slog.KindGroup {
		for _, sub := range a.Value.Group() {
			collectAttr(sub, out)
		}
		return
	}
	key := a.Key
	if _, denied := DenyKeys[key]; denied {
		return
	}
	if _, ok := AllowedKeys[key]; !ok {
		return
	}
	out[key] = a.Value.Any()
}
