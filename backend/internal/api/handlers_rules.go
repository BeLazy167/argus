package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// syncRuleMirror applies the rule's current enabled state to memory. Delete is
// a soft tombstone and IndexRule resurrects the same deterministic custom ID,
// so disable/re-enable and webhook retries are reversible and idempotent.
func syncRuleMirror(ctx context.Context, indexer memory.Indexer, rule memory.RuleMemory, enabled bool) error {
	if enabled {
		return indexer.IndexRule(ctx, "", rule)
	}
	return indexer.DeleteDocument(ctx, memory.RuleCustomID(rule.RuleID))
}

// mirrorRule best-effort mirrors one relational rule transition into memory.
// The relational row is authoritative; a mirror error never fails the request.
func (s *Server) mirrorRule(ctx context.Context, installationID int64, rule memory.RuleMemory, enabled bool) {
	if s.memRegistry == nil {
		return
	}
	indexer := s.memRegistry.GetIndexer(ctx, installationID)
	if indexer == nil {
		return
	}
	smCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := syncRuleMirror(smCtx, indexer, rule, enabled); err != nil {
		s.logger.Warn("sync rule memory mirror", "error", err, "rule_id", rule.RuleID, "enabled", enabled)
	}
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRules(r.Context(), getInstallationIDs(r.Context()))
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
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation"})
		return
	}
	rule, err := s.store.CreateRule(r.Context(), ids[0], body.Category, body.Content, body.Priority, enabled)
	if err != nil {
		s.logger.Error("create rule", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create rule"})
		return
	}
	if err := s.store.LogActivity(r.Context(), &ids[0], "rule_created", "", fmt.Sprintf("rule:%d", rule.ID), nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "rule_created")
	}
	s.mirrorRule(r.Context(), ids[0], memory.RuleMemory{
		RuleID:   rule.ID,
		Category: rule.Category,
		Priority: rule.Priority,
		Content:  rule.Content,
	}, rule.Enabled)
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
	rule, err := s.store.UpdateRule(r.Context(), id, getInstallationIDs(r.Context()), body.Category, body.Content, body.Priority, body.Enabled)
	if err != nil {
		s.handleDBError(w, err, "rule not found")
		return
	}
	if rule.InstallationID != nil {
		s.mirrorRule(r.Context(), *rule.InstallationID, memory.RuleMemory{
			RuleID:   rule.ID,
			Category: rule.Category,
			Priority: rule.Priority,
			Content:  rule.Content,
		}, rule.Enabled)
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "ruleID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rule id"})
		return
	}
	ids := getInstallationIDs(r.Context())
	if err := s.store.DeleteRule(r.Context(), id, ids); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	}
	if err := s.store.LogActivity(r.Context(), nil, "rule_deleted", "", fmt.Sprintf("rule:%d", id), nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "rule_deleted")
	}
	// DeleteRule scoped the row to ids and confirmed authorization; the rule
	// was created under ids[0] (see createRule), so its `_shared` doc lives in
	// that installation's container. Best-effort cleanup by deterministic customID.
	if len(ids) > 0 {
		s.mirrorRule(r.Context(), ids[0], memory.RuleMemory{RuleID: id}, false)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
