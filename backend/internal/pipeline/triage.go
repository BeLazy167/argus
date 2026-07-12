package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/BeLazy167/argus/backend/pkg/diff"
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

// TriageStage classifies files as skip/skim/deep using a fast LLM. Memory
// hints resolve from run.Indexer (the per-run indexer), so the stage needs no
// memory.Registry of its own.
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

	// Phase 1: Heuristic triage — instant, free, never fails
	results := heuristicTriage(run.Diff.Files)
	var deepCount int
	for _, r := range results {
		if r.Action == TriageDeep || r.Action == TriageSecuritySkim {
			deepCount++
		}
	}
	slog.Info("heuristic triage",
		"total", len(run.Diff.Files), "deep", deepCount,
		"pr", run.PREvent.PRNumber)

	// Phase 2: LLM refinement — only for manageable file counts
	if deepCount > 0 && deepCount <= 20 {
		llmResults, llmErr := ts.llmTriage(ctx, run)
		if llmErr != nil {
			// LLM triage failed — use heuristic results (non-fatal)
			slog.Warn("LLM triage failed, using heuristic results",
				"error", llmErr, "pr", run.PREvent.PRNumber)
		} else {
			// LLM overrides heuristic for files it classified
			for file, result := range llmResults {
				results[file] = result
			}
		}
	} else if deepCount > 20 {
		slog.Info("skipping LLM triage — too many deep files, reviewing all as deep",
			"deep_files", deepCount, "pr", run.PREvent.PRNumber)
	}

	// Review contract overrides: class-level routing wins over per-file
	// heuristics/LLM for non-production classes. Production class (or a nil
	// contract) changes nothing.
	if overridden := applyContractOverrides(run.Contract, results); overridden > 0 {
		slog.Info("triage contract overrides",
			"change_class", run.Contract.ChangeClass,
			"files_overridden", overridden,
			"pr", run.PREvent.PRNumber)
	}

	// Convert map to slice for PipelineRun
	triageSlice := make([]TriageResult, 0, len(results))
	for _, r := range results {
		triageSlice = append(triageSlice, r)
	}
	run.TriageResults = triageSlice

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventTriageComplete, map[string]any{
			"files": triageSlice,
		})
	}

	// Token usage is accumulated inside llmTriage if it ran
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

// llmTriage runs the existing LLM-based triage as an optional refinement step.
// Returns a map of file → TriageResult. Non-fatal — returns error if LLM fails.
func (ts *TriageStage) llmTriage(ctx context.Context, run *PipelineRun) (map[string]TriageResult, error) {
	provider, cfg, err := ts.registry.ResolveProvider(ctx, storeConfigLister{st: ts.store, installationID: run.DBInstallationID}, run.DBInstallationID, run.DBRepoID, llm.StageTriage)
	if err != nil {
		return nil, fmt.Errorf("resolve triage provider: %w", err)
	}

	owner, repo, splitErr := splitRepoFullName(run.PREvent.RepoFullName)
	if splitErr != nil {
		slog.Warn("triage: invalid repo name, skipping memory hints", "error", splitErr)
	}
	prompt := buildTriagePrompt(run.Diff.Files)
	if hints := triageMemoryHints(ctx, run.Indexer, run.Thresholds, owner, repo, run.Diff.Files); hints != "" {
		prompt += "\n" + hints
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      customOrDefault(run.Prompts, "triage_system", triageSystemPrompt),
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
		Stage:       "triage",
	})
	if err != nil {
		return nil, fmt.Errorf("triage LLM: %w", err)
	}

	// Accumulate token usage
	run.Tokens.Triage = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Triage)

	results, err := parseTriageResponse(resp.Content)
	if err != nil {
		slog.Warn("LLM triage parse failed",
			"error", err,
			"model", cfg.Model,
			"finish_reason", resp.FinishReason,
			"response_len", len(resp.Content),
			"response_prefix", util.Truncate(resp.Content, 300, true),
			"pr", run.PREvent.PRNumber)
		return nil, fmt.Errorf("parsing triage: %w", err)
	}

	m := make(map[string]TriageResult, len(results))
	for _, r := range results {
		m[r.File] = r
	}
	return m, nil
}

// applyContractOverrides mutates triage results per the review contract:
//   - one_time_script/docs/generated/test classes downgrade their deep files
//     to skim — except security-relevant paths, which keep their depth.
//   - migration class forces deep review on every SQL file (destructive DDL
//     must never be skimmed).
//
// Returns the number of files whose action changed. Nil contract or any other
// class (production, config, revert, empty/llm-pending) is a no-op.
func applyContractOverrides(c *ReviewContract, results map[string]TriageResult) int {
	if c == nil {
		return 0
	}
	overridden := 0
	switch c.ChangeClass {
	case ChangeClassOneTimeScript, ChangeClassDocs, ChangeClassGenerated, ChangeClassTest:
		for file, r := range results {
			if r.Action != TriageDeep || isSecurityRelevant(strings.ToLower(file)) {
				continue
			}
			r.Action = TriageSkim
			r.Reason = "contract: " + c.ChangeClass + " class"
			results[file] = r
			overridden++
		}
	case ChangeClassMigration:
		for file, r := range results {
			if !strings.HasSuffix(strings.ToLower(file), ".sql") || r.Action == TriageDeep {
				continue
			}
			r.Action = TriageDeep
			r.Reason = "contract: migration SQL forces deep"
			results[file] = r
			overridden++
		}
	}
	return overridden
}

// heuristicTriage classifies files using deterministic rules (file extension,
// path patterns, change size). Covers 80%+ of files instantly with zero LLM cost.
func heuristicTriage(files []diff.FileDiff) map[string]TriageResult {
	result := make(map[string]TriageResult, len(files))
	for _, f := range files {
		name := strings.ToLower(f.NewName)
		ext := filepath.Ext(name)
		lines := countAddedLines(f)

		var action TriageAction
		var reason string

		switch {
		case isSkippable(name, ext):
			action = TriageSkip
			reason = "generated/binary/vendored"
		case isSecurityRelevant(name):
			action = TriageSecuritySkim
			reason = "security-relevant path"
		case isSkimmable(name, ext, lines):
			action = TriageSkim
			reason = "config/docs/small change"
		default:
			action = TriageDeep
			reason = "code file with significant changes"
		}

		result[f.NewName] = TriageResult{
			File:   f.NewName,
			Action: action,
			Reason: reason,
		}
	}
	return result
}

func countAddedLines(f diff.FileDiff) int {
	count := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == diff.LineAdded {
				count++
			}
		}
	}
	return count
}

func isSkippable(name, ext string) bool {
	skipExts := map[string]bool{
		".lock": true, ".sum": true, ".map": true,
		".min.js": true, ".min.css": true,
		".svg": true, ".png": true, ".jpg": true, ".jpeg": true,
		".gif": true, ".ico": true, ".woff": true, ".woff2": true,
		".ttf": true, ".eot": true, ".pdf": true, ".zip": true,
	}
	skipPaths := []string{
		"vendor/", "node_modules/", "generated/", "__snapshots__/",
		"dist/", "build/", ".next/", "pnpm-lock", "package-lock",
		"yarn.lock", "go.sum",
	}
	if skipExts[ext] {
		return true
	}
	for _, p := range skipPaths {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

func isSkimmable(name, ext string, lines int) bool {
	skimExts := map[string]bool{
		".md": true, ".txt": true, ".yaml": true, ".yml": true,
		".toml": true, ".json": true,
	}
	skimFiles := []string{
		"dockerfile", "makefile", ".gitignore", "readme",
		".prettierrc", ".eslintrc", "tsconfig", "jest.config",
		".env.example",
	}
	if skimExts[ext] {
		return true
	}
	for _, f := range skimFiles {
		if strings.Contains(name, f) {
			return true
		}
	}
	if lines > 0 && lines < 10 {
		return true
	}
	return false
}

func isSecurityRelevant(name string) bool {
	keywords := []string{
		"auth", "token", "session", "secret", "crypt", "password",
		"permission", "rbac", "oauth", "jwt", "cors", "csrf",
		"credential", "login", "signin", "signup",
	}
	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
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

// searchHints runs a hint-style read (reranked, related+summary enriched,
// floored at the resolved FindingEnrich retrieval gate) and shapes the hits into
// render-ready strings, degrading to nil on a search error via the single
// BestEffort decorator. The shared adapter behind the triage and scoring memory
// blocks: the caller supplies the query scope/type/limit, searchHints stamps the
// hint retrieval knobs. thresholds is normalized here so a retried/resumed run's
// zero struct still floors at the default instead of 0.
func searchHints(ctx context.Context, indexer memory.Indexer, caller, container string, thresholds memory.Thresholds, q memory.MemoryQuery) []string {
	q.Threshold = thresholds.WithDefaults().FindingEnrich
	q.Rerank = true
	q.Enrich = true
	return memory.BestEffort(slog.Default(), caller, container, len(q.Query),
		func() ([]string, error) {
			m, err := indexer.Search(ctx, q)
			return memory.HintStrings(m), err
		})
}

// triageMemoryHints searches Supermemory for file synthesis docs, repo patterns,
// owner patterns, and rules matching changed files.
// Returns a hint block for the triage prompt, or empty string if no history found.
func triageMemoryHints(ctx context.Context, indexer memory.Indexer, thresholds memory.Thresholds, owner, repo string, files []diff.FileDiff) string {
	if indexer == nil || owner == "" || repo == "" || len(files) == 0 {
		return ""
	}

	var fileNames []string
	for _, f := range files {
		fileNames = append(fileNames, f.NewName)
	}
	query := filePathsQuery("file synthesis review history ", fileNames)
	// Post-refactor unified shape: repo container holds all repo memories
	// (filter by type at read time); shared container holds org-wide
	// patterns AND rules, so each shared read must pin type explicitly —
	// an untyped search mixes patterns and rules and degrades hint quality.
	repoTag := memory.RepoTagNew(repo)
	ownerTag := memory.SharedTag
	rulesTag := memory.SharedTag

	// Parallel searches for repo patterns, owner patterns, and rules. Each
	// searchHints degrades to nil on error via the single BestEffort decorator;
	// the deep Search owns the 5s timeout.
	var repoResults, ownerResults, ruleResults []string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		// type=synthesis: "File History (from past reviews)" is the file-scoped
		// review-history summary. The unified repo container also holds patterns,
		// scenarios, feedback, traces and review comments; an untyped search
		// (contradicting the comment above) mixes them into the history hints.
		repoResults = searchHints(ctx, indexer, "triage-synthesis", repoTag, thresholds, memory.MemoryQuery{
			Query: query, Repo: repo, Scope: memory.ScopeRepo, Type: memory.TypeSynthesis, Limit: 5,
		})
	}()
	go func() {
		defer wg.Done()
		ownerResults = searchHints(ctx, indexer, "triage-pattern", ownerTag, thresholds, memory.MemoryQuery{
			Query: query, Scope: memory.ScopeShared, Type: memory.TypePattern, Limit: 3,
		})
	}()
	go func() {
		defer wg.Done()
		ruleResults = searchHints(ctx, indexer, "triage-rule", rulesTag, thresholds, memory.MemoryQuery{
			Query: "review rules conventions", Scope: memory.ScopeShared, Type: memory.TypeRule, Limit: 3,
		})
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
