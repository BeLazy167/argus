package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/admission"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
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
Your ANALYSIS is exhaustive — trace every return path, every error branch, every boundary condition before concluding a file is clean. Depth of analysis, not volume of comments:
- Verify error handling on every path that can fail, including unlikely ones
- Check test coverage gaps when test files are in the diff
- Trace type safety through every conversion and boundary
Findings still follow the Review Laws: severity thresholds do not move, and analysis depth never manufactures comments.`

	case PersonaAdversarial:
		return `

## Persona: Adversarial Reviewer
Your ANALYSIS assumes the worst about every code path:
- For every function: "What input breaks this? What happens at 3 AM with bad data?"
- Assume every external input is malicious. Assume every network call will fail.
- Find the bug the author is sure doesn't exist. Find the edge case they didn't consider.
- Treat "it works on my machine" as a red flag, not a defense.
- Think like an attacker for security. Think like Murphy's Law for reliability.
The adversarial stance applies to your analysis only — findings follow the Review Laws for evidence, severity, and tone.`

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

	// Review budget limits. A soft limit reduces the review; a hard limit
	// refuses it. Nil inherits admission.DefaultLimits; an explicit 0 turns
	// that one measure off without touching the others.
	BudgetSoftFiles  *int   `json:"budget_soft_files,omitempty"`
	BudgetHardFiles  *int   `json:"budget_hard_files,omitempty"`
	BudgetSoftLines  *int   `json:"budget_soft_lines,omitempty"`
	BudgetHardLines  *int   `json:"budget_hard_lines,omitempty"`
	BudgetSoftTokens *int64 `json:"budget_soft_tokens,omitempty"`
	BudgetHardTokens *int64 `json:"budget_hard_tokens,omitempty"`
	// BudgetReducedMaxFiles is the file cap a reduced review runs under. An
	// operator who raises the soft limit needs this too, or the cap that fires
	// when it trips stays where it was.
	BudgetReducedMaxFiles *int `json:"budget_reduced_max_files,omitempty"`
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

// autoRunEnabled resolves the stored auto_run flag with load-failure awareness.
//
// Precedence: a repo-explicit value wins. When the repo is unset the org
// default decides — but only when we actually loaded it. orgLoadFailed=true
// means GetOrgDefaults errored, so we cannot prove the org didn't set
// auto_run=false; we fail CLOSED (return false) instead of applying the
// on-by-default, mirroring the adjacent auto-resolve gate. When both levels are
// genuinely unset and the loads succeeded, the default is ON (#161).
func autoRunEnabled(repoSettingsJSON, orgDefaultsJSON json.RawMessage, orgLoadFailed bool) bool {
	v, _ := autoRunSetting(repoSettingsJSON, orgDefaultsJSON, orgLoadFailed)
	return v
}

// autoRunSetting resolves the stored flag and reports whether anything was
// actually stored.
//
// The second return is what lets self-hosted be a DEFAULT rather than an
// override. Collapsing "stored false" and "unset" into one bool is what made
// SELF_HOSTED able to ignore an explicit auto_run=false — harmless while the
// default was on, and a trap once it became off, because a self-hoster could
// then never turn reviews off at all.
//
// Precedence: repo overrides org. A failed org-defaults load resolves to OFF
// and counts as explicit, so a database problem cannot be read as "unset" and
// quietly re-enable reviews.
func autoRunSetting(repoSettingsJSON, orgDefaultsJSON json.RawMessage, orgLoadFailed bool) (value, explicit bool) {
	if rs, ok := parseRepoSettings(repoSettingsJSON); ok && rs.AutoRun != nil {
		return *rs.AutoRun, true
	}
	if orgLoadFailed {
		return false, true
	}
	if os, ok := parseRepoSettings(orgDefaultsJSON); ok && os.AutoRun != nil {
		return *os.AutoRun, true
	}
	// Nothing stored. A review costs real money and, on a public repo, is
	// triggerable by people the maintainers have not vetted — so the safe
	// answer is to offer the trigger checkbox and let a maintainer decide.
	return false, false
}

// IsAutoRunEnabled resolves the stored auto_run flag assuming both settings
// blobs loaded cleanly. Deployment policy (self-hosted runs unconditionally)
// and load-failure handling are layered on top in decideAutoRun.
func IsAutoRunEnabled(repoSettingsJSON, orgDefaultsJSON json.RawMessage) bool {
	// Delegates to the same rule decideAutoRun uses, minus self-hosted, which
	// this caller (the incremental-plan decision) genuinely does not care
	// about. It used to re-implement the check and could disagree with the gate
	// that actually runs.
	return autoRunEnabled(repoSettingsJSON, orgDefaultsJSON, false)
}

// autoRunAction is the gate outcome for a webhook-driven PR event.
type autoRunAction int

const (
	// autoRunReview dispatches the review pipeline.
	autoRunReview autoRunAction = iota
	// autoRunSignal surfaces the on-demand "Trigger review" affordance because
	// auto-run is off, so the event is a visible signal rather than a silent
	// no-op. Posted idempotently — once per PR across opened + pushes (see
	// signalAutoRunDisabled).
	autoRunSignal
)

// decideAutoRun maps deployment config, stored repo/org settings, the
// org-defaults load state, and the webhook action to the auto-run gate outcome.
// Pure and side-effect free so the branch logic is unit-testable without a DB
// or GitHub client.
//
// Rules:
//   - manual (slash command / checkbox) always reviews — explicit user intent.
//   - self-hosted always reviews, UNCONDITIONALLY: there is no billing to gate,
//     so a self-host reviews on push regardless of stored settings.
//   - otherwise the stored flag decides (default ON; fail CLOSED when the org
//     load errored). When off, emit the trigger affordance.
func decideAutoRun(selfHosted bool, repoSettingsJSON, orgDefaultsJSON json.RawMessage, orgLoadFailed bool, action string) autoRunAction {
	// A manual action is a person asking, which Admission authorizes at the
	// launch site — the repo's auto-run policy does not apply to it.
	if action == "manual" {
		return autoRunReview
	}
	// The rule itself lives in internal/admission, the one place that decides
	// whether a review may run. This function is the adapter that resolves the
	// stored settings into the two booleans the rule takes.
	enabled, explicit := autoRunSetting(repoSettingsJSON, orgDefaultsJSON, orgLoadFailed)
	if admission.AutoRun(selfHosted, enabled, explicit).Allowed() {
		return autoRunReview
	}
	return autoRunSignal
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
		return "\nPersona lens: strict. Exhaustive analysis depth — trace every path; severity thresholds unchanged."
	case PersonaAdversarial:
		return "\nPersona lens: adversarial. Assume the worst about every code path in your analysis — find what breaks under pressure."
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

// SettingKeys returns every JSON key repoSettings defines.
//
// It is derived by reflection rather than written out, because this list used
// to exist in three places — repoSettings itself, the org-defaults write
// whitelist, and the repo-setting delete handler — and they drifted. A key
// missing from the write whitelist is silently dropped on save, which is how
// auto_run and the memory thresholds shipped as no-ops. One list cannot drift
// from itself.
func SettingKeys() map[string]bool {
	keys := make(map[string]bool)
	t := reflect.TypeOf(repoSettings{})
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.Index(tag, ","); comma >= 0 {
			tag = tag[:comma]
		}
		if tag != "" {
			keys[tag] = true
		}
	}
	return keys
}

// BudgetLimits resolves the review budget from merged settings, falling back to
// the defaults for anything unset. An explicit 0 survives: it disables that one
// measure, which is different from leaving it unset.
func BudgetLimits(settingsJSON json.RawMessage) admission.Limits {
	lim := admission.DefaultLimits
	rs, ok := parseRepoSettings(settingsJSON)
	if !ok {
		return lim
	}
	if rs.BudgetSoftFiles != nil {
		lim.SoftFiles = *rs.BudgetSoftFiles
	}
	if rs.BudgetHardFiles != nil {
		lim.HardFiles = *rs.BudgetHardFiles
	}
	if rs.BudgetSoftLines != nil {
		lim.SoftLines = *rs.BudgetSoftLines
	}
	if rs.BudgetHardLines != nil {
		lim.HardLines = *rs.BudgetHardLines
	}
	if rs.BudgetSoftTokens != nil {
		lim.SoftTokens = *rs.BudgetSoftTokens
	}
	if rs.BudgetHardTokens != nil {
		lim.HardTokens = *rs.BudgetHardTokens
	}
	if rs.BudgetReducedMaxFiles != nil {
		lim.ReducedMaxFiles = *rs.BudgetReducedMaxFiles
	}
	return lim
}

// ResolvedPersona is the overlay pair a run actually uses.
//
// Populated once per run from the personas table, falling back to the
// compiled-in overlays. The fallback is what makes this behaviour-preserving:
// before any row exists, and for any installation that never defines one, the
// review reads exactly the text it read when these lived in a switch.
type ResolvedPersona struct {
	Overlay        string
	SpecialistHint string
}

// personaReader is the narrow store surface persona resolution needs.
type personaReader interface {
	GetPersona(ctx context.Context, installationID int64, slug string) (*store.Persona, error)
}

// resolvePersona loads the overlay pair for a run.
//
// A stored persona wins over the compiled-in one of the same slug, which is how
// an installation retunes a built-in without losing its name. A miss or a read
// error falls back rather than failing: a review must not stop because somebody
// renamed a persona, and the compiled-in text is always a valid answer.
//
// PersonaCustom keeps its own path — its text lives in settings, not in a row —
// until the single custom slot is migrated into a named row of its own.
func resolvePersona(ctx context.Context, r personaReader, installationID int64, p Persona, customPrompt string) ResolvedPersona {
	fallback := ResolvedPersona{
		Overlay:        PersonaPromptOverlay(p),
		SpecialistHint: PersonaSpecialistHint(p),
	}
	if p == PersonaCustom {
		return ResolvedPersona{
			Overlay:        PersonaPromptOverlayCustom(customPrompt),
			SpecialistHint: PersonaSpecialistHintCustom(customPrompt),
		}
	}
	if r == nil || installationID == 0 {
		return fallback
	}
	row, err := r.GetPersona(ctx, installationID, string(p))
	if err != nil || row == nil {
		return fallback
	}
	return ResolvedPersona{Overlay: row.PromptOverlay, SpecialistHint: row.SpecialistHint}
}
