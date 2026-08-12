package api

import (
	"net/http"
	"strconv"
)

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.getStats")
	defer op.Finish(w)
	stats, err := s.store.GetStatsScoped(r.Context(), getInstallationIDs(r.Context()))
	if err != nil {
		s.logger.ErrorContext(r.Context(), "get stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.getActivity")
	defer op.Finish(w)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	activity, err := s.store.ListActivity(r.Context(), getInstallationIDs(r.Context()), limit)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list activity", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, activity)
}
