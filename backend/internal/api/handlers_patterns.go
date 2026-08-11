package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

func (s *Server) listPatterns(w http.ResponseWriter, r *http.Request) {
	ids := getInstallationIDs(r.Context())
	var patterns []store.Pattern
	var err error
	if rid := r.URL.Query().Get("repo_id"); rid != "" {
		repoID, parseErr := strconv.ParseInt(rid, 10, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo_id"})
			return
		}
		patterns, err = s.store.ListPatternsForRepo(r.Context(), ids, repoID)
	} else {
		patterns, err = s.store.ListPatterns(r.Context(), ids)
	}
	if err != nil {
		s.logger.Error("list patterns", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, patterns)
}

func (s *Server) getPatternStats(w http.ResponseWriter, r *http.Request) {
	// Default is every installation the caller belongs to; an explicit
	// installation_id narrows to one workspace so org-scoped pages don't mix
	// another installation's history into the timeline.
	ids := getInstallationIDs(r.Context())
	if iid := r.URL.Query().Get("installation_id"); iid != "" {
		id, parseErr := strconv.ParseInt(iid, 10, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation_id"})
			return
		}
		if !containsID(ids, id) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
			return
		}
		ids = []int64{id}
	}
	stats, err := s.store.GetPatternStats(r.Context(), ids)
	if err != nil {
		s.logger.Error("get pattern stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) getPattern(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "patternID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pattern id"})
		return
	}
	pattern, err := s.store.GetPattern(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "pattern not found")
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, pattern.InstallationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	writeJSON(w, http.StatusOK, pattern)
}

func (s *Server) createPattern(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstallationID int64  `json:"installation_id"`
		RepoID         *int64 `json:"repo_id"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Content == "" || body.InstallationID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation_id and content required"})
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, body.InstallationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	var repoName string
	if body.RepoID != nil {
		dbRepo, repoErr := s.store.GetRepoScoped(r.Context(), *body.RepoID, []int64{body.InstallationID})
		if repoErr != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
			return
		}
		parts := strings.SplitN(dbRepo.FullName, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		repoName = parts[1]
	}
	source := "manual"
	customID := memory.SharedPatternCustomID(source, body.Content)
	if body.RepoID != nil {
		customID = memory.PatternCustomID("", repoName, source, body.Content)
	}

	createdBy := getUserID(r.Context())
	pattern, err := s.store.CreatePattern(r.Context(), body.InstallationID, body.RepoID, body.Content, nil, &createdBy, &source, nil, nil, &customID)
	if err != nil {
		s.logger.Error("create pattern", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create pattern"})
		return
	}
	writeJSON(w, http.StatusCreated, pattern)
}

func (s *Server) deletePattern(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "patternID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pattern id"})
		return
	}

	if err := s.store.DeletePattern(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pattern not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
