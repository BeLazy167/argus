// Package api — handlers_features.go exposes per-installation feature flag
// endpoints backing the Settings → Features UI (issue acceptance toggle,
// cross-repo PR checks toggle, max linked PRs cap).
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/internal/pipeline"
	"github.com/BeLazy167/argus/internal/store/db"
)

// featureFlagsResponse is what the settings page consumes. Includes the
// computed/defaulted values so the UI never has to remember defaults.
type featureFlagsResponse struct {
	IssueAcceptance bool `json:"issue_acceptance"`
	CrossPRChecks   bool `json:"cross_pr_checks"`
	MaxLinkedPRs    int  `json:"max_linked_prs"`
}

func defaultFeatureFlags() featureFlagsResponse {
	d := pipeline.DefaultFeatureFlags()
	return featureFlagsResponse{
		IssueAcceptance: d.IssueAcceptance,
		CrossPRChecks:   d.CrossPRChecks,
		MaxLinkedPRs:    d.MaxLinkedPRs,
	}
}

func parseFeatureFlags(raw json.RawMessage) featureFlagsResponse {
	out := defaultFeatureFlags()
	if len(raw) == 0 || string(raw) == "{}" {
		return out
	}
	// Structural unmarshal into a generic map so missing fields keep defaults.
	var partial struct {
		IssueAcceptance *bool `json:"issue_acceptance"`
		CrossPRChecks   *bool `json:"cross_pr_checks"`
		MaxLinkedPRs    *int  `json:"max_linked_prs"`
	}
	if err := json.Unmarshal(raw, &partial); err != nil {
		return out
	}
	if partial.IssueAcceptance != nil {
		out.IssueAcceptance = *partial.IssueAcceptance
	}
	if partial.CrossPRChecks != nil {
		out.CrossPRChecks = *partial.CrossPRChecks
	}
	if partial.MaxLinkedPRs != nil && *partial.MaxLinkedPRs > 0 {
		out.MaxLinkedPRs = *partial.MaxLinkedPRs
	}
	return out
}

func (s *Server) getFeatureFlags(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	raw, err := s.store.Q.GetInstallationFeatureFlags(r.Context(), installationID)
	if err != nil {
		s.logger.Error("fetching feature flags", "error", err, "installation_id", installationID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, parseFeatureFlags(raw))
}

func (s *Server) setFeatureFlags(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	var body featureFlagsResponse
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	// Clamp max_linked_prs to safe range.
	if body.MaxLinkedPRs < 1 {
		body.MaxLinkedPRs = 5
	}
	if body.MaxLinkedPRs > 20 {
		body.MaxLinkedPRs = 20
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal failed"})
		return
	}
	if err := s.store.Q.UpdateInstallationFeatureFlags(r.Context(), db.UpdateInstallationFeatureFlagsParams{
		ID:           installationID,
		FeatureFlags: raw,
	}); err != nil {
		s.logger.Error("updating feature flags", "error", err, "installation_id", installationID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}
	s.auditSettings(r, installationID, "feature_flags.update", map[string]interface{}{
		"issue_acceptance": body.IssueAcceptance, "cross_pr_checks": body.CrossPRChecks, "max_linked_prs": body.MaxLinkedPRs,
	})
	writeJSON(w, http.StatusOK, body)
}
