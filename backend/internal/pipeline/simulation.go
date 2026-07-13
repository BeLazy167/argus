package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
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
	ID          int64 // matches store.Scenario.ID so results can be written back
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
//
// Verdict + Why + Fix are the user-facing triplet rendered in the PR body and dashboard.
// RootCause + Impact + Suggestion stay populated for the export API and for future analytics;
// the plainer Why/Fix are short, single-sentence renditions derived by the LLM from the longer
// fields.
type SimulationResult struct {
	ScenarioID int64   // back-ref to store.Scenario; zero when scenario is unseeded
	Scenario   string  // the scenario description being tested
	Passes     bool    // does the PR preserve the scenario?
	Verdict    string  // broken | fixed | partial | unclear — derived from Passes + Confidence when missing
	Confidence float64 // 0–1 confidence in the prediction
	Why        string  // one-sentence plain-English explanation rendered on GitHub + dashboard
	Fix        string  // one-sentence suggested fix
	RootCause  string  // long-form root-cause paragraph (kept for exports / debugging)
	Impact     string  // long-form impact paragraph (kept for exports / debugging)
	Suggestion string  // long-form fix suggestion (kept for exports / debugging)
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
	// Simulate up to 5 scenarios per PR. The candidate list is already ranked by UCB1 score
	// in SQL (see docs/plans/2026-04-17-ucb-scenario-selection.md) — the first 5 are the
	// best balance of exploitation (scenarios that find real bugs) and exploration
	// (newcomers without runs). Do not re-sort in Go; that would defeat the SQL ordering.
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

	// Persist each outcome to scenario_runs + denormalized last_* on scenarios. Non-fatal —
	// the PR review body is the user-visible contract; DB persistence is for the dashboard
	// and can be skipped without breaking the review.
	e.persistScenarioRuns(ctx, req, results)

	// Aggregate stream signal — silent when zero scenarios actually ran.
	if len(results) > 0 && req.Run.EventBus != nil {
		req.Run.EventBus.Publish(req.Run.ReviewID, EventSimulationsComplete, map[string]any{
			"total":  len(results),
			"passed": countPassedSimulations(results),
		})
	}

	return results, nil
}

// countPassedSimulations counts scenarios whose verdict indicates the code
// passed ("fixed"). "broken"/"partial"/"unclear" all count as non-passing.
func countPassedSimulations(results []SimulationResult) int {
	n := 0
	for _, r := range results {
		if r.Verdict == "fixed" {
			n++
		}
	}
	return n
}

// persistScenarioRuns writes each simulation outcome to the DB. Any failure is logged at Warn
// and the loop continues — same non-fatal pattern used for Supermemory indexing.
func (e *SimulationEngine) persistScenarioRuns(ctx context.Context, req SimulationRequest, results []SimulationResult) {
	if e.store == nil || req.Run == nil {
		return
	}
	for _, r := range results {
		if r.ScenarioID == 0 {
			continue // unseeded scenario (e.g. ad-hoc sim without a DB row)
		}
		if _, err := e.store.CreateScenarioRun(ctx, r.ScenarioID, req.Run.ReviewID, req.Run.PREvent.PRNumber,
			r.Verdict, r.Confidence, r.Why, r.Fix, r.RootCause, r.Impact); err != nil {
			e.logger.Warn("persist scenario run failed", "scenario_id", r.ScenarioID, "error", err)
			continue
		}
		// Skip the trigger-count bump when the last-run denorm update fails — otherwise the
		// counter drifts above the actual run history (one trigger recorded in scenario_runs
		// but no corresponding last_* summary refresh).
		if err := e.store.UpdateScenarioLastRun(ctx, r.ScenarioID, r.Verdict, r.Confidence, r.Why, r.Fix,
			req.Run.PREvent.PRNumber, req.Run.ReviewID); err != nil {
			e.logger.Warn("update scenario last-run failed", "scenario_id", r.ScenarioID, "error", err)
			continue
		}
		if err := e.store.IncrementScenarioTriggerCount(ctx, r.ScenarioID); err != nil {
			e.logger.Warn("increment scenario trigger count failed", "scenario_id", r.ScenarioID, "error", err)
		}
	}
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
  "verdict": "broken" | "fixed" | "partial" | "unclear",
  "why": "One short sentence, ≤20 words, plain English, no jargon.",
  "fix": "One short sentence with the concrete action, ≤20 words.",
  "root_cause": "Full paragraph for the export/debug API — same information as 'why' but expanded with mechanics.",
  "impact": "Full paragraph describing who/what is affected.",
  "suggestion": "Full paragraph with a concrete fix — same information as 'fix' but with code example if relevant."
}

Rules:
- 'why' and 'fix' MUST each be a single sentence. No lists, no multi-sentence prose. If you don't have a fix, set 'fix' to "" (empty string).
- 'verdict' MUST be one of the four literal strings. Pick 'fixed' when passes is true, 'broken' when passes is false and confidence ≥ 0.8, 'partial' when passes is false and confidence ≥ 0.5, 'unclear' otherwise.
- If you're unsure, set confidence < 0.5 and use 'unclear'. It's better to flag uncertainty than to miss a real issue or raise a false alarm.`

func (e *SimulationEngine) simulateScenario(ctx context.Context, req SimulationRequest, scenario SimScenario, cfg llm.ModelConfig, provider llm.Provider) (SimulationResult, error) {
	prompt := buildSimulationPrompt(req, scenario)

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      simulationSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   600,
		Temperature: 0.3, // low temp for more deterministic reasoning
		Stage:       "simulation",
	})
	if err != nil {
		return SimulationResult{}, err
	}

	// Track this scenario's cost under run.Tokens.Simulation[]. Order is
	// scenario-selection order (UCB1 top-5, stable). Lock-guarded via
	// addSimulation since multiple scenarios may run concurrently.
	req.Run.Tokens.addSimulation(StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	})

	result, err := parseSimulationResponse(resp.Content, scenario.Description)
	if err != nil {
		return SimulationResult{}, err
	}
	result.ScenarioID = scenario.ID

	if req.Run.EventBus != nil {
		req.Run.EventBus.Publish(req.Run.ReviewID, EventScenarioSimulated, map[string]any{
			"scenario_id": scenario.ID,
			"verdict":     result.Verdict,
			"files":       len(scenario.Files),
		})
	}
	return result, nil
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
		Verdict    string  `json:"verdict"`
		Why        string  `json:"why"`
		Fix        string  `json:"fix"`
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
	result.Verdict = normalizeVerdict(parsed.Verdict, parsed.Passes, result.Confidence)
	result.Why = parsed.Why
	result.Fix = parsed.Fix
	result.RootCause = parsed.RootCause
	result.Impact = parsed.Impact
	result.Suggestion = parsed.Suggestion
	// Backfill plain-English fields when the LLM forgot to populate them. Keeps the UI
	// from rendering empty "Why:" lines when we still have the longer root_cause / suggestion.
	if result.Why == "" {
		result.Why = firstSentence(parsed.RootCause)
	}
	if result.Fix == "" {
		result.Fix = firstSentence(parsed.Suggestion)
	}
	return result, nil
}

// normalizeVerdict enforces the four-value taxonomy. When the LLM supplies a verdict that's
// consistent with `passes` we trust it; otherwise (missing, invalid, or contradictory) we
// derive from (passes, confidence). Contradictions matter — `passes=false` with `verdict=fixed`
// would silently hide a broken scenario from the PR body and persist as "fixed" forever.
func normalizeVerdict(raw string, passes bool, confidence float64) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	valid := v == "broken" || v == "fixed" || v == "partial" || v == "unclear"
	// Reject contradictions between passes and verdict — trust `passes` (the structured boolean
	// is harder for the LLM to get wrong than a free-form label).
	contradictsPasses := (passes && v == "broken") || (passes && v == "partial") || (!passes && v == "fixed")
	if valid && !contradictsPasses {
		return v
	}
	switch {
	case passes:
		return "fixed"
	case confidence >= 0.8:
		return "broken"
	case confidence >= 0.5:
		return "partial"
	default:
		return "unclear"
	}
}

// firstSentence returns the first sentence of s (split on '. '). Used as a fallback renderer
// when the LLM emitted the long-form paragraphs but skipped the one-line why/fix.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, ". "); i > 0 && i < 200 {
		return s[:i+1]
	}
	return util.Truncate(s, 200, true)
}

// splitHeadlineAndBody parses a conversational-brief response produced by the
// synthesis prompt and separates the explicit `**Headline:** …` line from the
// rest of the body. The synthesis system prompt requires a ≤100-char Headline
// line; this helper is how we extract it. Returns ("", brief) when no headline
// is found — the caller's fallback path (extractHeadline) then derives one
// from the brief's first sentence.
//
// The returned body has the Headline line stripped so the posted comment
// doesn't show both the H2 one-liner and the Headline prefix again.
func splitHeadlineAndBody(brief string) (headline, body string) {
	s := strings.TrimSpace(brief)
	// Look for "**Headline:**" at the start, case-insensitive on the word
	// itself (tolerant of slight drift like "**headline:**" from the LLM).
	lower := strings.ToLower(s)
	const marker = "**headline:**"
	if !strings.HasPrefix(lower, marker) {
		return "", s
	}
	after := strings.TrimSpace(s[len(marker):])
	// The headline is the first line / up to the first blank line.
	eol := strings.Index(after, "\n")
	if eol < 0 {
		return strings.TrimSpace(after), ""
	}
	headline = strings.TrimSpace(after[:eol])
	body = strings.TrimSpace(after[eol:])
	return headline, body
}

// extractHeadline is the FALLBACK when the synthesis LLM didn't produce the
// required `**Headline:** …` line. It turns a plain conversational brief body
// into the one-liner that fits after `## 🔎 Argus · N/10 — ` in the H2.
//
// Two rough edges on the naive firstSentence-plus-truncate path — both
// surfaced in a production review:
//
//  1. The brief nearly always starts with "**Verdict:**" (per the synthesis
//     system prompt), so the raw first sentence puts bold markdown inside
//     the H2 — which renders awkwardly as nested bold.
//  2. Rune-count truncation cuts mid-word ("dependency/…"). We look for the
//     last word boundary below maxRunes and trim there instead.
//
// If maxRunes ≤ 0, returns the stripped first sentence uncut.
func extractHeadline(brief string, maxRunes int) string {
	s := strings.TrimSpace(brief)
	// Strip a leading "**Label:**" or "**Label**" bold prefix. We specifically
	// target the opening ** pair; anything after that ** is treated as prose.
	if strings.HasPrefix(s, "**") {
		if close := strings.Index(s[2:], "**"); close >= 0 {
			s = strings.TrimSpace(s[close+4:])
		}
	}
	s = firstSentence(s)
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	// Walk back to the last space within the limit so we don't break mid-word.
	cut := maxRunes
	for cut > 0 && runes[cut-1] != ' ' {
		cut--
	}
	if cut < maxRunes/2 {
		// No word boundary in the back half — fall through to a hard cut
		// rather than return a near-empty fragment.
		cut = maxRunes
	}
	return strings.TrimRight(string(runes[:cut]), " ,;:") + "…"
}

// verdictLabel maps an internal verdict to the human-facing label used in GitHub comments.
func verdictLabel(verdict string) string {
	switch verdict {
	case "broken":
		return "Broken"
	case "fixed":
		return "Fixed"
	case "partial":
		return "Partial fix"
	case "unclear":
		return "Unclear"
	default:
		return "Unclear"
	}
}

// extractJSON finds the first JSON object in a string (handles LLM preamble).
// Strips markdown code fences and correctly skips braces inside JSON string values.
func extractJSON(s string) string {
	s = stripCodeFences(s)
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
//
// Emits a scannable 4-line block per scenario:
//
//	**Scenario:** <one-liner>
//	**Verdict:** <Broken|Partial fix|Unclear> (<N>% sure)
//	**Why:** <one sentence>
//	**Fix:** <one sentence>
//
// Filtering: only scenarios with verdict ∈ {broken, partial, unclear} AND confidence ≥ 0.5 are
// rendered — "fixed" and low-confidence results clutter the review without adding signal.
func FormatSimulationResults(results []SimulationResult) string {
	if len(results) == 0 {
		return ""
	}

	var failures []SimulationResult
	for _, r := range results {
		if r.Verdict == "fixed" {
			continue
		}
		if r.Confidence < 0.5 {
			continue
		}
		failures = append(failures, r)
	}

	if len(failures) == 0 {
		return fmt.Sprintf("\n\n---\nSimulated %d scenarios — all pass.", len(results))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n\n---\n### Simulation Results\nTested %d scenarios, **%d potential issues found:**\n\n", len(results), len(failures)))
	for _, f := range failures {
		sb.WriteString(fmt.Sprintf("**Scenario:** %s\n", util.Truncate(f.Scenario, 200, true)))
		sb.WriteString(fmt.Sprintf("**Verdict:** %s (%.0f%% sure)\n", verdictLabel(f.Verdict), f.Confidence*100))
		if f.Why != "" {
			sb.WriteString(fmt.Sprintf("**Why:** %s\n", f.Why))
		}
		if f.Fix != "" {
			sb.WriteString(fmt.Sprintf("**Fix:** %s\n", f.Fix))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
