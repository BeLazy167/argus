package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules(r.Context())
	if err != nil {
		s.logger.Error("list rules", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Category string `json:"category"`
		Content  string `json:"content"`
		Priority int    `json:"priority"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Category == "" || body.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "category and content required"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	rule, err := s.store.CreateRule(r.Context(), body.Category, body.Content, body.Priority, enabled)
	if err != nil {
		s.logger.Error("create rule", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create rule"})
		return
	}
	if err := s.store.LogActivity(r.Context(), "rule_created", "", fmt.Sprintf("rule:%d", rule.ID), nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "rule_created")
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "ruleID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	var body struct {
		Category *string `json:"category"`
		Content  *string `json:"content"`
		Priority *int    `json:"priority"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	rule, err := s.store.UpdateRule(r.Context(), id, body.Category, body.Content, body.Priority, body.Enabled)
	if err != nil {
		s.handleDBError(w, err, "rule not found")
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "ruleID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	if err := s.store.DeleteRule(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}
	if err := s.store.LogActivity(r.Context(), "rule_deleted", "", fmt.Sprintf("rule:%d", id), nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "rule_deleted")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
