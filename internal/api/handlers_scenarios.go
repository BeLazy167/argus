package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	scenarios, err := s.store.ListScenariosForRepo(r.Context(), repoID, 100)
	if err != nil {
		s.logger.Error("list scenarios", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, scenarios)
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	repo, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	var body struct {
		Description string   `json:"description"`
		Source      string   `json:"source"`
		SourceRef   string   `json:"source_ref"`
		Files       []string `json:"files"`
		Modules     []string `json:"modules"`
		Severity    string   `json:"severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Description == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description required"})
		return
	}
	if body.Source == "" {
		body.Source = "manual"
	}
	if body.Severity == "" {
		body.Severity = "medium"
	}
	id, err := s.store.CreateScenario(r.Context(), repo.InstallationID, &repoID, body.Description, body.Source, body.SourceRef, body.Files, body.Modules, body.Severity)
	if err != nil {
		s.logger.Error("create scenario", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create scenario"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) deactivateScenario(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "scenarioID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid scenario id"})
		return
	}
	// Verify scenario belongs to user's installations before deactivating
	if err := s.store.DeactivateScenarioScoped(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}
