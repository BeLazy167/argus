package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/pipeline"
	"github.com/BeLazy167/argus/internal/store"
)

type Server struct {
	router           chi.Router
	store            *store.Store
	ghApp            *ghpkg.App
	orchestrator     *pipeline.Orchestrator
	replyAnalyzer    *pipeline.ReplyAnalyzer
	reactionAnalyzer *pipeline.ReactionAnalyzer
	indexer          *memory.Indexer
	registry         *llm.Registry
	eventBus         *pipeline.EventBus
	webhookSecret    []byte
	logger           *slog.Logger
	rateLimiter      *RateLimiter
	inFlightReviews  sync.Map     // "{repo}:{prNumber}" → struct{}
	webhookSem       chan struct{} // bounded concurrency for webhook goroutines
}

func NewServer(st *store.Store, ghApp *ghpkg.App, orchestrator *pipeline.Orchestrator, replyAnalyzer *pipeline.ReplyAnalyzer, reactionAnalyzer *pipeline.ReactionAnalyzer, indexer *memory.Indexer, registry *llm.Registry, eventBus *pipeline.EventBus, webhookSecret string, corsOrigin string, logger *slog.Logger) *Server {
	s := &Server{
		store:            st,
		ghApp:            ghApp,
		orchestrator:     orchestrator,
		replyAnalyzer:    replyAnalyzer,
		reactionAnalyzer: reactionAnalyzer,
		indexer:          indexer,
		registry:         registry,
		eventBus:         eventBus,
		webhookSecret:    []byte(webhookSecret),
		logger:           logger,
		rateLimiter:      NewRateLimiter(),
		webhookSem:       make(chan struct{}, 50),
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if corsOrigin != "" {
		r.Use(cors(corsOrigin))
	}

	// Health
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	// Webhooks (unauthenticated, signature-verified)
	r.Post("/webhooks/github", s.handleWebhook)

	// WebSocket stream & export — outside /api/v1 auth, handles own auth from query params
	r.Get("/api/v1/reviews/{reviewID}/stream", s.streamReviewWS)
	r.Get("/api/v1/reviews/{reviewID}/export", s.exportReview)

	// API v1 (authenticated via JWT)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.jwtAuth)

		// Unscoped (user-level) — with timeout
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(60 * time.Second))
			r.Get("/me/installations", s.listMyInstallations)
			r.Post("/installations/link", s.linkInstallation)
			r.Get("/installations/install-url", s.getInstallURL)
		})

		// Scoped (requires linked installation)
		r.Group(func(r chi.Router) {
			r.Use(s.requireInstallationScope)

			// All other scoped routes — with timeout
			r.Group(func(r chi.Router) {
				r.Use(middleware.Timeout(60 * time.Second))

				// Installations
				r.Get("/installations", s.listInstallations)
				r.Get("/installations/current", s.getCurrentInstallation)
				r.Post("/installations/auto-link", s.autoLinkInstallation)
				r.Post("/installations/{installationID}/sync-repos", s.syncRepos)

				// Repos
				r.Get("/repos", s.listRepos)
				r.Get("/repos/{repoID}", s.getRepo)
				r.Patch("/repos/{repoID}", s.updateRepo)

				// Provider Keys
				r.Get("/installations/{installationID}/provider-keys", s.listProviderKeys)
				r.Put("/installations/{installationID}/provider-keys", s.upsertProviderKey)
				r.Delete("/installations/{installationID}/provider-keys/{keyID}", s.deleteProviderKey)

				// Prompt Templates
				r.Get("/repos/{repoID}/prompts", s.listPromptTemplates)
				r.Put("/repos/{repoID}/prompts/{stage}", s.upsertPromptTemplate)
				r.Delete("/repos/{repoID}/prompts/{stage}", s.deletePromptTemplate)
				r.Get("/prompts/defaults", s.listDefaultPrompts)

				// Org Default Settings
				r.Get("/installations/{installationID}/defaults", s.getOrgDefaults)
				r.Put("/installations/{installationID}/defaults", s.setOrgDefaults)

				// Feature flags (issue acceptance, cross-PR checks, linked-PR cap)
				r.Get("/installations/{installationID}/features", s.getFeatureFlags)
				r.Put("/installations/{installationID}/features", s.setFeatureFlags)

				// Model Config
				r.Get("/repos/{repoID}/config", s.getModelConfigs)
				r.Put("/repos/{repoID}/config/{stage}", s.upsertModelConfig)
				r.Delete("/repos/{repoID}/config/{stage}", s.deleteModelConfig)
				r.Post("/installations/{installationID}/test-config", s.testConfig)

				// Org Model Config
				r.Get("/installations/{installationID}/config", s.getOrgModelConfigs)
				r.Put("/installations/{installationID}/config/{stage}", s.upsertOrgModelConfig)
				r.Delete("/installations/{installationID}/config/{stage}", s.deleteOrgModelConfig)

				// Reviews
				r.Get("/reviews", s.listAllReviews)
				r.Get("/repos/{repoID}/reviews", s.listReviews)
				r.Post("/repos/{repoID}/reviews", s.triggerReview)
				r.Get("/reviews/{reviewID}", s.getReview)
				r.Post("/reviews/{reviewID}/retry", s.retryReview)

				// Rules
				r.Get("/rules", s.listRules)
				r.Post("/rules", s.createRule)
				r.Put("/rules/{ruleID}", s.updateRule)
				r.Delete("/rules/{ruleID}", s.deleteRule)

				// Stats
				r.Get("/stats", s.getStats)
				r.Get("/activity", s.getActivity)

				// Patterns
				r.Get("/patterns", s.listPatterns)
				r.Get("/patterns/stats", s.getPatternStats)
				r.Get("/patterns/health", s.patternHealth)
				r.Post("/patterns", s.createPattern)
				r.Delete("/patterns/{patternID}", s.deletePattern)
				r.Get("/patterns/{patternID}", s.getPattern)

				// Graph & Architecture
				r.Get("/repos/{repoID}/graph", s.getGraph)
				r.Get("/repos/{repoID}/architecture", s.getArchitecture)
				r.Get("/repos/{repoID}/files/*", s.getFileMemory)

				// Scenarios
				r.Get("/repos/{repoID}/scenarios", s.listScenarios)
				r.Post("/repos/{repoID}/scenarios", s.createScenario)
				r.Delete("/scenarios/{scenarioID}", s.deactivateScenario)
				r.Get("/scenarios/{scenarioID}", s.getScenario)

				// Traces
				r.Get("/repos/{repoID}/traces", s.listTraces)
				r.Get("/repos/{repoID}/risk", s.getRepoRisk)

				// OpenRouter
				r.Get("/openrouter-models", s.listOpenRouterModels)
			})
		})
	})

	s.router = r
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// --- Health ---

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		s.logger.Error("readyz ping failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- Concurrency Guards ---

func (s *Server) tryAcquireReview(repo string, pr int) bool {
	key := fmt.Sprintf("%s:%d", repo, pr)
	_, loaded := s.inFlightReviews.LoadOrStore(key, struct{}{})
	return !loaded
}

func (s *Server) releaseReview(repo string, pr int) {
	key := fmt.Sprintf("%s:%d", repo, pr)
	s.inFlightReviews.Delete(key)
}

func (s *Server) acquireSem() bool {
	select {
	case s.webhookSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSem() {
	<-s.webhookSem
}

// --- Helpers ---

func containsID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func maskKey(encKey string) string {
	if len(encKey) > 0 {
		return "sk-...****"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(data); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode response"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleDBError(w http.ResponseWriter, err error, notFoundMsg string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": notFoundMsg})
		return
	}
	s.logger.Error("database error", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}
