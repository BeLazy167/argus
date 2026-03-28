package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/internal/util"
	"github.com/BeLazy167/argus/pkg/diff"
)

// TriageAction classifies how deeply a file should be reviewed.
type TriageAction string

const (
	TriageSkip         TriageAction = "skip"
	TriageSkim         TriageAction = "skim"
	TriageSecuritySkim TriageAction = "security_skim"
	TriageDeep         TriageAction = "deep"
)

// TriageResult holds the triage classification for a single file.
type TriageResult struct {
	File   string       `json:"file"`
	Action TriageAction `json:"action"`
	Reason string       `json:"reason"`
}

// TriageStage classifies files as skip/skim/deep using a fast LLM.
type TriageStage struct {
	registry  *llm.Registry
	store     *store.Store
	memClient *memory.Client
}

func NewTriageStage(registry *llm.Registry, st *store.Store, memClient *memory.Client) *TriageStage {
	return &TriageStage{registry: registry, store: st, memClient: memClient}
}

func (ts *TriageStage) Execute(ctx context.Context, run *PipelineRun) error {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return nil
	}

	provider, cfg, err := ts.registry.ResolveProvider(ctx, storeConfigLister{st: ts.store, installationID: run.DBInstallationID}, run.DBInstallationID, run.DBRepoID, llm.StageTriage)
	if err != nil {
		return fmt.Errorf("resolve triage provider: %w", err)
	}

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		slog.Warn("triage: invalid repo name, skipping memory hints", "error", err)
	}
	prompt := buildTriagePrompt(run.Diff.Files)
	if hints := triageMemoryHints(ctx, ts.memClient, owner, repo, run.Diff.Files); hints != "" {
		prompt += "\n" + hints
	}
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      customOrDefault(run.Prompts, "triage_system", triageSystemPrompt),
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

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventTriageComplete, map[string]any{
			"files": results,
		})
	}

	// Accumulate triage token usage
	run.Tokens.Triage = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Triage)
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventTokenUpdate, map[string]any{
			"total_tokens": run.Tokens.Total.TotalTokens,
			"cost":         run.Tokens.Total.Cost,
		})
	}

	return nil
}

func buildTriagePrompt(files []diff.FileDiff) string {
	var sb strings.Builder
	sb.WriteString("Classify each file for code review depth.\n\nFiles changed:\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("\n--- %s (%s) ---\n", f.NewName, f.Status))
		// Include first N lines of diff for context
		const maxDiffLines = 80
		lines := strings.Split(f.RawDiff, "\n")
		limit := min(len(lines), maxDiffLines)
		sb.WriteString(strings.Join(lines[:limit], "\n"))
		if len(lines) > maxDiffLines {
			sb.WriteString(fmt.Sprintf("\n... (%d more lines)\n", len(lines)-maxDiffLines))
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
		case TriageSkip, TriageSkim, TriageSecuritySkim, TriageDeep:
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

// StoreConfigListerFor returns an llm.ModelConfigLister backed by the given store.
func StoreConfigListerFor(st *store.Store, installationID int64) storeConfigLister {
	return storeConfigLister{st: st, installationID: installationID}
}

// storeConfigLister adapts *store.Store to llm.ModelConfigLister with org fallback.
type storeConfigLister struct {
	st             *store.Store
	installationID int64
}

func (s storeConfigLister) ListLLMConfigs(ctx context.Context, repoID int64) ([]llm.ModelConfig, error) {
	if s.installationID > 0 {
		dbConfigs, err := s.st.ListModelConfigsWithFallback(ctx, s.installationID, repoID)
		if err != nil {
			return nil, err
		}
		return storeToLLMConfigs(dbConfigs), nil
	}
	dbConfigs, err := s.st.ListModelConfigs(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return storeToLLMConfigs(dbConfigs), nil
}

// triageMemoryHints searches Supermemory for file synthesis docs, repo patterns,
// owner patterns, and rules matching changed files.
// Returns a hint block for the triage prompt, or empty string if no history found.
func triageMemoryHints(ctx context.Context, memClient *memory.Client, owner, repo string, files []diff.FileDiff) string {
	if memClient == nil || owner == "" || repo == "" || len(files) == 0 {
		return ""
	}

	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var fileNames []string
	for _, f := range files {
		fileNames = append(fileNames, f.NewName)
	}
	query := filePathsQuery("file synthesis review history ", fileNames)
	repoTag := memory.RepoTag(owner, repo, "patterns")
	ownerTag := memory.OwnerTag(owner, "patterns")
	rulesTag := memory.OwnerTag(owner, "rules")

	// Parallel searches for repo patterns, owner patterns, and rules
	var repoResults, ownerResults, ruleResults []string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		repoResults = searchMemoryRich(searchCtx, memClient, query, repoTag, 5)
	}()
	go func() {
		defer wg.Done()
		ownerResults = searchMemoryRich(searchCtx, memClient, query, ownerTag, 3)
	}()
	go func() {
		defer wg.Done()
		ruleResults = searchMemoryRich(searchCtx, memClient, "review rules conventions", rulesTag, 3)
	}()
	wg.Wait()

	var sb strings.Builder

	if len(repoResults) > 0 {
		sb.WriteString("\n## File History (from past reviews)\n")
		for _, r := range repoResults {
			sb.WriteString(fmt.Sprintf("- %s\n", util.Truncate(r, 200, true)))
		}
	}
	if len(ownerResults) > 0 {
		sb.WriteString("\n## Org-wide Patterns\n")
		for _, r := range ownerResults {
			sb.WriteString(fmt.Sprintf("- %s\n", util.Truncate(r, 200, true)))
		}
	}
	if len(ruleResults) > 0 {
		sb.WriteString("\n## Review Rules\n")
		for _, r := range ruleResults {
			sb.WriteString(fmt.Sprintf("- %s\n", util.Truncate(r, 200, true)))
		}
	}

	total := len(repoResults) + len(ownerResults) + len(ruleResults)
	if total == 0 {
		return ""
	}
	slog.Debug("triage memory hints", "repo_results", len(repoResults), "owner_results", len(ownerResults), "rule_results", len(ruleResults), "owner", owner, "repo", repo)
	return sb.String()
}

const triageSystemPrompt = `You are a code review triage assistant. Given a list of changed files with abbreviated diffs, classify each file into one of four review depths:

- "skip": Generated files, lockfiles, configs with no logic, pure renames, vendored deps, .min.js, .map files
- "skim": Simple changes (<20 lines), typo fixes, minor refactors that don't change behavior, style-only changes, test files with only assertion value changes
- "security_skim": Auth middleware, input parsing/validation, API route handlers, encryption, session management — files that need a security pass but not full deep review
- "deep": Business logic, security-sensitive code, API changes, complex algorithms, new features, files with >50 lines changed, any file with control flow changes

## Risk-Aware Rules (apply BEFORE general classification):
- Files touching authentication, authorization, session management, or cryptography: ALWAYS "deep"
- Public API surfaces (route handlers, exported interfaces, SDK methods): ALWAYS "deep"
- Files implementing core business logic (payment, billing, scoring, matching): ALWAYS "deep"
- Lock files (.lock), generated code (.generated., .pb.go, .g.dart), vendor directories: ALWAYS "skip"
- Configuration files (.yaml, .json, .toml) with security implications (secrets, permissions, CORS): "security_skim"

## When in doubt: classify UP, not down
- If a file MIGHT contain business logic, classify as "deep" not "skim"
- A 200-line file with logic changes must NEVER be "skim" — that's "deep"
- New files introducing new modules or classes: ALWAYS "deep"
- Files in languages the LLM handles less well (Go, Rust) deserve "deep" more than "skim"

## NEVER skim these:
- Files with error handling changes (try/catch, error returns)
- Files with database queries or ORM calls
- Files with network/HTTP client code
- Files with concurrency primitives (mutex, channels, async/await patterns)

Respond ONLY with a JSON array. Each element: {"file": "<path>", "action": "skip|skim|security_skim|deep", "reason": "<brief reason>"}
Do not include any other text.`
