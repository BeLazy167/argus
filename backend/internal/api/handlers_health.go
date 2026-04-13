package api

import (
	"net/http"
	"time"
)

func (s *Server) patternHealth(w http.ResponseWriter, r *http.Request) {
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no installation scope"})
		return
	}
	installationID := ids[0]

	since := time.Now().Add(-48 * time.Hour)
	stats, err := s.store.GetPatternHealthStats(r.Context(), installationID, since)
	if err != nil {
		s.logger.Error("pattern health", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	status := http.StatusOK
	if stats.LastLearnedAt != nil && time.Since(*stats.LastLearnedAt) > 48*time.Hour && stats.ReviewsProcessed > 5 {
		status = http.StatusServiceUnavailable
	}
	if stats.LastLearnedAt == nil && stats.ReviewsProcessed > 5 {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, stats)
}
