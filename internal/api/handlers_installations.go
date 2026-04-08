package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
	// Security: only allow linking if the user is already linked (re-link/idempotent)
	// or if no users are linked yet (first claim after GitHub App install).
	var claimedCount int
	_ = s.store.Pool.QueryRow(r.Context(), "SELECT count(*) FROM user_installations WHERE installation_id = $1", inst.ID).Scan(&claimedCount)
	if claimedCount > 0 {
		var alreadyLinked int
		_ = s.store.Pool.QueryRow(r.Context(), "SELECT count(*) FROM user_installations WHERE installation_id = $1 AND clerk_user_id = $2", inst.ID, userID).Scan(&alreadyLinked)
		if alreadyLinked == 0 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation already claimed — ask an existing member to invite you"})
			return
		}
	}
	if body.ClerkOrgID != "" {
		if err := s.store.SetInstallationClerkOrgID(r.Context(), inst.ID, body.ClerkOrgID); err != nil {
			s.logger.Error("set clerk org id", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link org"})
			return
		}
	}
	ui, err := s.store.LinkUserInstallation(r.Context(), userID, inst.ID, "owner")
	if err != nil {
		s.logger.Error("link installation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link installation"})
		return
	}
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

// autoLinkInstallation matches a user's unlinked installation to the current Clerk org
// by comparing org_login to the Clerk org slug/name. Called automatically by the frontend.
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

	// Find user's installations that match the org slug
	userID := getUserID(r.Context())
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

	writeJSON(w, http.StatusOK, map[string]any{"synced": count})
}
