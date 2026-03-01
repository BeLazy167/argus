package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/pipeline"
	"github.com/BeLazy167/argus/internal/store"
)

type Server struct {
	router          chi.Router
	store           *store.Store
	ghApp           *ghpkg.App
	orchestrator    *pipeline.Orchestrator
	replyAnalyzer   *pipeline.ReplyAnalyzer
	indexer         *memory.Indexer
	registry        *llm.Registry
	webhookSecret   []byte
	logger          *slog.Logger
	rateLimiter     *RateLimiter
	inFlightReviews sync.Map     // "{repo}:{prNumber}" → struct{}
	webhookSem      chan struct{} // bounded concurrency for webhook goroutines
}

func NewServer(st *store.Store, ghApp *ghpkg.App, orchestrator *pipeline.Orchestrator, replyAnalyzer *pipeline.ReplyAnalyzer, indexer *memory.Indexer, registry *llm.Registry, webhookSecret string, corsOrigin string, logger *slog.Logger) *Server {
	s := &Server{
		store:         st,
		ghApp:         ghApp,
		orchestrator:  orchestrator,
		replyAnalyzer: replyAnalyzer,
		indexer:       indexer,
		registry:      registry,
		webhookSecret: []byte(webhookSecret),
		logger:        logger,
		rateLimiter:   NewRateLimiter(),
		webhookSem:    make(chan struct{}, 50),
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	if corsOrigin != "" {
		r.Use(cors(corsOrigin))
	}

	// Health
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	// Webhooks (unauthenticated, signature-verified)
	r.Post("/webhooks/github", s.handleWebhook)

	// API v1 (authenticated via JWT)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.jwtAuth)

		// Unscoped (user-level)
		r.Get("/me/installations", s.listMyInstallations)
		r.Post("/installations/link", s.linkInstallation)

		// Scoped (requires linked installation)
		r.Group(func(r chi.Router) {
			r.Use(s.requireInstallationScope)

			// Installations
			r.Get("/installations", s.listInstallations)

			// Repos
			r.Get("/repos", s.listRepos)
			r.Get("/repos/{repoID}", s.getRepo)
			r.Patch("/repos/{repoID}", s.updateRepo)

			// Provider Keys
			r.Get("/installations/{installationID}/provider-keys", s.listProviderKeys)
			r.Put("/installations/{installationID}/provider-keys", s.upsertProviderKey)
			r.Delete("/installations/{installationID}/provider-keys/{keyID}", s.deleteProviderKey)

			// Model Config
			r.Get("/repos/{repoID}/config", s.getModelConfigs)
			r.Put("/repos/{repoID}/config/{stage}", s.upsertModelConfig)
			r.Delete("/repos/{repoID}/config/{stage}", s.deleteModelConfig)
			r.Post("/installations/{installationID}/test-config", s.testConfig)

			// Reviews
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
			r.Post("/patterns", s.createPattern)
			r.Delete("/patterns/{patternID}", s.deletePattern)
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

// --- Webhook ---

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	event, err := ghpkg.ParseWebhook(r, s.webhookSecret)
	if err != nil {
		s.logger.Error("webhook parse failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook"})
		return
	}

	switch event.Type {
	case "pull_request":
		prEvent, err := ghpkg.ToPREvent(event)
		if err != nil {
			s.logger.Error("parsing PR event", "error", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		orgLogin := strings.SplitN(prEvent.RepoFullName, "/", 2)[0]
		if !s.rateLimiter.AllowReview(prEvent.RepoFullName, orgLogin, false) {
			s.logger.Warn("rate limited", "repo", prEvent.RepoFullName)
			break
		}
		if !s.tryAcquireReview(prEvent.RepoFullName, prEvent.PRNumber) {
			s.logger.Info("review already in-flight", "repo", prEvent.RepoFullName, "pr", prEvent.PRNumber)
			break
		}
		if !s.acquireSem() {
			s.releaseReview(prEvent.RepoFullName, prEvent.PRNumber)
			s.logger.Warn("webhook semaphore full")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server busy"})
			return
		}
		go func() {
			defer s.releaseSem()
			defer s.releaseReview(prEvent.RepoFullName, prEvent.PRNumber)
			if err := s.orchestrator.HandlePREvent(context.Background(), *prEvent); err != nil {
				s.logger.Error("review pipeline failed", "error", err, "pr", prEvent.PRNumber)
			}
		}()

	case "pull_request_review_comment":
		if event.Action == "created" && s.replyAnalyzer != nil {
			commentEvent, err := ghpkg.ToCommentEvent(event)
			if err != nil {
				s.logger.Error("parsing comment event", "error", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if strings.HasSuffix(commentEvent.CommentAuthor, "[bot]") {
				break
			}
			if commentEvent.InReplyToID > 0 {
				if !s.acquireSem() {
					s.logger.Warn("webhook semaphore full for reply analysis")
					break
				}
				go func() {
					defer s.releaseSem()
					if err := s.replyAnalyzer.Analyze(context.Background(), *commentEvent); err != nil {
						s.logger.Error("reply analysis failed", "error", err, "comment_id", commentEvent.CommentID)
					}
				}()
			}
		}

	case "issue_comment":
		if event.Action == "created" {
			issueEvent, err := ghpkg.ToIssueCommentEvent(event)
			if err != nil {
				s.logger.Error("parsing issue comment event", "error", err)
				break
			}
			if issueEvent == nil || strings.HasSuffix(issueEvent.CommentAuthor, "[bot]") {
				break
			}
			if !s.acquireSem() {
				s.logger.Warn("webhook semaphore full for command dispatch")
				break
			}
			go func() {
				defer s.releaseSem()
				s.dispatchCommand(context.Background(), *issueEvent)
			}()
		}

	case "installation":
		s.logger.Info("installation event", "action", event.Action)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// --- Installations ---

func (s *Server) listMyInstallations(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	list, err := s.store.ListUserInstallations(r.Context(), userID)
	if err != nil {
		s.logger.Error("list user installations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) linkInstallation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	var body struct {
		InstallationID int64 `json:"installation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	inst, err := s.store.GetInstallationByGitHubID(r.Context(), body.InstallationID)
	if err != nil {
		s.handleDBError(w, err, "installation not found")
		return
	}
	ui, err := s.store.LinkUserInstallation(r.Context(), userID, inst.ID, "owner")
	if err != nil {
		s.logger.Error("link installation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to link installation"})
		return
	}
	writeJSON(w, http.StatusOK, ui)
}

func (s *Server) listInstallations(w http.ResponseWriter, r *http.Request) {
	s.listMyInstallations(w, r)
}

// --- Repos ---

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.store.ListReposScoped(r.Context(), getInstallationIDs(r.Context()))
	if err != nil {
		s.logger.Error("list repos", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) getRepo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	repo, err := s.store.GetRepoScoped(r.Context(), id, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) updateRepo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	var body struct {
		Enabled       *bool   `json:"enabled"`
		DefaultBranch *string `json:"default_branch"`
		SettingsJSON  []byte  `json:"settings_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	repo, err := s.store.UpdateRepo(r.Context(), id, body.Enabled, body.DefaultBranch, body.SettingsJSON)
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

// --- Reviews ---

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	reviews, err := s.store.ListReviewsScoped(r.Context(), repoID, getInstallationIDs(r.Context()), limit, offset)
	if err != nil {
		s.logger.Error("list reviews", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (s *Server) triggerReview(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	var body struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PRNumber == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pr_number required"})
		return
	}

	repo, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}

	inst, err := s.store.GetInstallation(r.Context(), repo.InstallationID)
	if err != nil {
		s.logger.Error("lookup installation for manual review", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "installation not found"})
		return
	}

	orgLogin := strings.SplitN(repo.FullName, "/", 2)[0]
	if !s.rateLimiter.AllowReview(repo.FullName, orgLogin, false) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	if !s.tryAcquireReview(repo.FullName, body.PRNumber) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review already in-flight"})
		return
	}

	if err := s.store.LogActivity(r.Context(), "manual_review_triggered", "", repo.FullName, nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "manual_review_triggered")
	}

	go func() {
		defer s.releaseReview(repo.FullName, body.PRNumber)
		if err := s.orchestrator.HandlePREvent(context.Background(), ghpkg.PREvent{
			Action:         "manual",
			InstallationID: inst.InstallationID,
			RepoFullName:   repo.FullName,
			RepoID:         repo.GithubID,
			PRNumber:       body.PRNumber,
		}); err != nil {
			s.logger.Error("manual review failed", "error", err, "repo", repo.FullName, "pr", body.PRNumber)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "triggered", "repo": repo.FullName, "pr_number": fmt.Sprintf("%d", body.PRNumber)})
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	comments, err := s.store.GetReviewComments(r.Context(), id)
	if err != nil {
		s.logger.Error("fetching review comments", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review comments"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"review":   review,
		"comments": comments,
	})
}

func (s *Server) retryReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	if review.Status != "failed" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only failed reviews can be retried"})
		return
	}
	if err := s.store.UpdateReviewStatus(r.Context(), id, "pending", ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	go func() {
		if err := s.orchestrator.RetryReview(context.Background(), id); err != nil {
			s.logger.Error("retry review failed", "error", err, "review_id", id)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "review_id": id.String()})
}

// --- Model Config ---

func (s *Server) getModelConfigs(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
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
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	stage := chi.URLParam(r, "stage")
	validStages := map[string]bool{"triage": true, "review": true, "synthesis": true, "embedding": true}
	if !validStages[stage] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stage must be triage, review, synthesis, or embedding"})
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
	cfg, err := s.store.UpsertModelConfig(r.Context(), repoID, stage, body.Provider, body.Model, body.BaseURL, body.MaxTokens, body.Temperature)
	if err != nil {
		s.logger.Error("upsert model config", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save config"})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) deleteModelConfig(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	stage := chi.URLParam(r, "stage")
	if err := s.store.DeleteModelConfig(r.Context(), repoID, stage); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
		return
	}
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
		MaxTokens:   8,
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

// --- Rules ---

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

// --- Stats / Activity ---

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStatsScoped(r.Context(), getInstallationIDs(r.Context()))
	if err != nil {
		s.logger.Error("get stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	activity, err := s.store.ListActivity(r.Context(), limit)
	if err != nil {
		s.logger.Error("list activity", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

// --- Patterns ---

func (s *Server) listPatterns(w http.ResponseWriter, r *http.Request) {
	patterns, err := s.store.ListPatterns(r.Context(), getInstallationIDs(r.Context()))
	if err != nil {
		s.logger.Error("list patterns", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, patterns)
}

func (s *Server) createPattern(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstallationID int64  `json:"installation_id"`
		RepoID         *int64 `json:"repo_id"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Content == "" || body.InstallationID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation_id and content required"})
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, body.InstallationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	// Index in Supermemory (respect repo scope)
	var smID *string
	if s.indexer != nil {
		inst, err := s.store.GetInstallation(r.Context(), body.InstallationID)
		if err != nil {
			s.logger.Error("create pattern: lookup installation", "error", err)
		} else {
			metadata := map[string]string{"source": "dashboard"}
			var resp *memory.AddResponse
			if body.RepoID != nil {
				dbRepo, err := s.store.GetRepo(r.Context(), *body.RepoID)
				if err == nil {
					parts := strings.SplitN(dbRepo.FullName, "/", 2)
					if len(parts) == 2 {
						resp, err = s.indexer.IndexRepoPattern(r.Context(), parts[0], parts[1], body.Content, metadata)
					}
				}
			} else {
				resp, err = s.indexer.IndexOwnerPattern(r.Context(), inst.OrgLogin, body.Content, metadata)
			}
			if err != nil {
				s.logger.Error("index pattern in supermemory", "error", err)
			} else if resp != nil {
				smID = &resp.ID
			}
		}
	}

	createdBy := getUserID(r.Context())
	pattern, err := s.store.CreatePattern(r.Context(), body.InstallationID, body.RepoID, body.Content, smID, &createdBy)
	if err != nil {
		s.logger.Error("create pattern", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create pattern"})
		return
	}
	writeJSON(w, http.StatusCreated, pattern)
}

func (s *Server) deletePattern(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "patternID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pattern id"})
		return
	}

	// Fetch pattern for Supermemory cleanup (scoped to user's installations)
	pattern, getErr := s.store.GetPattern(r.Context(), id)

	// Delete from DB first (scoped auth check)
	if err := s.store.DeletePattern(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pattern not found"})
		return
	}

	// Only delete from Supermemory after DB deletion succeeds (confirms authorization)
	if getErr == nil && pattern.SupermemoryID != nil && s.indexer != nil {
		if err := s.indexer.DeleteDocument(r.Context(), *pattern.SupermemoryID); err != nil {
			s.logger.Error("delete pattern from supermemory", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
			APIKeyMasked:   maskKey(k.APIKeyEnc),
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
	pk, err := s.store.UpsertProviderKey(r.Context(), installationID, body.RepoID, body.Provider, body.APIKey, body.BaseURL)
	if err != nil {
		s.logger.Error("upsert provider key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save key"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":              pk.ID,
		"installation_id": pk.InstallationID,
		"repo_id":         pk.RepoID,
		"provider":        pk.Provider,
		"api_key_masked":  maskKey(pk.APIKeyEnc),
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func containsID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// maskKey returns a static masked placeholder for an encrypted key.
func maskKey(encKey string) string {
	if len(encKey) > 0 {
		return "sk-...****"
	}
	return ""
}

// --- Command Dispatch ---

var commandRe = regexp.MustCompile(`(?i)@argus-eye\s+(review|remember|resolve|fix)(.*)`)

func (s *Server) dispatchCommand(ctx context.Context, evt ghpkg.IssueCommentEvent) {
	match := commandRe.FindStringSubmatch(evt.CommentBody)
	if match == nil {
		return
	}

	parts := strings.SplitN(evt.RepoFullName, "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repo := parts[0], parts[1]
	ghClient := ghpkg.NewClient(s.ghApp)

	cmd := strings.ToLower(match[1])
	args := strings.TrimSpace(match[2])

	switch cmd {
	case "review":
		s.handleReviewCommand(ctx, evt, owner, repo, ghClient, args)
	case "remember":
		s.handleRememberCommand(ctx, evt, owner, repo, ghClient, args)
	case "resolve":
		s.handleResolveCommand(ctx, evt, owner, repo, ghClient)
	case "fix":
		s.handleFixCommand(ctx, evt, owner, repo, ghClient)
	}
}

func (s *Server) handleReviewCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client, args string) {
	force := strings.Contains(args, "--force")
	var personaOverride string
	if idx := strings.Index(args, "--persona"); idx >= 0 {
		rest := strings.TrimSpace(args[idx+len("--persona"):])
		if fields := strings.Fields(rest); len(fields) > 0 {
			personaOverride = fields[0]
		}
	}

	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	prEvent, err := ghClient.GetPullRequest(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("review command: fetch PR failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	if !force {
		existing, err := s.store.GetLatestReviewBySHA(ctx, evt.RepoFullName, evt.PRNumber, prEvent.HeadSHA)
		if err == nil && existing != nil {
			short := prEvent.HeadSHA
			if len(short) > 7 {
				short = short[:7]
			}
			body := fmt.Sprintf("Already reviewed at `%s`. Use `@argus-eye review --force` to re-review.", short)
			_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, body)
			_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
			return
		}
	}

	if !s.rateLimiter.AllowReview(evt.RepoFullName, owner, force) {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Rate limit exceeded. Try again later.")
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}
	if !s.tryAcquireReview(evt.RepoFullName, evt.PRNumber) {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"A review is already in progress for this PR.")
		return
	}
	defer s.releaseReview(evt.RepoFullName, evt.PRNumber)

	prEvent.Action = "manual"
	prEvent.RepoID = evt.RepoID
	prEvent.PersonaOverride = personaOverride
	s.logger.Info("review command triggered", "repo", evt.RepoFullName, "pr", evt.PRNumber, "force", force, "by", evt.CommentAuthor)

	if err := s.orchestrator.HandlePREvent(ctx, *prEvent); err != nil {
		s.logger.Error("review command: pipeline failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Review failed. Check the Argus dashboard for details.")
		return
	}

	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

// --- Concurrency Guards ---

// tryAcquireReview attempts to mark a PR as in-flight. Returns false if already running.
func (s *Server) tryAcquireReview(repo string, pr int) bool {
	key := fmt.Sprintf("%s:%d", repo, pr)
	_, loaded := s.inFlightReviews.LoadOrStore(key, struct{}{})
	return !loaded
}

func (s *Server) releaseReview(repo string, pr int) {
	key := fmt.Sprintf("%s:%d", repo, pr)
	s.inFlightReviews.Delete(key)
}

// acquireSem tries to acquire a webhook goroutine slot. Returns false if full.
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

// handleDBError distinguishes pgx.ErrNoRows (404) from other errors (500).
func (s *Server) handleDBError(w http.ResponseWriter, err error, notFoundMsg string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": notFoundMsg})
		return
	}
	s.logger.Error("database error", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}
