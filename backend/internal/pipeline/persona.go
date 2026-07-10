package pipeline

import (
	"encoding/json"
	"log/slog"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// Persona identifies a review style.
type Persona string

const (
	PersonaDefault             Persona = "default"
	PersonaSecurityAuditor     Persona = "security_auditor"
	PersonaPerformanceEngineer Persona = "performance_engineer"
	PersonaMentor              Persona = "mentor"
	PersonaArchitect           Persona = "architect"
	PersonaStrict              Persona = "strict"
	PersonaAdversarial         Persona = "adversarial"
	PersonaFreshEyes           Persona = "fresh_eyes"
	PersonaCustom              Persona = "custom"
)

// ValidPersonas is the set of valid persona values.
var ValidPersonas = map[Persona]bool{
	PersonaDefault: true, PersonaSecurityAuditor: true, PersonaPerformanceEngineer: true,
	PersonaMentor: true, PersonaArchitect: true, PersonaStrict: true, PersonaAdversarial: true, PersonaFreshEyes: true,
	PersonaCustom: true,
}

// PersonaPromptOverlay returns the system prompt addition for a given persona.
// For custom personas, use PersonaPromptOverlayCustom instead.
func PersonaPromptOverlay(p Persona) string {
	switch p {
	case PersonaSecurityAuditor:
		return `

## Persona: Security Auditor
You are reviewing with a security-first mindset. Prioritize:
- Injection vulnerabilities (SQL, XSS, command, LDAP)
- Authentication and authorization flaws
- Secrets, credentials, API keys in code
- Input validation gaps at every boundary
- Unsafe deserialization, path traversal, SSRF
- Cryptographic misuse (weak algorithms, hardcoded IVs)
Lower your threshold for security issues — flag anything suspicious even at "warning" level.
Non-security issues should only be reported if critical.`

	case PersonaPerformanceEngineer:
		return `

## Persona: Performance Engineer
You are reviewing with a performance-first mindset. Prioritize:
- N+1 queries and unbounded database calls
- Missing pagination on list endpoints
- Unnecessary allocations and copies in hot paths
- Goroutine/thread leaks and unclosed resources
- Missing caching opportunities
- Algorithmic complexity issues (O(n²) where O(n) is possible)
- Memory-inefficient data structures
Only report non-performance issues if they are critical bugs.`

	case PersonaMentor:
		return `

## Persona: Mentor
You are reviewing as a senior engineer mentoring a junior developer. Your tone is:
- Educational: explain WHY something is a problem, not just WHAT
- Encouraging: acknowledge good patterns before suggesting improvements
- Contextual: link to docs, articles, or language specs when relevant
- Patient: suggest learning paths for recurring issues
Frame every comment as a learning opportunity. Use phrases like "A common pattern here is..." or "This works, but here's why X is preferred..."`

	case PersonaArchitect:
		return `

## Persona: Architect
You are reviewing from a systems design perspective. Prioritize:
- Separation of concerns and module boundaries
- API contract design (backwards compatibility, versioning)
- Dependency direction (no circular deps, clean layering)
- Design patterns — appropriate use and misuse
- Coupling and cohesion analysis
- Interface design and abstraction quality
- Scalability implications of design choices
Ignore minor style or formatting issues.`

	case PersonaStrict:
		return `

## Persona: Strict Reviewer
You are an extremely thorough reviewer. Comment on everything:
- Every potential issue regardless of severity
- Style inconsistencies, naming conventions
- Missing error handling, even for unlikely cases
- Documentation gaps, missing comments on complex logic
- Test coverage gaps if test files are in the diff
- Type safety concerns
Do not skip anything. If in doubt, comment.`

	case PersonaAdversarial:
		return `

## Persona: Adversarial Reviewer
You HATE this implementation. Your job is to destroy it.
- For every function: "What input breaks this? What happens at 3 AM with bad data?"
- Assume every external input is malicious. Assume every network call will fail.
- Find the bug the author is sure doesn't exist. Find the edge case they didn't consider.
- Don't hold back. If the code is fragile, say so. If the logic is wrong, prove it.
- Treat "it works on my machine" as a red flag, not a defense.
- Think like an attacker for security. Think like Murphy's Law for reliability.
Still be professional — attack the code, not the author.`

	case PersonaFreshEyes:
		return `

## Persona: Fresh Eyes
You are reviewing this code as if you've never seen the codebase before. Your perspective is:
- "What does this do?" — If the intent isn't clear from names and structure alone, flag it
- "Why does this exist?" — Question any logic that isn't self-documenting
- Missing docstrings on public APIs and exported functions
- Confusing variable/function names that require context to understand
- Logic that is technically correct but would confuse a new team member
- Implicit assumptions that aren't documented anywhere
Frame comments as "A new developer would ask..." or "This isn't obvious because..."`

	default:
		return ""
	}
}

// PersonaPromptOverlayCustom returns the overlay for a custom persona prompt.
func PersonaPromptOverlayCustom(customPrompt string) string {
	if customPrompt == "" {
		return ""
	}
	return "\n\n## Persona: Custom\n" + customPrompt
}

// PersonaSpecialistHintCustom returns a condensed hint from a custom persona prompt.
func PersonaSpecialistHintCustom(customPrompt string) string {
	if customPrompt == "" {
		return ""
	}
	hint := customPrompt
	if len(hint) > 150 {
		hint = hint[:150] + "..."
	}
	return "\nPersona lens (custom): " + hint
}

// repoSettings is the JSON structure stored in repos.settings_json and
// installations.default_settings (org-wide defaults).
type repoSettings struct {
	Persona             string   `json:"persona,omitempty"`
	CustomPersonaPrompt string   `json:"custom_persona_prompt,omitempty"`
	DeepReview          bool     `json:"deep_review,omitempty"`
	CrossFileContext    *bool    `json:"cross_file_context,omitempty"`
	BlastRadius         *bool    `json:"blast_radius,omitempty"`
	ScenarioMemory      *bool    `json:"scenario_memory,omitempty"`
	CodeSimulation      *bool    `json:"code_simulation,omitempty"`
	PREnrichment        *bool    `json:"pr_enrichment,omitempty"`
	LearnPatterns       *bool    `json:"learn_patterns,omitempty"`
	LearnConventions    *bool    `json:"learn_conventions,omitempty"`
	FileSynthesis       *bool    `json:"file_synthesis,omitempty"`
	ArchitectureGraph   *bool    `json:"architecture_graph,omitempty"`
	SkipBaseBranches    []string `json:"skip_base_branches,omitempty"`
	AutoRun             *bool    `json:"auto_run,omitempty"`
	// AutoResolveEnabled gates the diff-based auto-resolve of stale review
	// threads on synchronize pushes. Default ON (see IsAutoResolveEnabled):
	// it's pure-diff with no LLM cost, and users who deliberately disable
	// review auto-run usually still want stale comments cleared when they
	// push fixes. Repos that want manual-only resolve control set this to
	// false.
	AutoResolveEnabled *bool `json:"auto_resolve_enabled,omitempty"`

	// Memory thresholds (Bundle 3). Nil (absent from JSON) means "inherit
	// the hardcoded default." An explicit number — including 0 and 1 — is
	// a real override: 0 disables the server-side similarity filter, 1
	// filters out everything. parseThresholds normalizes out-of-range
	// values (< 0 or > 1) and logs a Warn when an operator override is
	// rejected.
	ThresholdFindingEnrich   *float64 `json:"threshold_finding_enrich,omitempty"`
	ThresholdSpecialistMin   *float64 `json:"threshold_specialist_min,omitempty"`
	ThresholdScenarioTrigger *float64 `json:"threshold_scenario_trigger,omitempty"`
	ThresholdScenarioDedupe  *float64 `json:"threshold_scenario_dedupe,omitempty"`

	// DisableSharedDecay opts an installation OUT of the nightly _shared
	// retirement job (Bundle 5). Nil or false means decay is on.
	DisableSharedDecay *bool `json:"disable_shared_decay,omitempty"`

	// supermemory_qps / supermemory_burst intentionally omitted. Earlier
	// iterations added these fields but the Registry.GetClient path never
	// read them, making them dead code that misled operators. Tracked as
	// follow-up: plumb per-installation QPS through GetClient then re-add
	// here.
}

func parseRepoSettings(settingsJSON json.RawMessage) (repoSettings, bool) {
	if len(settingsJSON) == 0 {
		return repoSettings{}, false
	}
	var s repoSettings
	if err := json.Unmarshal(settingsJSON, &s); err != nil {
		slog.Warn("corrupt settings_json", "error", err)
		return repoSettings{}, false
	}
	return s, true
}

// isDeepReviewEnabled checks if deep review is enabled in repo settings.
func isDeepReviewEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return ok && s.DeepReview
}

// isCrossFileContextEnabled checks if cross-file context is enabled in repo settings.
// Defaults to true when not explicitly set.
func isCrossFileContextEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.CrossFileContext == nil || *s.CrossFileContext
}

// isBlastRadiusEnabled checks if blast radius analysis is enabled in repo settings.
// Defaults to true when not explicitly set.
func isBlastRadiusEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.BlastRadius == nil || *s.BlastRadius
}

// isScenarioMemoryEnabled checks if scenario memory is enabled in repo settings.
// Defaults to true when not explicitly set.
func isScenarioMemoryEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.ScenarioMemory == nil || *s.ScenarioMemory
}

// isCodeSimulationEnabled checks if code simulation is enabled in repo settings.
// Defaults to true when not explicitly set.
func isCodeSimulationEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.CodeSimulation == nil || *s.CodeSimulation
}

func isPREnrichmentEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.PREnrichment == nil || *s.PREnrichment
}

func isLearnPatternsEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.LearnPatterns == nil || *s.LearnPatterns
}

func isLearnConventionsEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.LearnConventions == nil || *s.LearnConventions
}

func isFileSynthesisEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.FileSynthesis == nil || *s.FileSynthesis
}

func isArchitectureGraphEnabled(settingsJSON json.RawMessage) bool {
	s, ok := parseRepoSettings(settingsJSON)
	return !ok || s.ArchitectureGraph == nil || *s.ArchitectureGraph
}

// parseThresholds resolves memory.Thresholds from merged settings JSON.
// Any field missing or outside [0, 1] falls back to the hardcoded default so
// a partial or corrupt settings blob can never produce nonsense thresholds.
// Out-of-range overrides log a Warn so operators discover the rejection
// instead of debugging for hours why their tuning "doesn't apply."
func parseThresholds(settingsJSON json.RawMessage) memory.Thresholds {
	t := memory.NewThresholds()
	s, ok := parseRepoSettings(settingsJSON)
	if !ok {
		return t
	}
	applyIfValid := func(name string, dst *float64, override *float64) {
		if override == nil {
			return
		}
		v := *override
		if v < 0 || v > 1 {
			slog.Warn("threshold override out of range, using default",
				"name", name, "value", v, "default", *dst, "valid_range", "[0,1]")
			return
		}
		*dst = v
	}
	applyIfValid("finding_enrich", &t.FindingEnrich, s.ThresholdFindingEnrich)
	applyIfValid("specialist_min", &t.SpecialistMin, s.ThresholdSpecialistMin)
	applyIfValid("scenario_trigger", &t.ScenarioTrigger, s.ThresholdScenarioTrigger)
	applyIfValid("scenario_dedupe", &t.ScenarioDedupe, s.ThresholdScenarioDedupe)
	return t
}

// IsAutoRunEnabled resolves the auto_run flag for a repo.
//
// Precedence: repo overrides org; nil at both levels defaults to OFF.
// Returns true only when the nearest explicitly set value is true.
//
// When the flag is OFF, PR webhook events (opened/synchronize/reopened) are
// NOT auto-dispatched to the review pipeline; instead the webhook layer posts
// a task-list "Trigger review" comment for on-demand execution.
func IsAutoRunEnabled(repoSettingsJSON, orgDefaultsJSON json.RawMessage) bool {
	if rs, ok := parseRepoSettings(repoSettingsJSON); ok && rs.AutoRun != nil {
		return *rs.AutoRun
	}
	if os, ok := parseRepoSettings(orgDefaultsJSON); ok && os.AutoRun != nil {
		return *os.AutoRun
	}
	return false
}

// IsAutoResolveEnabled resolves the auto_resolve_enabled flag for a repo.
//
// Precedence: repo overrides org; nil at both levels defaults to ON. This
// is the opposite default from IsAutoRunEnabled by design — auto-resolve
// is pure-diff (no LLM spend, no review noise) and intentionally runs on
// every synchronize regardless of whether the review pipeline fires.
// Users who truly want manual thread control set this explicitly to false.
func IsAutoResolveEnabled(repoSettingsJSON, orgDefaultsJSON json.RawMessage) bool {
	if rs, ok := parseRepoSettings(repoSettingsJSON); ok && rs.AutoResolveEnabled != nil {
		return *rs.AutoResolveEnabled
	}
	if os, ok := parseRepoSettings(orgDefaultsJSON); ok && os.AutoResolveEnabled != nil {
		return *os.AutoResolveEnabled
	}
	return true
}

// PersonaSpecialistHint returns a short directive for appending to specialist prompts.
// Condensed to avoid diluting specialist focus.
func PersonaSpecialistHint(p Persona) string {
	switch p {
	case PersonaSecurityAuditor:
		return "\nPersona lens: security-first. Weight security findings highest and flag any trust boundary violations."
	case PersonaPerformanceEngineer:
		return "\nPersona lens: performance-focused. Flag allocations, N+1 patterns, and unnecessary complexity."
	case PersonaMentor:
		return "\nPersona lens: mentor. Frame findings as learning opportunities with brief explanations of why."
	case PersonaArchitect:
		return "\nPersona lens: architect. Prioritize design patterns, coupling, and API surface concerns."
	case PersonaStrict:
		return "\nPersona lens: strict. Lower your threshold for reporting — flag anything questionable."
	case PersonaAdversarial:
		return "\nPersona lens: adversarial. Assume the worst about every code path — find what breaks under pressure."
	case PersonaFreshEyes:
		return "\nPersona lens: fresh eyes. Flag anything that isn't immediately obvious to a newcomer."
	default:
		return ""
	}
}

// loadPersona extracts the persona from a repo's settings_json.
func loadPersona(settingsJSON json.RawMessage) Persona {
	s, ok := parseRepoSettings(settingsJSON)
	if !ok || s.Persona == "" {
		return PersonaDefault
	}
	p := Persona(s.Persona)
	if !ValidPersonas[p] {
		slog.Warn("unknown persona in settings, defaulting", "persona", s.Persona)
		return PersonaDefault
	}
	return p
}

// loadCustomPersonaPrompt extracts the custom persona prompt from settings_json.
func loadCustomPersonaPrompt(settingsJSON json.RawMessage) string {
	s, ok := parseRepoSettings(settingsJSON)
	if !ok {
		return ""
	}
	return s.CustomPersonaPrompt
}
