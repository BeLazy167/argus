package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/pkg/diff"
)

// TriageAction classifies how deeply a file should be reviewed.
type TriageAction string

const (
	TriageSkip TriageAction = "skip"
	TriageSkim TriageAction = "skim"
	TriageDeep TriageAction = "deep"
)

// TriageResult holds the triage classification for a single file.
type TriageResult struct {
	File   string       `json:"file"`
	Action TriageAction `json:"action"`
	Reason string       `json:"reason"`
}

// TriageStage classifies files as skip/skim/deep using a fast LLM.
type TriageStage struct {
	registry *llm.Registry
	store    *store.Store
}

func NewTriageStage(registry *llm.Registry, st *store.Store) *TriageStage {
	return &TriageStage{registry: registry, store: st}
}

func (ts *TriageStage) Execute(ctx context.Context, run *PipelineRun) error {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return nil
	}

	// Load per-repo model configs from DB (use DB serial IDs, not GitHub IDs)
	var repoConfigs []llm.ModelConfig
	if dbConfigs, err := ts.store.ListModelConfigs(ctx, run.DBRepoID); err == nil {
		repoConfigs = storeToLLMConfigs(dbConfigs)
	}

	cfg, err := ts.registry.GetConfig(run.DBRepoID, llm.StageTriage, repoConfigs)
	if err != nil {
		return fmt.Errorf("triage config: %w", err)
	}
	provider, err := ts.registry.GetProviderForRepo(ctx, run.DBInstallationID, &run.DBRepoID, cfg.Provider)
	if err != nil {
		return fmt.Errorf("triage provider: %w", err)
	}

	prompt := buildTriagePrompt(run.Diff.Files)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      triageSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
	})
	if err != nil {
		return fmt.Errorf("triage LLM: %w", err)
	}

	results, err := parseTriageResponse(resp.Content)
	if err != nil {
		return fmt.Errorf("parsing triage: %w", err)
	}

	run.TriageResults = results

	// Accumulate triage token usage
	run.Tokens.Triage = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
	}
	run.Tokens.addToTotal(run.Tokens.Triage)

	return nil
}

func buildTriagePrompt(files []diff.FileDiff) string {
	var sb strings.Builder
	sb.WriteString("Classify each file for code review depth.\n\nFiles changed:\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("\n--- %s (%s) ---\n", f.NewName, f.Status))
		// Include first 50 lines of diff for context
		lines := strings.Split(f.RawDiff, "\n")
		limit := 50
		if len(lines) < limit {
			limit = len(lines)
		}
		sb.WriteString(strings.Join(lines[:limit], "\n"))
		if len(lines) > 50 {
			sb.WriteString(fmt.Sprintf("\n... (%d more lines)\n", len(lines)-50))
		}
	}
	return sb.String()
}

func parseTriageResponse(content string) ([]TriageResult, error) {
	results, err := unmarshalLLMArray[TriageResult](content)
	if err != nil {
		return nil, err
	}
	return validateTriageResults(results), nil
}

func validateTriageResults(results []TriageResult) []TriageResult {
	valid := make([]TriageResult, 0, len(results))
	for _, r := range results {
		if r.File == "" {
			continue
		}
		switch r.Action {
		case TriageSkip, TriageSkim, TriageDeep:
		default:
			r.Action = TriageDeep // default to deep if invalid
		}
		valid = append(valid, r)
	}
	return valid
}

// storeToLLMConfigs converts store.ModelConfig to llm.ModelConfig.
func storeToLLMConfigs(dbConfigs []store.ModelConfig) []llm.ModelConfig {
	out := make([]llm.ModelConfig, len(dbConfigs))
	for i, c := range dbConfigs {
		var repoID int64
		if c.RepoID != nil {
			repoID = *c.RepoID
		}
		out[i] = llm.ModelConfig{
			RepoID:      repoID,
			Stage:       llm.PipelineStage(c.Stage),
			Provider:    c.Provider,
			Model:       c.Model,
			MaxTokens:   c.MaxTokens,
			Temperature: float64(c.Temperature),
		}
	}
	return out
}

const triageSystemPrompt = `You are a code review triage assistant. Given a list of changed files with abbreviated diffs, classify each file into one of three review depths:

- "skip": Generated files, lockfiles, configs with no logic, pure renames, vendored deps
- "skim": Simple changes, typo fixes, minor refactors, test-only changes, style-only changes
- "deep": Business logic, security-sensitive code, API changes, complex algorithms, new features

Respond ONLY with a JSON array. Each element: {"file": "<path>", "action": "skip|skim|deep", "reason": "<brief reason>"}
Do not include any other text.`
