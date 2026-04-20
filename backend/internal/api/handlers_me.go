package api

import (
	"context"
	"net/http"
)

// meResponse is the wire shape returned by GET /api/me. The FE uses it to
// alias `github:<login>` → clerk_user_id in PostHog so webhook events
// (attributed to github:<pr_author>) merge with dashboard events (attributed
// to Clerk user id) into a single person on the platform.
//
// omitempty on GithubLogin lets the FE distinguish "user has no linked GitHub
// account" (field absent or null) from "user is logged in via GitHub" (field
// populated). For now the backend always returns null until Agent X wires the
// Clerk SDK lookup; the FE treats that as the fallback path.
type meResponse struct {
	ClerkUserID string  `json:"clerk_user_id"`
	Email       string  `json:"email,omitempty"`
	GithubLogin *string `json:"github_login,omitempty"`
}

// handleMe returns the current Clerk user's identity plus linked GitHub login.
//
// Route: GET /api/v1/me
//
// Auth: requires Clerk JWT (sits behind jwtAuth middleware). Unauthenticated
// callers never reach here — if the ctx lacks a user id we fall closed with
// 401 rather than leaking a stub response.
//
// The GitHub login source today is best-effort: we do not import a Clerk SDK,
// so there is no server-side lookup of external accounts. Fallback value is
// nil. The FE already reads externalAccounts client-side and calls posthog.alias
// before identify (see posthog-provider.tsx) — this endpoint exists so email-
// signup users (who lack externalAccounts) still get a stable identity merge
// once the Clerk SDK lookup is wired.
//
// TODO: integrate github.com/clerkinc/clerk-sdk-go and call
// client.Users().Read(userID).ExternalAccounts to populate github_login +
// email without a round-trip to the FE.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	resp := meResponse{
		ClerkUserID: userID,
		Email:       s.lookupEmail(r.Context(), userID),
		GithubLogin: s.lookupGithubLogin(r.Context(), userID),
	}
	writeJSON(w, http.StatusOK, resp)
}

// lookupGithubLogin returns the user's GitHub login when discoverable, or nil.
// Kept as a method (not a free function) so a future Clerk SDK wiring has a
// hook point without touching every caller.
func (s *Server) lookupGithubLogin(_ context.Context, _ string) *string {
	// TODO: Clerk SDK integration — see handleMe doc comment. Returning nil
	// is correct today: the FE's client-side alias covers GitHub-OAuth users,
	// and email-signup users cannot be merged until the server-side lookup
	// lands.
	return nil
}

// lookupEmail returns the user's primary email when available, or "".
// Same TODO as lookupGithubLogin — pending Clerk SDK integration.
func (s *Server) lookupEmail(_ context.Context, _ string) string {
	return ""
}
