package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filePath := r.URL.Query().Get("file")

	if filePath != "" {
		traces, err := s.store.ListTracesForFiles(r.Context(), repoID, []string{filePath}, limit)
		if err != nil {
			s.logger.Error("list traces for file", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, traces)
		return
	}

	traces, err := s.store.ListTracesForRepo(r.Context(), repoID, limit)
	if err != nil {
		s.logger.Error("list traces for repo", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, traces)
}

func (s *Server) getRepoRisk(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	hotFiles, err := s.store.GetHotFiles(r.Context(), repoID, limit)
	if err != nil {
		s.logger.Error("get hot files", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, hotFiles)
}
