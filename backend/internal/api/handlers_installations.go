package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listMyInstallations(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	list, err := s.store.ListUserInstallations(r.Context(), userID)
	if err != nil {
		s.logger.Error("list user installations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) linkInstallation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	orgID := getOrgID(r.Context())
	var body struct {
		InstallationID int64  `json:"installation_id"`
		ClerkOrgID     string `json:"clerk_org_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	inst, err := s.store.GetInstallationByGitHubID(r.Context(), body.InstallationID)
	if err != nil {
		s.handleDBError(w, err, "installation not found")
		return
	}

	// Security: allow linking if:
	// 1. No users linked yet (first claim after GitHub App install), OR
	// 2. User is already linked (re-link/idempotent), OR
	// 3. User is in the same Clerk org as the installation (org member joining)
	//
	// DB errors on the auth path must fail closed — never fall through as a
	// first-owner claim when the claim-count or membership check errors out.
	claimedCount, countErr := s.store.CountInstallationUsers(r.Context(), inst.ID)
	if countErr != nil {
		s.logger.Error("count installation users", "error", countErr, "installation", inst.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate installation claim"})
		return
	}
	if claimedCount > 0 {
		alreadyLinked, linkErr := s.store.IsUserLinkedToInstallation(r.Context(), userID, inst.ID)
		if linkErr != nil {
			s.logger.Error("check user installation link", "error", linkErr, "installation", inst.ID, "user", userID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to validate installation claim"})
			return
		}
		if !alreadyLinked {
			// Not already linked — check if user is in the same Clerk org
			if inst.ClerkOrgID == nil || *inst.ClerkOrgID == "" || *inst.ClerkOrgID != orgID {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation already claimed — ask an existing member to invite you"})
				return
			}
		}
	}

	// Determine role: owner for first claim, org_member for subsequent org members
	role := "org_member"
	if claimedCount == 0 {
		role = "owner"
	}

	if body.ClerkOrgID != "" {
		if err := s.store.SetInstallationClerkOrgID(r.Context(), inst.ID, body.ClerkOrgID); err != nil {
			s.logger.Error("set clerk org id", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link org"})
			return
		}
	}
	ui, err := s.store.LinkUserInstallation(r.Context(), userID, inst.ID, role)
	if err != nil {
		s.logger.Error("link installation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link installation"})
		return
	}

	// Auto-sync repos so they appear immediately after linking
	go func() {
		syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if s.ghApp != nil {
			repos, listErr := s.ghApp.ListInstallationRepos(syncCtx, inst.InstallationID)
			if listErr != nil {
				s.logger.Warn("auto-sync repos after link failed", "error", listErr, "installation", inst.ID)
				return
			}
			for _, r := range repos {
				if _, upsertErr := s.store.UpsertRepo(syncCtx, inst.ID, r.GetID(), r.GetFullName(), r.GetDefaultBranch()); upsertErr != nil {
					s.logger.Warn("auto-sync upsert repo failed", "error", upsertErr, "repo", r.GetFullName())
				}
			}
			s.logger.Info("auto-synced repos after link", "count", len(repos), "installation", inst.ID)
		}
	}()

	writeJSON(w, http.StatusOK, ui)
}

func (s *Server) listInstallations(w http.ResponseWriter, r *http.Request) {
	s.listMyInstallations(w, r)
}

func (s *Server) getCurrentInstallation(w http.ResponseWriter, r *http.Request) {
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active installation"})
		return
	}
	inst, err := s.store.GetInstallation(r.Context(), ids[0])
	if err != nil {
		s.handleDBError(w, err, "installation not found")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// autoLinkInstallation matches a user's installation to the current Clerk org.
// Two paths:
//  1. Unlinked: installation matches org slug and clerk_org_id is not yet set → set it
//  2. Already linked: installation's clerk_org_id matches the current org → ensure user_installations row exists
//
// Called automatically by the frontend when a user is in a Clerk org but has no scoped installation.
func (s *Server) autoLinkInstallation(w http.ResponseWriter, r *http.Request) {
	orgID := getOrgID(r.Context())
	if orgID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no org context"})
		return
	}
	var body struct {
		OrgSlug string `json:"org_slug"` // Clerk org slug or name
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrgSlug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "org_slug required"})
		return
	}

	userID := getUserID(r.Context())

	// Path 1: Find installations already linked to this Clerk org.
	// This handles new org members whose user_installations row was auto-created
	// by resolveInstallationIDs but whose frontend hasn't refreshed yet.
	inst, err := s.store.GetInstallationByClerkOrgID(r.Context(), orgID)
	if err == nil && inst != nil {
		// Ensure the user has a user_installations row
		role := getOrgRole(r.Context())
		if role == "" {
			role = "org_member"
		}
		if _, linkErr := s.store.LinkUserInstallation(r.Context(), userID, inst.ID, role); linkErr != nil {
			s.logger.Warn("auto-link: ensure user_installations row", "error", linkErr)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "linked", "org_login": inst.OrgLogin})
		return
	}

	// Path 2: Find user's installations that match the org slug and are not yet linked to a Clerk org.
	installations, err := s.store.ListUserInstallations(r.Context(), userID)
	if err != nil {
		s.logger.Error("auto-link: list installations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	for _, inst := range installations {
		if strings.EqualFold(inst.OrgLogin, body.OrgSlug) && (inst.ClerkOrgID == nil || *inst.ClerkOrgID == "") {
			if err := s.store.SetInstallationClerkOrgID(r.Context(), inst.ID, orgID); err != nil {
				s.logger.Error("auto-link: set clerk_org_id", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link"})
				return
			}
			s.logger.Info("auto-linked installation to clerk org", "installation", inst.OrgLogin, "clerk_org_id", orgID)
			writeJSON(w, http.StatusOK, map[string]string{"status": "linked", "org_login": inst.OrgLogin})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "no_match"})
}

// getInstallURL returns a GitHub App install URL with suggested_target_id pre-selecting the org.
func (s *Server) getInstallURL(w http.ResponseWriter, r *http.Request) {
	orgName := r.URL.Query().Get("org")
	baseURL := "https://github.com/apps/argus-eye/installations/new"

	if orgName == "" {
		writeJSON(w, http.StatusOK, map[string]string{"url": baseURL})
		return
	}

	// Look up GitHub org ID by name (public API, no auth needed)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/orgs/"+orgName, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"url": baseURL})
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		// Org not found or API error — fall back to generic URL
		writeJSON(w, http.StatusOK, map[string]string{"url": baseURL})
		return
	}
	defer resp.Body.Close()

	var org struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&org); err != nil || org.ID == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"url": baseURL})
		return
	}

	// Return URL with suggested_target_id to pre-select the org
	url := fmt.Sprintf("%s/permissions?suggested_target_id=%d", baseURL, org.ID)
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) syncRepos(w http.ResponseWriter, r *http.Request) {
	installationDBID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}

	ids := getInstallationIDs(r.Context())
	found := false
	for _, id := range ids {
		if id == installationDBID {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation not in scope"})
		return
	}

	inst, err := s.store.GetInstallation(r.Context(), installationDBID)
	if err != nil {
		s.handleDBError(w, err, "installation not found")
		return
	}

	repos, err := s.ghApp.ListInstallationRepos(r.Context(), inst.InstallationID)
	if err != nil {
		s.logger.Error("sync repos: list from github", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to list repos from GitHub"})
		return
	}

	var count int
	for _, repo := range repos {
		_, err := s.store.UpsertRepo(r.Context(), installationDBID, repo.GetID(), repo.GetFullName(), repo.GetDefaultBranch())
		if err != nil {
			s.logger.Warn("sync repo upsert failed", "error", err, "repo", repo.GetFullName())
			continue
		}
		count++
	}

	writeJSON(w, http.StatusOK, SyncReposResponse{Synced: count})
}
