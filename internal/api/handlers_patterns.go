package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
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
	stats, err := s.store.GetPatternStats(r.Context(), getInstallationIDs(r.Context()))
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

	// Index in Supermemory (respect repo scope)
	var smID *string
	if s.indexer != nil {
		inst, err := s.store.GetInstallation(r.Context(), body.InstallationID)
		if err != nil {
			s.logger.Error("create pattern: lookup installation", "error", err)
		} else {
			metadata := map[string]string{"source": "dashboard"}
			var resp *memory.AddResponse
			if body.RepoID != nil {
				dbRepo, err := s.store.GetRepo(r.Context(), *body.RepoID)
				if err == nil {
					parts := strings.SplitN(dbRepo.FullName, "/", 2)
					if len(parts) == 2 {
						resp, err = s.indexer.IndexRepoPattern(r.Context(), parts[0], parts[1], body.Content, "", metadata)
					}
				}
			} else {
				resp, err = s.indexer.IndexOwnerPattern(r.Context(), inst.OrgLogin, body.Content, "", metadata)
			}
			if err != nil {
				s.logger.Error("index pattern in supermemory", "error", err)
			} else if resp != nil {
				smID = &resp.ID
			}
		}
	}

	createdBy := getUserID(r.Context())
	pattern, err := s.store.CreatePattern(r.Context(), body.InstallationID, body.RepoID, body.Content, smID, &createdBy, nil, nil, nil)
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

	// Fetch pattern for Supermemory cleanup (scoped to user's installations)
	pattern, getErr := s.store.GetPattern(r.Context(), id)

	// Delete from DB first (scoped auth check)
	if err := s.store.DeletePattern(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pattern not found"})
		return
	}

	// Only delete from Supermemory after DB deletion succeeds (confirms authorization)
	if getErr == nil && pattern.SupermemoryID != nil && s.indexer != nil {
		if err := s.indexer.DeleteDocument(r.Context(), *pattern.SupermemoryID); err != nil {
			s.logger.Error("delete pattern from supermemory", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
