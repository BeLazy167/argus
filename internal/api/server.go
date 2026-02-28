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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	ghpkg "github.com/acmeorg/argus/internal/github"
	"github.com/acmeorg/argus/internal/pipeline"
	"github.com/acmeorg/argus/internal/store"
)

type Server struct {
	router        chi.Router
	store         *store.Store
	ghApp         *ghpkg.App
	orchestrator  *pipeline.Orchestrator
	replyAnalyzer *pipeline.ReplyAnalyzer
	webhookSecret []byte
	logger        *slog.Logger
}

func NewServer(st *store.Store, ghApp *ghpkg.App, orchestrator *pipeline.Orchestrator, replyAnalyzer *pipeline.ReplyAnalyzer, webhookSecret string, corsOrigin string, logger *slog.Logger) *Server {
	s := &Server{
		store:         st,
		ghApp:         ghApp,
		orchestrator:  orchestrator,
		replyAnalyzer: replyAnalyzer,
		webhookSecret: []byte(webhookSecret),
		logger:        logger,
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
		go func() {
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
				go func() {
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
			go s.handleReviewCommand(context.Background(), *issueEvent)
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
	userID := getUserID(r.Context())
	list, err := s.store.ListUserInstallations(r.Context(), userID)
	if err != nil {
		s.logger.Error("list installations", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, list)
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

	_ = s.store.LogActivity(r.Context(), "manual_review_triggered", "", repo.FullName, nil)

	go func() {
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
		comments = nil
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
	_ = s.store.LogActivity(r.Context(), "rule_created", "", fmt.Sprintf("rule:%d", rule.ID), nil)
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
	_ = s.store.LogActivity(r.Context(), "rule_deleted", "", fmt.Sprintf("rule:%d", id), nil)
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

// maskKey decrypts an encrypted key and returns masked version (last 4 chars).
func maskKey(encKey string) string {
	// We use the encrypted value length to derive a mask — don't decrypt for listing.
	// Just show a static mask since we can't safely show any of the real key.
	if len(encKey) > 0 {
		return "sk-...****"
	}
	return ""
}

// --- Review Command (comment trigger) ---

var reviewCmdRe = regexp.MustCompile(`(?i)@argus-eye\s+review(\s+--force)?`)

func (s *Server) handleReviewCommand(ctx context.Context, evt ghpkg.IssueCommentEvent) {
	match := reviewCmdRe.FindStringSubmatch(evt.CommentBody)
	if match == nil {
		return
	}
	force := strings.TrimSpace(match[1]) == "--force"

	parts := strings.SplitN(evt.RepoFullName, "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repo := parts[0], parts[1]
	ghClient := ghpkg.NewClient(s.ghApp)

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

	prEvent.Action = "manual"
	prEvent.RepoID = evt.RepoID
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
