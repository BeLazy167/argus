package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/internal/store/db"
)

func (s *Server) getGraph(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	nodes, err := s.store.Q.ListGraphNodes(r.Context(), repoID)
	if err != nil {
		s.logger.Error("get graph nodes", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load graph nodes"})
		return
	}

	edges, err := s.store.Q.ListGraphEdges(r.Context(), repoID)
	if err != nil {
		s.logger.Error("get graph edges", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load graph edges"})
		return
	}

	// Ensure non-null JSON arrays (nil slices serialize as null)
	if nodes == nil {
		nodes = []db.ListGraphNodesRow{}
	}
	if edges == nil {
		edges = []db.ListGraphEdgesRow{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"edges": edges,
	})
}

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
