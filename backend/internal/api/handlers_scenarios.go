package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	gh "github.com/google/go-github/v68/github"
)

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	scenarios, err := s.store.ListScenariosForRepo(r.Context(), repoID, 100)
	if err != nil {
		s.logger.Error("list scenarios", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, scenarios)
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	repo, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	var body struct {
		Description string   `json:"description"`
		Source      string   `json:"source"`
		SourceRef   string   `json:"source_ref"`
		Files       []string `json:"files"`
		Modules     []string `json:"modules"`
		Severity    string   `json:"severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Description == "" || len(body.Description) > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description required (max 2000 chars)"})
		return
	}
	if len(body.Files) > 20 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many files (max 20)"})
		return
	}
	if body.Source == "" {
		body.Source = "manual"
	}
	validSeverities := map[string]bool{"critical": true, "high": true, "medium": true, "low": true}
	if body.Severity == "" {
		body.Severity = "medium"
	}
	if !validSeverities[body.Severity] {
		body.Severity = "medium"
	}
	id, err := s.store.CreateScenario(r.Context(), repo.InstallationID, &repoID, body.Description, body.Source, body.SourceRef, body.Files, body.Modules, body.Severity)
	if err != nil {
		s.logger.Error("create scenario", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create scenario"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) deactivateScenario(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "scenarioID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid scenario id"})
		return
	}
	// Verify scenario belongs to user's installations before deactivating
	if err := s.store.DeactivateScenarioScoped(r.Context(), id, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}

// getScenarioKPIs returns the 4-card summary counts for a repo's /scenarios page.
// Scoped to the user's installations — repo_id is validated before counting.
func (s *Server) getScenarioKPIs(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), repoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "repo not found")
		return
	}
	kpis, err := s.store.GetScenarioKPIs(r.Context(), repoID)
	if err != nil {
		s.logger.Error("scenario kpis", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, kpis)
}

// listScenarioRuns returns the per-scenario simulation history (newest first).
// Limit defaults to 20 and is capped at 100 to protect the API from abuse.
func (s *Server) listScenarioRuns(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "scenarioID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid scenario id"})
		return
	}
	// Scope check — load scenario, verify installation ownership, then fetch runs.
	scenario, err := s.store.GetScenario(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "scenario not found")
		return
	}
	if !containsID(getInstallationIDs(r.Context()), scenario.InstallationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	limit := parseLimitParam(r.URL.Query().Get("limit"), 20, 100)
	runs, err := s.store.GetScenarioRuns(r.Context(), id, limit)
	if err != nil {
		s.logger.Error("scenario runs", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// parseLimitParam extracts a pagination limit from a query string. Missing, non-numeric, or
// non-positive values fall through to defaultVal. Values above maxLimit are clamped to it.
// Keeps the handler tidy and makes the clamp logic easy to test without HTTP scaffolding.
func parseLimitParam(raw string, defaultVal, maxLimit int) int {
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultVal
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func (s *Server) getScenario(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "scenarioID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid scenario id"})
		return
	}
	scenario, err := s.store.GetScenario(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "scenario not found")
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, scenario.InstallationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	writeJSON(w, http.StatusOK, scenario)
}

// generateScenarioFromIssue creates a scenario from a GitHub issue webhook event.
func (s *Server) generateScenarioFromIssue(ctx context.Context, event *gh.IssuesEvent) {
	issue := event.GetIssue()
	installationID := event.GetInstallation().GetID()
	repoFullName := event.GetRepo().GetFullName()

	inst, err := s.store.GetInstallationByGitHubID(ctx, installationID)
	if err != nil {
		s.logger.Warn("issue scenario: installation lookup", "error", err, "gh_id", installationID)
		return
	}

	description := extractScenarioFromIssue(issue.GetTitle(), issue.GetBody())
	files := extractFilePathsFromText(issue.GetBody())

	severity := "medium"
	for _, l := range issue.Labels {
		name := strings.ToLower(l.GetName())
		if name == "critical" || name == "p0" {
			severity = "critical"
		}
		if name == "high" || name == "p1" {
			severity = "high"
		}
	}

	// Find matching repo in DB
	repos, _ := s.store.ListReposScoped(ctx, []int64{inst.ID})
	var repoID *int64
	for _, r := range repos {
		if r.FullName == repoFullName {
			repoID = &r.ID
			break
		}
	}

	_, err = s.store.CreateScenario(ctx, inst.ID, repoID, description, "issue", fmt.Sprintf("#%d", issue.GetNumber()), files, nil, severity)
	if err != nil {
		s.logger.Error("issue scenario: create failed", "error", err, "issue", issue.GetNumber())
		return
	}
	s.logger.Info("auto-created scenario from issue", "issue", issue.GetNumber(), "repo", repoFullName)
}

// extractScenarioFromIssue builds a scenario description from issue title and body.
func extractScenarioFromIssue(title, body string) string {
	desc := fmt.Sprintf("Issue: %s", title)
	if body != "" {
		cleanBody := strings.ReplaceAll(body, "```", "")
		cleanBody = strings.ReplaceAll(cleanBody, "###", "")
		if len(cleanBody) > 500 {
			cleanBody = cleanBody[:500] + "..."
		}
		desc += " — " + strings.TrimSpace(cleanBody)
	}
	return desc
}

var filePathRe = regexp.MustCompile("`([a-zA-Z0-9_/.-]+\\.[a-zA-Z]{1,5})`")

// extractFilePathsFromText finds backtick-wrapped file paths in text.
func extractFilePathsFromText(text string) []string {
	matches := filePathRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var paths []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			paths = append(paths, m[1])
		}
	}
	return paths
}
