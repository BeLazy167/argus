package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/internal/util"
)

// SimulationRequest holds all inputs needed to simulate execution paths for a PR.
type SimulationRequest struct {
	Run          *PipelineRun
	Scenarios    []SimScenario
	BlastRadius  []BlastRadiusNode
	Traces       []DecisionTraceContext
	FileContents map[string]string // key files content
}

// SimScenario describes a single scenario to simulate against the PR change.
type SimScenario struct {
	Description string
	Severity    string
	Source      string
	Files       []string
}

// BlastRadiusNode represents a code symbol that depends on changed code.
type BlastRadiusNode struct {
	FilePath string
	Name     string
	Kind     string // function, class, etc.
	Depth    int    // distance from changed code
}

// DecisionTraceContext captures past issues or decisions related to changed files.
type DecisionTraceContext struct {
	FilePath  string
	TraceType string
	Content   string
	DaysAgo   int
}

// SimulationResult holds the outcome of simulating one scenario against the PR.
type SimulationResult struct {
	Scenario   string  // the scenario being tested
	Passes     bool    // does the change break this scenario?
	Confidence float64 // 0-1 confidence in the prediction
	RootCause  string  // if broken: what specifically breaks
	Impact     string  // who/what is affected
	Suggestion string  // suggested fix or investigation
}

// SimulationEngine runs LLM-based code path simulations for PR changes.
type SimulationEngine struct {
	registry *llm.Registry
	store    *store.Store
	ghClient *ghpkg.Client
	logger   *slog.Logger
}

// NewSimulationEngine creates a SimulationEngine.
func NewSimulationEngine(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, logger *slog.Logger) *SimulationEngine {
	return &SimulationEngine{registry: registry, store: st, ghClient: ghClient, logger: logger}
}

// RunSimulations executes scenario simulations for a PR and returns results.
// Each scenario is simulated independently. Low-confidence results are still
// returned but marked as uncertain.
func (e *SimulationEngine) RunSimulations(ctx context.Context, req SimulationRequest) ([]SimulationResult, error) {
	if len(req.Scenarios) == 0 {
		return nil, nil
	}

	// Resolve provider — use synthesis stage config
	lister := storeConfigLister{st: e.store, installationID: req.Run.DBInstallationID}
	provider, cfg, err := e.registry.ResolveProvider(ctx, lister, req.Run.DBInstallationID, req.Run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		// fallback to review
		provider, cfg, err = e.registry.ResolveProvider(ctx, lister, req.Run.DBInstallationID, req.Run.DBRepoID, llm.StageReview)
		if err != nil {
			return nil, fmt.Errorf("no provider for simulation: %w", err)
		}
	}

	var results []SimulationResult
	// Simulate up to 5 most critical scenarios
	limit := min(5, len(req.Scenarios))

	for _, scenario := range req.Scenarios[:limit] {
		result, err := e.simulateScenario(ctx, req, scenario, cfg, provider)
		if err != nil {
			desc := util.Truncate(scenario.Description, 50, true)
			e.logger.Warn("simulation failed for scenario", "error", err, "scenario", desc)
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

const simulationSystemPrompt = `You are a code simulation engine. Given a scenario (expected behavior), a code change (PR diff), and context about the codebase (dependency graph, past issues, file contents), predict whether the change breaks the scenario.

Think step by step:
1. Identify which parts of the code change are relevant to the scenario
2. Trace the execution path: how does data flow from the entry point through the changed code?
3. Predict: does the change alter behavior in a way that violates the scenario?
4. If yes: identify the root cause, impact, and suggest a fix
5. Rate your confidence (0-1)

Respond with JSON:
{
  "passes": true/false,
  "confidence": 0.85,
  "root_cause": "The change to X causes Y which breaks Z",
  "impact": "Affects all EU customers during billing cycle",
  "suggestion": "Add a null check before accessing the converted amount"
}

If you're unsure, set confidence < 0.5 and explain why in root_cause. It's better to flag uncertainty than to miss a real issue or raise a false alarm.`

func (e *SimulationEngine) simulateScenario(ctx context.Context, req SimulationRequest, scenario SimScenario, cfg llm.ModelConfig, provider llm.Provider) (SimulationResult, error) {
	prompt := buildSimulationPrompt(req, scenario)

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      simulationSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   600,
		Temperature: 0.3, // low temp for more deterministic reasoning
	})
	if err != nil {
		return SimulationResult{}, err
	}

	return parseSimulationResponse(resp.Content, scenario.Description)
}

// buildSimulationPrompt assembles the full prompt for a single scenario simulation.
func buildSimulationPrompt(req SimulationRequest, scenario SimScenario) string {
	var sb strings.Builder

	// PR context — sanitize user-controlled fields
	safeTitle := sanitizeUserInput(util.Truncate(req.Run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(req.Run.PREvent.PRAuthor, 100, false))
	sb.WriteString(fmt.Sprintf("## PR #%d: %s\nBy: %s\n\n",
		req.Run.PREvent.PRNumber, safeTitle, safeAuthor))

	// Scenario to test
	sb.WriteString(fmt.Sprintf("## Scenario to verify\n%s\nSeverity: %s | Source: %s\nRelated files: %s\n\n",
		scenario.Description, scenario.Severity, scenario.Source, strings.Join(scenario.Files, ", ")))

	// Changed files (diffs)
	sb.WriteString("## Code changes\n")
	for _, f := range req.Run.Diff.Files {
		sb.WriteString(fmt.Sprintf("### %s\n```diff\n%s\n```\n\n", f.NewName, util.Truncate(f.RawDiff, 2000, false)))
	}

	// Blast radius
	if len(req.BlastRadius) > 0 {
		sb.WriteString("## Blast radius (code that depends on changed files)\n")
		for _, n := range req.BlastRadius {
			sb.WriteString(fmt.Sprintf("- %s `%s` in %s (depth: %d)\n", n.Kind, n.Name, n.FilePath, n.Depth))
		}
		sb.WriteString("\n")
	}

	// Decision traces (past issues)
	if len(req.Traces) > 0 {
		sb.WriteString("## Past issues with these files\n")
		for _, t := range req.Traces {
			sb.WriteString(fmt.Sprintf("- [%s] %s — %s (%dd ago)\n", t.TraceType, t.FilePath, util.Truncate(t.Content, 100, true), t.DaysAgo))
		}
		sb.WriteString("\n")
	}

	// Key file contents
	if len(req.FileContents) > 0 {
		sb.WriteString("## Key file contents\n")
		for path, content := range req.FileContents {
			sb.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, truncateLines(content, 200)))
		}
	}

	sb.WriteString("## Task\nSimulate whether this PR change breaks the scenario described above. Respond with JSON.")
	return sb.String()
}

// parseSimulationResponse extracts structured simulation results from LLM JSON output.
func parseSimulationResponse(content string, scenario string) (SimulationResult, error) {
	result := SimulationResult{Scenario: scenario}

	var parsed struct {
		Passes     bool    `json:"passes"`
		Confidence float64 `json:"confidence"`
		RootCause  string  `json:"root_cause"`
		Impact     string  `json:"impact"`
		Suggestion string  `json:"suggestion"`
	}

	cleaned := extractJSON(content)
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return result, fmt.Errorf("failed to parse simulation response: %w", err)
	}

	result.Passes = parsed.Passes
	result.Confidence = max(0, min(1, parsed.Confidence))
	result.RootCause = parsed.RootCause
	result.Impact = parsed.Impact
	result.Suggestion = parsed.Suggestion
	return result, nil
}

// extractJSON finds the first JSON object in a string (handles LLM preamble).
// Correctly skips braces inside JSON string values.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	inString := false
	for i := start; i < len(s); i++ {
		if s[i] == '\\' && inString {
			i++ // skip escaped character
			continue
		}
		if s[i] == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

// FormatSimulationResults formats simulation results for the PR review body.
func FormatSimulationResults(results []SimulationResult) string {
	if len(results) == 0 {
		return ""
	}

	var failures []SimulationResult
	for _, r := range results {
		if !r.Passes && r.Confidence >= 0.5 {
			failures = append(failures, r)
		}
	}

	if len(failures) == 0 {
		return fmt.Sprintf("\n\n---\nSimulated %d scenarios — all pass.", len(results))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n\n---\n### Simulation Results\nTested %d scenarios, **%d potential issues found:**\n\n", len(results), len(failures)))
	for _, f := range failures {
		sb.WriteString(fmt.Sprintf("**Scenario:** %s\n", util.Truncate(f.Scenario, 200, true)))
		sb.WriteString(fmt.Sprintf("**Confidence:** %.0f%%\n", f.Confidence*100))
		sb.WriteString(fmt.Sprintf("**Root cause:** %s\n", f.RootCause))
		if f.Impact != "" {
			sb.WriteString(fmt.Sprintf("**Impact:** %s\n", f.Impact))
		}
		if f.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("**Suggestion:** %s\n", f.Suggestion))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
