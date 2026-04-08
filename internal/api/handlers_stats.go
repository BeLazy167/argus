package api

import (
	"net/http"
	"strconv"
)

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStatsScoped(r.Context(), getInstallationIDs(r.Context()))
	if err != nil {
		s.logger.Error("get stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	activity, err := s.store.ListActivity(r.Context(), getInstallationIDs(r.Context()), limit)
	if err != nil {
		s.logger.Error("list activity", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, activity)
}
