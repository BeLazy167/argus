package api

import (
	"bytes"
	"context"
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
	"github.com/BeLazy167/argus/backend/internal/crypto"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/BeLazy167/argus/backend/internal/store"
)

type Server struct {
	router           chi.Router
	store            *store.Store
	ghApp            *ghpkg.App
	orchestrator     *pipeline.Orchestrator
	replyAnalyzer    *pipeline.ReplyAnalyzer
	reactionAnalyzer *pipeline.ReactionAnalyzer
	registry         *llm.Registry
	eventBus         *pipeline.EventBus
	webhookSecret    []byte
	logger           *slog.Logger
	rateLimiter      *RateLimiter
	inFlightReviews  sync.Map     // "{repo}:{prNumber}" → struct{}
	inFlightCancels  sync.Map     // "{repo}:{prNumber}" → context.CancelFunc
	webhookSem       chan struct{} // bounded concurrency for webhook goroutines
	audit            *auditLogger
	memRegistry      *memory.Registry
}

func NewServer(st *store.Store, ghApp *ghpkg.App, orchestrator *pipeline.Orchestrator, replyAnalyzer *pipeline.ReplyAnalyzer, reactionAnalyzer *pipeline.ReactionAnalyzer, registry *llm.Registry, eventBus *pipeline.EventBus, webhookSecret string, corsOrigin string, logger *slog.Logger, memRegistry *memory.Registry) *Server {
	s := &Server{
		store:            st,
		ghApp:            ghApp,
		orchestrator:     orchestrator,
		replyAnalyzer:    replyAnalyzer,
		reactionAnalyzer: reactionAnalyzer,
		registry:         registry,
		eventBus:         eventBus,
		webhookSecret:    []byte(webhookSecret),
		logger:           logger,
		rateLimiter:      NewRateLimiter(),
		webhookSem:       make(chan struct{}, 50),
		audit:            newAuditLogger(logger),
		memRegistry:      memRegistry,
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

	// WebSocket stream — outside /api/v1 auth, handles own auth from query params
	r.Get("/api/v1/reviews/{reviewID}/stream", s.streamReviewWS)

	// Public export — verified via HMAC signature in URL (linked from GitHub comments)
	r.Get("/api/v1/reviews/{reviewID}/export", s.exportReviewPublic)

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

				// Supermemory Key
				r.Get("/installations/{installationID}/supermemory-key", s.getSupermemoryKeyStatus)
				r.Put("/installations/{installationID}/supermemory-key", s.setSupermemoryKey)
				r.Delete("/installations/{installationID}/supermemory-key", s.deleteSupermemoryKey)

				// Model Pricing (global)
				r.Get("/pricing", s.listPricing)
				r.Put("/pricing", s.upsertPricing)
				r.Delete("/pricing/{pattern}", s.deletePricing)

				// Org Default Settings
				r.Get("/installations/{installationID}/defaults", s.getOrgDefaults)
				r.Put("/installations/{installationID}/defaults", s.setOrgDefaults)

				// Feature flags (issue acceptance, cross-PR checks, linked-PR cap)
				r.Get("/installations/{installationID}/features", s.getFeatureFlags)
				r.Put("/installations/{installationID}/features", s.setFeatureFlags)

				// Repo Settings
				r.Delete("/repos/{repoID}/settings/{key}", s.deleteRepoSettingKey)

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
				// export also accessible via signed URL at public route above
				r.Post("/reviews/{reviewID}/retry", s.retryReview)
				r.Post("/reviews/{reviewID}/cancel", s.cancelReview)

				// Rules
				r.Get("/rules", s.listRules)
				r.Post("/rules", s.createRule)
				r.Put("/rules/{ruleID}", s.updateRule)
				r.Delete("/rules/{ruleID}", s.deleteRule)

				// Stats (legacy)
				r.Get("/stats", s.getStats)
				r.Get("/activity", s.getActivity)

				// Org Stats (detailed analytics)
				r.Get("/stats/overview", s.statsOverview)
				r.Get("/stats/timeseries", s.statsTimeseries)
				r.Get("/stats/users", s.statsUsers)
				r.Get("/stats/models", s.statsModels)
				r.Get("/stats/findings", s.statsFindings)
				r.Get("/stats/adoption", s.statsAdoption)
				r.Get("/stats/repos", s.statsRepos)
				r.Get("/stats/review-times", s.statsReviewTimes)
				r.Get("/stats/cost-per-stage", s.statsCostPerStage)

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

func (s *Server) storeCancel(repo string, pr int, cancel context.CancelFunc) {
	key := fmt.Sprintf("%s:%d", repo, pr)
	s.inFlightCancels.Store(key, cancel)
}

func (s *Server) removeCancel(repo string, pr int) {
	key := fmt.Sprintf("%s:%d", repo, pr)
	s.inFlightCancels.Delete(key)
}

func (s *Server) loadCancel(repo string, pr int) (context.CancelFunc, bool) {
	key := fmt.Sprintf("%s:%d", repo, pr)
	v, ok := s.inFlightCancels.Load(key)
	if !ok {
		return nil, false
	}
	fn, ok := v.(context.CancelFunc)
	return fn, ok
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

func maskKey(encHint string) string {
	if encHint != "" {
		if plain, err := crypto.Decrypt(encHint); err == nil {
			return "****" + plain
		}
	}
	return "sk-...****"
}

// Close flushes audit events. Call on shutdown.
func (s *Server) Close() {
	s.audit.close()
}

// EventTracker returns a pipeline.EventTracker backed by PostHog.
func (s *Server) EventTracker() pipeline.EventTracker {
	return newEventTracker(s.audit)
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
