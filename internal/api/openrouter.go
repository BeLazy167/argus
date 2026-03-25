package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type openRouterModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

type openRouterModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int    `json:"context_length"`
		Pricing       struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
		Architecture struct {
			Modality string `json:"modality"`
		} `json:"architecture"`
	} `json:"data"`
}

type modelCache struct {
	models    []openRouterModel
	fetchedAt time.Time
}

var openRouterCache sync.Map // key: int64 (installationID), value: *modelCache
const modelCacheTTL = time.Hour

func (s *Server) listOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("installation_id")
	if idStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation_id required"})
		return
	}
	installationID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation_id"})
		return
	}

	ids := getInstallationIDs(r.Context())
	if !containsID(ids, installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	// Check cache
	if cached, ok := openRouterCache.Load(installationID); ok {
		mc := cached.(*modelCache)
		if time.Since(mc.fetchedAt) < modelCacheTTL {
			writeJSON(w, http.StatusOK, mc.models)
			return
		}
	}

	// Resolve OpenRouter API key
	apiKey, _, found, err := s.store.ResolveAPIKey(r.Context(), installationID, nil, "openrouter")
	if err != nil {
		s.logger.Error("resolve openrouter key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key resolution failed"})
		return
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no openrouter API key configured"})
		return
	}

	// Fetch models from OpenRouter
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("fetch openrouter models", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to fetch models"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("openrouter returned %d", resp.StatusCode)})
		return
	}

	var orResp openRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&orResp); err != nil {
		s.logger.Error("decode openrouter response", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to parse models"})
		return
	}

	// Filter text models and map to simplified type
	var models []openRouterModel
	for _, m := range orResp.Data {
		modality := m.Architecture.Modality
		if modality != "" && !strings.Contains(modality, "text") {
			continue
		}
		om := openRouterModel{
			ID:            m.ID,
			Name:          m.Name,
			ContextLength: m.ContextLength,
		}
		om.Pricing.Prompt = m.Pricing.Prompt
		om.Pricing.Completion = m.Pricing.Completion
		models = append(models, om)
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})

	// Cache
	openRouterCache.Store(installationID, &modelCache{
		models:    models,
		fetchedAt: time.Now(),
	})

	writeJSON(w, http.StatusOK, models)
}
