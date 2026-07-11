package api

import (
	"net/http"
)

// statsGauge exposes vw_review_gauge — the Gauge's address-rate telemetry
// (human-weighted address rate, dismiss rate, median time-to-merge per
// category per change_class) — scoped to the caller's installations. Read by
// the future dashboard Gauge panel.
func (s *Server) statsGauge(w http.ResponseWriter, r *http.Request) {
	instIDs := getInstallationIDs(r.Context())
	rows, err := s.store.ListReviewGauge(r.Context(), instIDs)
	if err != nil {
		s.logger.Error("stats gauge", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gauge": rows})
}
