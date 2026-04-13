package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listPricing(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Pool.Query(r.Context(), `SELECT model_pattern, input_per_million, output_per_million FROM model_pricing ORDER BY model_pattern`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list pricing"})
		return
	}
	defer rows.Close()

	type entry struct {
		Pattern string  `json:"model_pattern"`
		Input   float64 `json:"input_per_million"`
		Output  float64 `json:"output_per_million"`
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Pattern, &e.Input, &e.Output); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) upsertPricing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pattern string  `json:"model_pattern"`
		Input   float64 `json:"input_per_million"`
		Output  float64 `json:"output_per_million"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Pattern == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_pattern required"})
		return
	}

	_, err := s.store.Pool.Exec(r.Context(), `
		INSERT INTO model_pricing (model_pattern, input_per_million, output_per_million)
		VALUES ($1, $2, $3)
		ON CONFLICT (model_pattern) DO UPDATE SET
			input_per_million = EXCLUDED.input_per_million,
			output_per_million = EXCLUDED.output_per_million,
			updated_at = NOW()
	`, body.Pattern, body.Input, body.Output)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to upsert pricing"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) deletePricing(w http.ResponseWriter, r *http.Request) {
	pattern, err := url.PathUnescape(chi.URLParam(r, "pattern"))
	if err != nil || pattern == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pattern"})
		return
	}
	ct, err := s.store.Pool.Exec(r.Context(), `DELETE FROM model_pricing WHERE model_pattern = $1`, pattern)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete"})
		return
	}
	if ct.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
