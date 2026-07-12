package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// TestStoreToLLMConfigs_OrgRowMapsToRepoIDZero pins the org-row convention
// the registry cascade depends on: repo_id NULL in the DB → RepoID 0 in
// llm.ModelConfig (the org-fallback sentinel in Registry.GetConfig).
func TestStoreToLLMConfigs_OrgRowMapsToRepoIDZero(t *testing.T) {
	repoID := int64(42)
	in := []store.ModelConfig{
		{RepoID: &repoID, Stage: "review", Provider: "openai", Model: "repo-model", MaxTokens: 1000, Temperature: 0.5},
		{RepoID: nil, Stage: "scoring", Provider: "openrouter", Model: "org-model", MaxTokens: 2000, Temperature: 0.1},
	}
	out := storeToLLMConfigs(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].RepoID != 42 || out[0].Stage != llm.StageReview || out[0].Model != "repo-model" {
		t.Errorf("repo row mapped wrong: %+v", out[0])
	}
	if out[1].RepoID != 0 {
		t.Errorf("org row (repo_id NULL) must map to RepoID 0, got %d", out[1].RepoID)
	}
	if out[1].Stage != llm.StageScoring || out[1].Provider != "openrouter" || out[1].MaxTokens != 2000 {
		t.Errorf("org row fields mapped wrong: %+v", out[1])
	}
}

// staticKeyResolver satisfies llm.KeyResolver with a canned key + baseURL so
// the registry builds a real ChatProvider pointed at a local test server.
type staticKeyResolver struct{ baseURL string }

func (s staticKeyResolver) ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (string, string, bool, error) {
	return "test-key", s.baseURL, true, nil
}

// emptyConfigLister simulates an installation with a provider key but ZERO
// model_configs rows — the unconfigured-scoring case the notice covers.
type emptyConfigLister struct{}

func (emptyConfigLister) ListLLMConfigs(ctx context.Context, repoID int64) ([]llm.ModelConfig, error) {
	return nil, nil
}

// staticConfigLister returns a fixed set of model configs.
type staticConfigLister struct{ configs []llm.ModelConfig }

func (l staticConfigLister) ListLLMConfigs(ctx context.Context, repoID int64) ([]llm.ModelConfig, error) {
	return l.configs, nil
}

func scoringTestRun() *PipelineRun {
	return &PipelineRun{
		PREvent: ghpkg.PREvent{
			RepoFullName: "acme/primary",
			PRNumber:     7,
			PRTitle:      "add feature",
			PRAuthor:     "octocat",
		},
		DBInstallationID: 2001,
		DBRepoID:         501,
		FileReviews: []FileReview{
			{Path: "a.go", Comments: []FileComment{
				{Line: 1, What: "nil deref on error path", Severity: SeverityWarning},
			}},
		},
	}
}

// judgeStubServer returns an httptest server answering every request with a
// valid single-group judge response, plus a counter of LLM calls received.
func judgeStubServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	const judgeJSON = `[{"representative":0,"score":90,"severity":"warning","duplicates":[],"reason":"valid"}]`
	stub, err := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": judgeJSON}, "finish_reason": "stop"},
		},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stub)
	}))
	t.Cleanup(server.Close)
	return server, calls
}

// TestScoringStage_UnconfiguredSkipPostsNotice locks in: a provider key with
// no model rows makes scoring skip EXPLICITLY — ScoringSkipped +
// ScoringUnconfigured set, and the summary notice renders. Unfiltered
// findings must never be silent.
func TestScoringStage_UnconfiguredSkipPostsNotice(t *testing.T) {
	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{})
	ss := &ScoringStage{registry: registry, cfgLister: emptyConfigLister{}}
	run := scoringTestRun()

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !run.ScoringSkipped || !run.ScoringUnconfigured {
		t.Fatalf("skipped=%v unconfigured=%v, want both true", run.ScoringSkipped, run.ScoringUnconfigured)
	}
	notice := scoringSkippedNotice(run)
	if !strings.Contains(notice, "not score-filtered") || !strings.Contains(notice, "Settings → Org Defaults") {
		t.Errorf("notice = %q, want unconfigured-scoring setup notice", notice)
	}
	// Findings pass through untouched.
	if len(run.FileReviews) != 1 || len(run.FileReviews[0].Comments) != 1 {
		t.Errorf("comments must pass through on skip, got %+v", run.FileReviews)
	}
}

// missingKeyResolver simulates an installation with model rows possible but
// no stored provider key (found=false, no error).
type missingKeyResolver struct{}

func (missingKeyResolver) ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (string, string, bool, error) {
	return "", "", false, nil
}

// TestScoringStage_MissingKeySkipPostsNotice: a scoring row without a stored
// provider key is also a configuration gap (ErrNoAPIKey) — flags set, notice
// renders.
func TestScoringStage_MissingKeySkipPostsNotice(t *testing.T) {
	registry := llm.NewRegistry()
	registry.SetResolver(missingKeyResolver{})
	ss := &ScoringStage{registry: registry, cfgLister: staticConfigLister{configs: []llm.ModelConfig{
		{RepoID: 0, Stage: llm.StageScoring, Provider: "openrouter", Model: "test-model", MaxTokens: 1000, Temperature: 0.1},
	}}}
	run := scoringTestRun()

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !run.ScoringSkipped || !run.ScoringUnconfigured {
		t.Fatalf("skipped=%v unconfigured=%v, want both true", run.ScoringSkipped, run.ScoringUnconfigured)
	}
	if notice := scoringSkippedNotice(run); notice == "" {
		t.Error("notice empty, want setup notice for missing provider key")
	} else if !strings.Contains(notice, "API key") {
		t.Errorf("missing-key notice must point at API keys, not model config: %q", notice)
	}
}

// failingConfigLister simulates a transient DB failure listing model configs.
type failingConfigLister struct{}

func (failingConfigLister) ListLLMConfigs(ctx context.Context, repoID int64) ([]llm.ModelConfig, error) {
	return nil, errors.New("pq: connection reset")
}

// TestScoringStage_ListerFailureNoSetupNotice locks in the sentinel gating: a
// transient lister error (DB blip) on a possibly fully-configured org skips
// scoring but must NOT claim the org is unconfigured or post the setup notice.
func TestScoringStage_ListerFailureNoSetupNotice(t *testing.T) {
	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{})
	ss := &ScoringStage{registry: registry, cfgLister: failingConfigLister{}}
	run := scoringTestRun()

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !run.ScoringSkipped {
		t.Fatal("ScoringSkipped = false, want true on lister failure")
	}
	if run.ScoringUnconfigured {
		t.Fatal("ScoringUnconfigured = true on transient lister failure, want false")
	}
	if notice := scoringSkippedNotice(run); notice != "" {
		t.Errorf("notice = %q, want empty on transient lister failure", notice)
	}
}

// TestScoringStage_NoticeGatedOnFindings: with zero findings nothing went
// unfiltered — the notice must not render even when scoring is unconfigured.
func TestScoringStage_NoticeGatedOnFindings(t *testing.T) {
	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{})
	ss := &ScoringStage{registry: registry, cfgLister: emptyConfigLister{}}
	run := scoringTestRun()
	run.FileReviews = nil

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !run.ScoringSkipped || !run.ScoringUnconfigured {
		t.Fatalf("skipped=%v unconfigured=%v, want both true", run.ScoringSkipped, run.ScoringUnconfigured)
	}
	if notice := scoringSkippedNotice(run); notice != "" {
		t.Errorf("notice = %q, want empty when the review has zero findings", notice)
	}
}

// TestScoringStage_ConfiguredRunsJudgeNoNotice: with an org scoring row the
// judge runs (one LLM call, score applied) and no notice renders.
func TestScoringStage_ConfiguredRunsJudgeNoNotice(t *testing.T) {
	server, calls := judgeStubServer(t)
	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{baseURL: server.URL})
	ss := &ScoringStage{registry: registry, cfgLister: staticConfigLister{configs: []llm.ModelConfig{
		{RepoID: 0, Stage: llm.StageScoring, Provider: "openrouter", Model: "test-model", MaxTokens: 1000, Temperature: 0.1},
	}}}
	run := scoringTestRun()

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if run.ScoringSkipped || run.ScoringUnconfigured {
		t.Fatalf("skipped=%v unconfigured=%v, want both false", run.ScoringSkipped, run.ScoringUnconfigured)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("judge LLM calls = %d, want 1", got)
	}
	if notice := scoringSkippedNotice(run); notice != "" {
		t.Errorf("notice = %q, want empty when scoring ran", notice)
	}
	if got := run.FileReviews[0].Comments[0].Score; got != 90 {
		t.Errorf("finding score = %d, want 90 from judge", got)
	}
}

// TestScoringStage_TransientFailureNoSetupNotice: a judge HTTP failure sets
// ScoringSkipped but NOT ScoringUnconfigured — the setup nudge would mislead
// on a transient outage, so no notice renders.
func TestScoringStage_TransientFailureNoSetupNotice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	registry := llm.NewRegistry()
	registry.SetResolver(staticKeyResolver{baseURL: server.URL})
	ss := &ScoringStage{registry: registry, cfgLister: staticConfigLister{configs: []llm.ModelConfig{
		{RepoID: 0, Stage: llm.StageScoring, Provider: "openrouter", Model: "test-model", MaxTokens: 1000, Temperature: 0.1},
	}}}
	run := scoringTestRun()

	if err := ss.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !run.ScoringSkipped {
		t.Fatal("ScoringSkipped = false, want true on judge failure")
	}
	if run.ScoringUnconfigured {
		t.Fatal("ScoringUnconfigured = true on transient failure, want false")
	}
	if notice := scoringSkippedNotice(run); notice != "" {
		t.Errorf("notice = %q, want empty on transient failure", notice)
	}
}
