package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) getFileMemory(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	filePath := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if filePath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file path required"})
		return
	}

	mem, err := s.store.GetFileMemory(r.Context(), repoID, filePath)
	if err != nil {
		s.logger.Error("get file memory", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load file memory"})
		return
	}
	writeJSON(w, http.StatusOK, mem)
}
