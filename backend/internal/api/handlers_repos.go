package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

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
	scopedRepo, err := s.store.GetRepoScoped(r.Context(), id, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	var body struct {
		Enabled       *bool           `json:"enabled"`
		DefaultBranch *string         `json:"default_branch"`
		SettingsJSON  json.RawMessage `json:"settings_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Enabled != nil && *body.Enabled {
		tier, _ := s.store.GetPlanTier(r.Context(), scopedRepo.InstallationID)
		if !s.cfg.IsPro(tier) {
			count, _ := s.store.CountEnabledRepos(r.Context(), scopedRepo.InstallationID)
			if count >= 3 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "Free plan limited to 3 repos. Upgrade to Pro for unlimited."})
				return
			}
		}
	}
	repo, err := s.store.UpdateRepo(r.Context(), id, body.Enabled, body.DefaultBranch, body.SettingsJSON)
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	writeJSON(w, http.StatusOK, repo)
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
	if !s.cfg.IsPro(inst.PlanTier) && !s.rateLimiter.AllowReview(repo.FullName, orgLogin, false) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}
	// The launcher owns slot + cancel + spawn. Detached BaseCtx mirrors the
	// original (background, no request trace).
	prEvent := ghpkg.PREvent{
		Action:         "manual",
		InstallationID: inst.InstallationID,
		RepoFullName:   repo.FullName,
		RepoID:         repo.GithubID,
		PRNumber:       body.PRNumber,
	}
	launchErr := s.launcher.Launch(pipeline.LaunchSpec{
		Repo:    repo.FullName,
		PR:      body.PRNumber,
		BaseCtx: context.Background(),
		Run:     func(ctx context.Context) error { return s.orchestrator.HandlePREvent(ctx, prEvent) },
		OnDone: func(err error) {
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("manual review failed", "error", err, "repo", repo.FullName, "pr", body.PRNumber)
			}
		},
	})
	if errors.Is(launchErr, pipeline.ErrInFlight) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review already in-flight"})
		return
	}

	if err := s.store.LogActivity(r.Context(), nil, "manual_review_triggered", "", repo.FullName, nil); err != nil {
		s.logger.Error("failed to log activity", "error", err, "action", "manual_review_triggered")
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "triggered", "repo": repo.FullName, "pr_number": fmt.Sprintf("%d", body.PRNumber)})
}
