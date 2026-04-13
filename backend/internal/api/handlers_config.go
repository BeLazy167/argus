package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

// verifyRepoAccess parses repoID from URL and verifies it belongs to the caller's installations.
func (s *Server) verifyRepoAccess(w http.ResponseWriter, r *http.Request) (int64, bool) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return 0, false
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return 0, false
	}
	return repoID, true
}

// --- Model Config ---

func (s *Server) getModelConfigs(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	configs, err := s.store.ListModelConfigs(r.Context(), repoID)
	if err != nil {
		s.logger.Error("list model configs", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) upsertModelConfig(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	stage := chi.URLParam(r, "stage")
	validStages := map[string]bool{"triage": true, "review": true, "synthesis": true, "embedding": true, "scoring": true}
	if !validStages[stage] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stage must be triage, review, synthesis, embedding, or scoring"})
		return
	}
	var body struct {
		Provider    string  `json:"provider"`
		Model       string  `json:"model"`
		BaseURL     *string `json:"base_url"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float32 `json:"temperature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Provider == "" || body.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and model required"})
		return
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = 4096
	}
	if body.Temperature < 0 {
		body.Temperature = 0
	}
	if body.Temperature > 2.0 {
		body.Temperature = 2.0
	}
	cfg, err := s.store.UpsertModelConfig(r.Context(), repoID, stage, body.Provider, body.Model, body.BaseURL, body.MaxTokens, body.Temperature)
	if err != nil {
		s.logger.Error("upsert model config", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save config"})
		return
	}
	s.auditSettings(r, 0, "model_config.upsert", map[string]interface{}{"stage": stage, "provider": body.Provider, "repo_id": repoID})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) deleteModelConfig(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	stage := chi.URLParam(r, "stage")
	if err := s.store.DeleteModelConfig(r.Context(), repoID, stage); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
		return
	}
	s.auditSettings(r, 0, "model_config.delete", map[string]interface{}{"stage": stage, "repo_id": repoID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// testConfig sends a minimal LLM request to verify API key + model work end-to-end.
func (s *Server) testConfig(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Provider == "" || body.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and model required"})
		return
	}

	provider, err := s.registry.GetProviderForRepo(r.Context(), installationID, nil, body.Provider)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("key resolution failed: %s", err)})
		return
	}

	start := time.Now()
	resp, err := provider.Complete(r.Context(), llm.CompletionRequest{
		Model:       body.Model,
		System:      "Respond with exactly: ok",
		Messages:    []llm.Message{{Role: "user", Content: "ping"}},
		MaxTokens:   32,
		Temperature: 0,
	})
	latency := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"error":      err.Error(),
			"latency_ms": latency,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"response":   resp.Content,
		"latency_ms": latency,
		"tokens":     resp.TokensUsed.TotalTokens,
	})
}

// --- Org Model Config ---

func (s *Server) getOrgModelConfigs(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	configs, err := s.store.ListOrgModelConfigs(r.Context(), installationID)
	if err != nil {
		s.logger.Error("list org model configs", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) upsertOrgModelConfig(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	stage := chi.URLParam(r, "stage")
	validStages := map[string]bool{"triage": true, "review": true, "synthesis": true, "embedding": true, "scoring": true}
	if !validStages[stage] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stage must be triage, review, synthesis, embedding, or scoring"})
		return
	}
	var body struct {
		Provider    string  `json:"provider"`
		Model       string  `json:"model"`
		BaseURL     *string `json:"base_url"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float32 `json:"temperature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Provider == "" || body.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and model required"})
		return
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = 4096
	}
	if body.Temperature < 0 {
		body.Temperature = 0
	}
	if body.Temperature > 2.0 {
		body.Temperature = 2.0
	}
	cfg, err := s.store.UpsertOrgModelConfig(r.Context(), installationID, stage, body.Provider, body.Model, body.BaseURL, body.MaxTokens, body.Temperature)
	if err != nil {
		s.logger.Error("upsert org model config", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save config"})
		return
	}
	s.auditSettings(r, installationID, "org_model_config.upsert", map[string]interface{}{"stage": stage, "provider": body.Provider})
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) deleteOrgModelConfig(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	stage := chi.URLParam(r, "stage")
	if err := s.store.DeleteOrgModelConfig(r.Context(), installationID, stage); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
		return
	}
	s.auditSettings(r, installationID, "org_model_config.delete", map[string]interface{}{"stage": stage})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Prompt Templates ---

func (s *Server) listPromptTemplates(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	customs, err := s.store.ListPromptTemplates(r.Context(), repoID)
	if err != nil {
		s.logger.Error("list prompt templates", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	customMap := make(map[string]string, len(customs))
	for _, c := range customs {
		customMap[c.Stage] = c.PromptText
	}
	defaults := pipeline.DefaultPrompts()
	type entry struct {
		Stage      string `json:"stage"`
		PromptText string `json:"prompt_text"`
		IsCustom   bool   `json:"is_custom"`
	}
	var result []entry
	for stage, defaultText := range defaults {
		if custom, ok := customMap[stage]; ok {
			result = append(result, entry{Stage: stage, PromptText: custom, IsCustom: true})
		} else {
			result = append(result, entry{Stage: stage, PromptText: defaultText, IsCustom: false})
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) upsertPromptTemplate(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	stage := chi.URLParam(r, "stage")
	if !pipeline.ValidPromptStages[stage] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stage"})
		return
	}
	var body struct {
		PromptText string `json:"prompt_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.PromptText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt_text required"})
		return
	}
	validated, errMsg := pipeline.ValidateCustomPrompt(body.PromptText)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	repo, repoErr := s.store.GetRepo(r.Context(), repoID)
	if repoErr != nil {
		s.handleDBError(w, repoErr, "repo not found")
		return
	}
	tier, _ := s.store.GetPlanTier(r.Context(), repo.InstallationID)
	if tier != "pro" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Custom prompts require Pro plan."})
		return
	}
	pt, err := s.store.UpsertPromptTemplate(r.Context(), repoID, stage, validated)
	if err != nil {
		s.logger.Error("upsert prompt template", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save prompt"})
		return
	}
	s.auditSettings(r, repo.InstallationID, "prompt.upsert", map[string]interface{}{"stage": stage, "repo_id": repoID})
	writeJSON(w, http.StatusOK, pt)
}

func (s *Server) deletePromptTemplate(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	stage := chi.URLParam(r, "stage")
	if err := s.store.DeletePromptTemplate(r.Context(), repoID, stage); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "prompt template not found"})
		return
	}
	s.auditSettings(r, 0, "prompt.delete", map[string]interface{}{"stage": stage, "repo_id": repoID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) listDefaultPrompts(w http.ResponseWriter, _ *http.Request) {
	defaults := pipeline.DefaultPrompts()
	type entry struct {
		Stage      string `json:"stage"`
		PromptText string `json:"prompt_text"`
	}
	var result []entry
	for stage, text := range defaults {
		result = append(result, entry{Stage: stage, PromptText: text})
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Provider Keys ---

func (s *Server) listProviderKeys(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	// Verify user has access to this installation
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	keys, err := s.store.ListProviderKeys(r.Context(), installationID)
	if err != nil {
		s.logger.Error("list provider keys", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	// Mask keys — show only last 4 chars
	type maskedKey struct {
		ID             int64   `json:"id"`
		InstallationID int64   `json:"installation_id"`
		RepoID         *int64  `json:"repo_id,omitempty"`
		Provider       string  `json:"provider"`
		APIKeyMasked   string  `json:"api_key_masked"`
		BaseURL        *string `json:"base_url,omitempty"`
		CreatedAt      string  `json:"created_at"`
		UpdatedAt      string  `json:"updated_at"`
	}
	result := make([]maskedKey, len(keys))
	for i, k := range keys {
		result[i] = maskedKey{
			ID:             k.ID,
			InstallationID: k.InstallationID,
			RepoID:         k.RepoID,
			Provider:       k.Provider,
			APIKeyMasked:   maskKey(k.KeyHint),
			BaseURL:        k.BaseURL,
			CreatedAt:      k.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:      k.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) upsertProviderKey(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	var body struct {
		RepoID   *int64  `json:"repo_id"`
		Provider string  `json:"provider"`
		APIKey   string  `json:"api_key"`
		BaseURL  *string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Provider == "" || body.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and api_key required"})
		return
	}
	if body.Provider == "azure" || body.Provider == "gcp_vertex" || body.Provider == "aws_bedrock" {
		if body.BaseURL != nil {
			trimmed := strings.TrimSpace(*body.BaseURL)
			body.BaseURL = &trimmed
		}
		if body.BaseURL == nil || *body.BaseURL == "" {
			msgs := map[string]string{
				"azure":       "Azure requires a base URL. OpenAI models: https://{resource}.openai.azure.com/openai — Foundry models: https://{endpoint}.inference.ai.azure.com/v1",
				"gcp_vertex":  "GCP Vertex requires a base URL: https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/endpoints/openapi",
				"aws_bedrock": "AWS Bedrock requires a base URL: https://bedrock-runtime.{region}.amazonaws.com",
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msgs[body.Provider]})
			return
		}
		u := strings.TrimRight(*body.BaseURL, "/")
		if body.Provider == "azure" && strings.Contains(u, ".openai.azure.com") {
			if parsed, err := url.Parse(u); err == nil && !strings.HasSuffix(parsed.Path, "/openai") {
				parsed.Path = strings.TrimRight(parsed.Path, "/") + "/openai"
				u = parsed.String()
			}
		}
		body.BaseURL = &u
	}
	pk, err := s.store.UpsertProviderKey(r.Context(), installationID, body.RepoID, body.Provider, body.APIKey, body.BaseURL)
	if err != nil {
		s.logger.Error("upsert provider key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save key"})
		return
	}
	s.auditSettings(r, installationID, "provider_key.upsert", map[string]interface{}{"provider": body.Provider})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              pk.ID,
		"installation_id": pk.InstallationID,
		"repo_id":         pk.RepoID,
		"provider":        pk.Provider,
		"api_key_masked":  maskKey(pk.KeyHint),
		"base_url":        pk.BaseURL,
	})
}

func (s *Server) deleteProviderKey(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	keyID, err := strconv.ParseInt(chi.URLParam(r, "keyID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key id"})
		return
	}
	if err := s.store.DeleteProviderKey(r.Context(), keyID, installationID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}
	s.auditSettings(r, installationID, "provider_key.delete", map[string]interface{}{"key_id": keyID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Org Default Settings ---

func (s *Server) getOrgDefaults(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	settings, err := s.store.GetOrgDefaults(r.Context(), installationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(settings)
}

func (s *Server) setOrgDefaults(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	var body struct {
		Persona             string `json:"persona,omitempty"`
		CustomPersonaPrompt string `json:"custom_persona_prompt,omitempty"`
		DeepReview          *bool  `json:"deep_review,omitempty"`
		CrossFileContext    *bool  `json:"cross_file_context,omitempty"`
		BlastRadius         *bool  `json:"blast_radius,omitempty"`
		ScenarioMemory      *bool  `json:"scenario_memory,omitempty"`
		CodeSimulation      *bool  `json:"code_simulation,omitempty"`
		PREnrichment        *bool  `json:"pr_enrichment,omitempty"`
		LearnPatterns       *bool  `json:"learn_patterns,omitempty"`
		LearnConventions    *bool  `json:"learn_conventions,omitempty"`
		FileSynthesis       *bool  `json:"file_synthesis,omitempty"`
		ArchitectureGraph   *bool  `json:"architecture_graph,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.Persona != "" {
		p := pipeline.Persona(body.Persona)
		if !pipeline.ValidPersonas[p] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid persona"})
			return
		}
	}
	settings, err := json.Marshal(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal failed"})
		return
	}
	if err := s.store.SetOrgDefaults(r.Context(), installationID, settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save"})
		return
	}
	s.auditSettings(r, installationID, "org_defaults.update", map[string]interface{}{"persona": body.Persona})
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// deleteRepoSettingKey removes a single key from repo settings_json, reverting it to org default inheritance.
func (s *Server) deleteRepoSettingKey(w http.ResponseWriter, r *http.Request) {
	repoID, ok := s.verifyRepoAccess(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "key")
	validKeys := map[string]bool{
		"persona": true, "custom_persona_prompt": true, "deep_review": true,
		"cross_file_context": true, "blast_radius": true, "scenario_memory": true,
		"code_simulation": true, "pr_enrichment": true, "learn_patterns": true,
		"learn_conventions": true, "file_synthesis": true, "architecture_graph": true,
	}
	if !validKeys[key] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown setting key"})
		return
	}
	if _, err := s.store.Pool.Exec(r.Context(), `UPDATE repos SET settings_json = settings_json - $2, updated_at = NOW() WHERE id = $1`, repoID, key); err != nil {
		s.logger.Error("delete repo setting key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove key"})
		return
	}
	s.auditSettings(r, 0, "repo_setting.delete", map[string]interface{}{"key": key, "repo_id": repoID})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

// --- Supermemory Key ---

func (s *Server) getSupermemoryKeyStatus(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	enc, err := s.store.GetSupermemoryKey(r.Context(), installationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"configured": enc != ""})
}

func (s *Server) setSupermemoryKey(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api_key required"})
		return
	}
	enc, err := crypto.Encrypt(body.APIKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encryption failed"})
		return
	}
	if err := s.store.SetSupermemoryKey(r.Context(), installationID, enc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}
	if s.memRegistry != nil {
		s.memRegistry.InvalidateClient(installationID)
	}
	s.auditSettings(r, installationID, "supermemory_key.set", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) deleteSupermemoryKey(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation id"})
		return
	}
	if !containsID(getInstallationIDs(r.Context()), installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized"})
		return
	}
	if err := s.store.ClearSupermemoryKey(r.Context(), installationID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if s.memRegistry != nil {
		s.memRegistry.InvalidateClient(installationID)
	}
	s.auditSettings(r, installationID, "supermemory_key.delete", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
