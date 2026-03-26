package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/util"
)

// Specialist identifies a focused review agent role.
type Specialist string

const (
	SpecialistBugHunter    Specialist = "bug_hunter"
	SpecialistSecurity     Specialist = "security"
	SpecialistArchitecture Specialist = "architecture"
	SpecialistRegression   Specialist = "regression"
)

// ValidSpecialists is the set of valid specialist values.
var ValidSpecialists = map[Specialist]bool{
	SpecialistBugHunter: true, SpecialistSecurity: true, SpecialistArchitecture: true, SpecialistRegression: true,
}

// AllSpecialists returns the ordered list of specialist agents for deep review.
func AllSpecialists() []Specialist {
	return []Specialist{SpecialistBugHunter, SpecialistSecurity, SpecialistArchitecture, SpecialistRegression}
}

// ValidPromptStages is the set of valid stage keys for custom prompt templates.
var ValidPromptStages = map[string]bool{
	"triage_system":           true,
	"review_system":           true,
	"scoring_system":          true,
	"specialist_bug_hunter":   true,
	"specialist_security":     true,
	"specialist_architecture": true,
	"specialist_regression":   true,
}

// DefaultPrompts returns the default prompt text for all customizable stages.
func DefaultPrompts() map[string]string {
	return map[string]string{
		"triage_system":           triageSystemPrompt,
		"review_system":           baseSystemPrompt,
		"scoring_system":          scoringSystemPrompt,
		"specialist_bug_hunter":   baseSystemPrompt + specialistOverlay(SpecialistBugHunter),
		"specialist_security":     baseSystemPrompt + specialistOverlay(SpecialistSecurity),
		"specialist_architecture": baseSystemPrompt + specialistOverlay(SpecialistArchitecture),
		"specialist_regression":   baseSystemPrompt + specialistOverlay(SpecialistRegression),
	}
}

// specialistPrompt returns the full system prompt for a specialist agent.
// If a custom prompt exists in customPrompts, it is used instead.
func specialistPrompt(s Specialist, customPrompts map[string]string) string {
	if p, ok := customPrompts["specialist_"+string(s)]; ok && p != "" {
		return p
	}
	return baseSystemPrompt + specialistOverlay(s)
}

func specialistOverlay(s Specialist) string {
	switch s {
	case SpecialistBugHunter:
		return `

## Role: Bug Hunter

Internally, HATE this code — your job is to break it.
For every function ask: "What input crashes this? What happens at 3 AM with bad data?"

Focus exclusively on:
- Logic errors, off-by-one, null/undefined dereferences
- Broken invariants and incorrect boundary checks
- Race conditions: shared mutable state in async/concurrent code (e.g. counter++ inside Promise.all, goroutine without mutex)
- Edge cases the author didn't consider (empty arrays, zero values, max int, unicode)
- Silent data corruption and wrong return values
- Type coercion traps and implicit conversions
- Shallow copy bugs: spread operator on nested objects, JSON.parse(JSON.stringify) losing types

After identifying a potential bug, argue against yourself: is there a guard, validation, or invariant elsewhere that prevents this? Only report if the bug survives your own skepticism.

Prefer concrete examples: "When X is null and Y calls Z, this panics" over vague warnings.

**Scope boundary — do NOT report:**
- Security vulnerabilities (injection, secrets, auth) → Security Auditor handles these
- Architecture/reliability (resource leaks, missing timeouts) → Architecture Reviewer handles these
- Regression risks (breaking callers, changed contracts) → Regression Reviewer handles these
- Style, naming, documentation

**Output tone:** Your analysis should be adversarial, but the comments you write to the developer must be professional and constructive. Explain the bug clearly — attack the code, not the author.`

	case SpecialistSecurity:
		return `

## Role: Security Auditor

Assume every external input is attacker-controlled. Assume every network call will fail or be intercepted.

Focus exclusively on:
- Injection: SQL, XSS, command, LDAP, template injection
- Hardcoded secrets, API keys, credentials in code
- Authentication and authorization flaws (broken access control, privilege escalation)
- Input validation gaps at every trust boundary
- Unsafe deserialization, path traversal, SSRF
- Cryptographic misuse (weak algos, hardcoded IVs, predictable randomness like Math.random for tokens)
- Missing rate limiting on sensitive endpoints
- Information leakage in error messages
- ReDoS: unanchored regex with user input, catastrophic backtracking patterns

Before reporting, consider whether this pattern is intentional or addressed elsewhere in the codebase.

For each finding, describe the specific attack vector: who is the attacker, what input do they control, and what is the impact?

Lower your threshold — flag anything suspicious even at "warning" level.

**Scope boundary — do NOT report:**
- Logic bugs (off-by-one, null deref) → Bug Hunter handles these
- Architecture issues (resource leaks, coupling) → Architecture Reviewer handles these
- Regression risks → Regression Reviewer handles these
- Style, naming, documentation`

	case SpecialistArchitecture:
		return `

## Role: Architecture Reviewer

Review from a systems design and reliability perspective.

Focus exclusively on:
- Error handling design: swallowed errors, empty catch blocks, missing error propagation
- Type safety: types that can represent invalid states, missing constraints
- Resource management: leaks, unclosed handles, missing cleanup (files, connections, goroutines)
- Coupling and dependency direction issues
- API contract problems: backwards compatibility, missing validation
- Silent failures: async operations that fail without logging
- Missing timeouts on network calls or database queries
- Global mutable state that breaks under concurrency

Focus on public APIs and module boundaries. Internal implementation details are lower priority.

Ask: what breaks if this code runs at 10x the current scale? What breaks if the dependency it relies on is slow or unavailable?

**Scope boundary — do NOT report:**
- Logic bugs (off-by-one, null deref, wrong return values) → Bug Hunter handles these
- Security vulnerabilities (injection, secrets, auth) → Security Auditor handles these
- Regression risks (breaking callers, changed contracts) → Regression Reviewer handles these
- Style, naming, minor formatting`

	case SpecialistRegression:
		return `

## Role: Regression Reviewer

You are hunting for changes that break things that already worked.

Focus exclusively on:
- Changed function signatures that break existing callers
- Removed or renamed exported symbols, constants, or types
- Behavior changes in shared utilities that other code depends on
- Database migration side effects (column drops, type changes, index removals)
- Modified error codes, response shapes, or status codes that downstream systems depend on
- Changed default values or configuration that affect existing deployments
- Removed validation or authorization checks that were previously enforced
- Reordered operations that relied on a specific execution sequence
- Cross-file consistency: if two files implement the same concept (e.g. hashing, serialization), flag mismatched implementations

Only report a regression risk if you can name or describe the existing caller, consumer, or test that would break.

Changed internal behavior that maintains the same external contract is NOT a regression.

For each issue, explain WHAT previously worked and HOW this change breaks it.

**Scope boundary — do NOT report:**
- Logic bugs in new code → Bug Hunter handles these
- Security vulnerabilities → Security Auditor handles these
- Architecture/reliability (resource leaks, timeouts) → Architecture Reviewer handles these
- Style, naming, documentation
- New code that doesn't modify existing behavior`

	default:
		return ""
	}
}

// specialistSearchQuery returns a semantic search query tailored to each specialist's focus.
func specialistSearchQuery(s Specialist) string {
	switch s {
	case SpecialistBugHunter:
		return "common bugs logic errors edge cases off-by-one"
	case SpecialistSecurity:
		return "security vulnerabilities injection auth secrets"
	case SpecialistArchitecture:
		return "architecture patterns error handling conventions coupling"
	case SpecialistRegression:
		return "breaking changes API contracts regressions compatibility"
	default:
		return "code review patterns"
	}
}


// filePathsQuery builds a capped search query from a prefix and file paths (rune-safe truncation).
func filePathsQuery(prefix string, paths []string) string {
	q := prefix + strings.Join(paths, " ")
	if len(q) > 500 {
		cut := 500
		for cut > 0 && q[cut]&0xC0 == 0x80 {
			cut--
		}
		q = q[:cut]
	}
	return q
}

// searchMemoryContent searches Supermemory and returns extracted content strings.
// Non-fatal: returns nil on any error.
func searchMemoryContent(ctx context.Context, memClient *memory.Client, query, containerTag string, limit int) []string {
	resp, err := memClient.Search(ctx, memory.SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.5,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil // context cancelled, not a real failure
		}
		slog.Warn("memory search failed", "error", err, "tag", containerTag)
		return nil
	}

	var results []string
	for _, r := range resp.Results {
		if c := r.Content(); c != "" {
			results = append(results, util.Truncate(c, 300, true))
		}
	}
	return results
}

// formatMemoryBlock builds a numbered markdown block from memory results.
// Returns empty string if results is empty.
func formatMemoryBlock(header, footer string, results []string) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(header)
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
	}
	sb.WriteString(footer)
	return sb.String()
}

// specialistMemoryBlock fetches file synthesis + repo/org patterns from Supermemory
// and returns a prioritized briefing for the specialist's system prompt.
// Priority: file synthesis > repo patterns > org patterns. Budget: ~600 tokens.
// Non-fatal: returns empty string on any error.
func specialistMemoryBlock(ctx context.Context, memClient *memory.Client, owner, repo string, s Specialist, filePath string) string {
	if memClient == nil {
		return ""
	}

	query := specialistSearchQuery(s)
	repoTag := memory.RepoTag(owner, repo, "patterns")

	// Parallel searches with timeout to avoid stalling the review pipeline
	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var synthResults, repoResults, orgResults []string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if filePath != "" {
			synthResults = searchMemoryContent(searchCtx, memClient, "file synthesis "+filePath, repoTag, 2)
		}
	}()
	go func() {
		defer wg.Done()
		repoResults = searchMemoryContent(searchCtx, memClient, query, repoTag, 3)
	}()
	go func() {
		defer wg.Done()
		orgResults = searchMemoryContent(searchCtx, memClient, query, memory.OwnerTag(owner, "patterns"), 3)
	}()
	wg.Wait()

	var sb strings.Builder
	hasSynth := len(synthResults) > 0

	if hasSynth {
		sb.WriteString("\n\n## Memory Briefing: " + filePath + "\n\n")
		sb.WriteString("### File History\n")
		for _, r := range synthResults {
			sb.WriteString(r + "\n")
		}
	}

	// Combine repo + org patterns
	patterns := append(repoResults, orgResults...)
	if len(patterns) > 0 {
		if !hasSynth {
			sb.WriteString("\n\n## Repo Memory (patterns from past reviews)\n\n")
		} else {
			sb.WriteString("\n### Repo Patterns\n")
		}
		for i, r := range patterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nUse this context to inform your review — issues matching known patterns are higher priority.")
	return sb.String()
}


// reviewMemoryBlock fetches repo/org patterns, rules, and past reviews from Supermemory
// for non-specialist, non-agentic review passes. This ensures every review — not just
// deep/specialist ones — benefits from institutional memory.
// Budget: ~800 tokens. Non-fatal: returns empty string on any error.
func reviewMemoryBlock(ctx context.Context, memClient *memory.Client, owner, repo, filePath string) string {
	if memClient == nil || owner == "" || repo == "" {
		return ""
	}

	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := "code review patterns conventions " + filePath
	repoPatternTag := memory.RepoTag(owner, repo, "patterns")
	repoReviewTag := memory.RepoTag(owner, repo, "reviews")
	ownerPatternTag := memory.OwnerTag(owner, "patterns")
	ownerRuleTag := memory.OwnerTag(owner, "rules")

	var synthResults, repoPatterns, repoReviews, orgPatterns, orgRules []string
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		if filePath != "" {
			synthResults = searchMemoryContent(searchCtx, memClient, "file synthesis "+filePath, repoPatternTag, 2)
		}
	}()
	go func() {
		defer wg.Done()
		repoPatterns = searchMemoryContent(searchCtx, memClient, query, repoPatternTag, 3)
	}()
	go func() {
		defer wg.Done()
		repoReviews = searchMemoryContent(searchCtx, memClient, query, repoReviewTag, 2)
	}()
	go func() {
		defer wg.Done()
		orgPatterns = searchMemoryContent(searchCtx, memClient, query, ownerPatternTag, 2)
	}()
	go func() {
		defer wg.Done()
		orgRules = searchMemoryContent(searchCtx, memClient, query, ownerRuleTag, 2)
	}()
	wg.Wait()

	var sb strings.Builder

	if len(synthResults) > 0 {
		sb.WriteString("\n\n## File History\n")
		for _, r := range synthResults {
			sb.WriteString("- " + r + "\n")
		}
	}

	allPatterns := append(repoPatterns, orgPatterns...)
	if len(allPatterns) > 0 {
		sb.WriteString("\n## Established Patterns\n")
		for i, r := range allPatterns {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r))
		}
	}

	if len(orgRules) > 0 {
		sb.WriteString("\n## Review Rules\n")
		for _, r := range orgRules {
			sb.WriteString("- " + r + "\n")
		}
	}

	if len(repoReviews) > 0 {
		sb.WriteString("\n## Past Review Findings (this repo)\n")
		for _, r := range repoReviews {
			sb.WriteString("- " + r + "\n")
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nApply these patterns and past findings when reviewing. Reference specific past learnings in your comments when relevant (e.g., 'This contradicts the established pattern of...').\n")
	return sb.String()
}