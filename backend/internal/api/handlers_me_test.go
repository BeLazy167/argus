package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleMeShape guards the /api/me wire contract — FE consumers rely on
// clerk_user_id always present, github_login omitted when nil, and email
// omitted when empty. Breaking either key silently mismerges PostHog identities.
func TestHandleMeShape(t *testing.T) {
	s := &Server{}

	t.Run("authenticated returns clerk_user_id and omits empty fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		// Simulate what jwtAuth does — stash the user id on ctx.
		req = req.WithContext(context.WithValue(req.Context(), userIDKey, "user_abc123"))
		rec := httptest.NewRecorder()

		s.handleMe(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q, want application/json", got)
		}

		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got := body["clerk_user_id"]; got != "user_abc123" {
			t.Fatalf("clerk_user_id = %v, want user_abc123", got)
		}
		// email + github_login are omitempty when empty — they must not appear.
		if _, has := body["email"]; has {
			t.Fatalf("email present in response when empty — omitempty broken")
		}
		if _, has := body["github_login"]; has {
			t.Fatalf("github_login present when nil — omitempty broken")
		}
	})

	t.Run("missing user id returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		rec := httptest.NewRecorder()

		s.handleMe(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}
