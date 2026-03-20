package pipeline

import (
	"encoding/json"
	"log/slog"
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
)

// ValidPersonas is the set of valid persona values.
var ValidPersonas = map[Persona]bool{
	PersonaDefault: true, PersonaSecurityAuditor: true, PersonaPerformanceEngineer: true,
	PersonaMentor: true, PersonaArchitect: true, PersonaStrict: true, PersonaAdversarial: true, PersonaFreshEyes: true,
}

// PersonaPromptOverlay returns the system prompt addition for a given persona.
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

// repoSettings is the JSON structure stored in repos.settings_json.
type repoSettings struct {
	Persona    string `json:"persona,omitempty"`
	DeepReview bool   `json:"deep_review,omitempty"`
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
