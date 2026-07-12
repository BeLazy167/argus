package llm

import (
	"context"
	"errors"
	"testing"
)

// TestGetConfig_RepoRowWinsOverOrg pins the top of the cascade: a
// repo-specific row beats an org-level (RepoID == 0) row for the same stage.
func TestGetConfig_RepoRowWinsOverOrg(t *testing.T) {
	r := NewRegistry()
	configs := []ModelConfig{
		{RepoID: 0, Stage: StageScoring, Provider: "openai", Model: "org-model", MaxTokens: 1000, Temperature: 0.5},
		{RepoID: 42, Stage: StageScoring, Provider: "anthropic", Model: "repo-model", MaxTokens: 2000, Temperature: 0.1},
	}
	cfg, err := r.GetConfig(42, StageScoring, configs)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Model != "repo-model" || cfg.Provider != "anthropic" {
		t.Errorf("repo row must win over org row, got %+v", cfg)
	}
}

// TestGetConfig_OrgFallbackWhenNoRepoRow pins the org fallback: an org-level
// row (RepoID == 0) satisfies the stage when no repo-specific row exists.
func TestGetConfig_OrgFallbackWhenNoRepoRow(t *testing.T) {
	r := NewRegistry()
	configs := []ModelConfig{
		{RepoID: 0, Stage: StageScoring, Provider: "openai", Model: "org-model", MaxTokens: 1000, Temperature: 0.5},
	}
	cfg, err := r.GetConfig(42, StageScoring, configs)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.Model != "org-model" {
		t.Errorf("expected org fallback, got %+v", cfg)
	}
}

// TestGetConfig_OtherStageRowDoesNotLeak verifies a repo row for a different
// stage never satisfies the lookup — the unconfigured stage errors with the
// ErrNoModelConfig sentinel.
func TestGetConfig_OtherStageRowDoesNotLeak(t *testing.T) {
	r := NewRegistry()
	configs := []ModelConfig{
		{RepoID: 42, Stage: StageTriage, Provider: "openai", Model: "triage-model"},
	}
	_, err := r.GetConfig(42, StageScoring, configs)
	if err == nil {
		t.Fatal("expected error — a triage row must not satisfy a scoring lookup")
	}
	if !errors.Is(err, ErrNoModelConfig) {
		t.Errorf("error = %q, want ErrNoModelConfig", err)
	}
}

// TestGetConfig_UnconfiguredStageErrors: no rows at all (any stage, known or
// unknown) is an explicit ErrNoModelConfig — never a silent platform default.
func TestGetConfig_UnconfiguredStageErrors(t *testing.T) {
	r := NewRegistry()
	for _, stage := range []PipelineStage{StageTriage, StageReview, StageScoring, StageSynthesis, StageEmbedding, PipelineStage("bogus")} {
		_, err := r.GetConfig(42, stage, nil)
		if err == nil {
			t.Errorf("GetConfig(%s) = nil error, want ErrNoModelConfig", stage)
			continue
		}
		if !errors.Is(err, ErrNoModelConfig) {
			t.Errorf("GetConfig(%s) error = %q, want ErrNoModelConfig", stage, err)
		}
	}
}

// notFoundKeyResolver reports no stored key without erroring.
type notFoundKeyResolver struct{}

func (notFoundKeyResolver) ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (string, string, bool, error) {
	return "", "", false, nil
}

// TestGetProviderForRepo_MissingKeyIsSentinel: a stored-key miss surfaces
// ErrNoAPIKey through the wrap chain so callers can gate setup guidance on it.
func TestGetProviderForRepo_MissingKeyIsSentinel(t *testing.T) {
	r := NewRegistry()
	r.SetResolver(notFoundKeyResolver{})
	_, err := r.GetProviderForRepo(context.Background(), 1, nil, "openrouter")
	if !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("error = %q, want ErrNoAPIKey", err)
	}
}
