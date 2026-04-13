package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/util"
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
- Race conditions — rules by runtime:
  * Go: goroutines create real data races. Flag shared state accessed from multiple goroutines without sync primitives (mutex, channel, atomic)
  * JavaScript/TypeScript: single-threaded event loop. NO data races possible unless using Worker threads or SharedArrayBuffer. Do NOT flag Promise.all, concurrent fetch, or callback patterns as race conditions
  * Python: GIL prevents most data races. Only flag with threading module + shared mutable state
- Edge cases the author didn't consider (empty arrays, zero values, max int, unicode)
- Silent data corruption and wrong return values
- Type coercion traps and implicit conversions
- Shallow copy bugs: spread operator on nested objects, JSON.parse(JSON.stringify) losing types

After identifying a potential bug, argue against yourself: is there a guard, validation, or invariant elsewhere that prevents this? Only report if the bug survives your own skepticism.

Prefer concrete examples: "When X is null and Y calls Z, this panics" over vague warnings.

Ignore style, naming, documentation. Only report real bugs with concrete failure scenarios.

**Output tone:** Your analysis should be adversarial, but the comments you write to the developer must be professional and constructive. Explain the bug clearly — attack the code, not the author.`

	case SpecialistSecurity:
		return `

## Role: Security Auditor

Assume every external input is attacker-controlled. Assume every network call will fail or be intercepted.

Threat model scoping:
- EXTERNAL code (API handlers, route handlers, middleware, controllers, endpoints, CLI args, file uploads): full attacker model applies. Flag injection, auth bypass, SSRF, etc.
- INTERNAL code (internal/, lib/, util/, helper/, pkg/, private methods): use "unexpected input" framing. Do NOT assume an attacker controls input to internal utility functions unless they are directly reachable from an external entry point
- When unsure: trace the call chain. If the function is only called from other internal functions, use internal framing
- The file path hints at audience: /api/, /handler/, /endpoint/ = external-facing; /internal/, /util/, /helpers/ = internal

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
Ignore non-security issues entirely.`

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

Ignore style, naming, minor formatting. Only report architectural and reliability issues.`

	case SpecialistRegression:
		return `

## Role: Regression & Edge Case Reviewer

You have two modes depending on whether code is modified or new.

### For MODIFIED code (changes to existing functions/classes):
- Changed function signatures that break existing callers
- Removed or renamed exported symbols, constants, or types
- Behavior changes in shared utilities that other code depends on
- Modified error codes, response shapes, or status codes downstream systems depend on
- Removed validation or authorization checks that were previously enforced
- Reordered operations that relied on a specific execution sequence

For regressions, explain WHAT previously worked and HOW this change breaks it.

### For NEW code (new files, new functions):
Hunt for what the author forgot — the gaps that will cause bugs in production:
- Missing boundary/edge case handling (empty input, zero, negative, max int, NaN, undefined)
- Missing error paths (what if the network call fails? what if the file doesn't exist?)
- Missing input validation (unbounded arrays, oversized strings, type coercion)
- Missing cleanup/disposal (event listeners, timers, connections, file handles)
- Off-by-one errors in loops, slices, or index math
- Race conditions or shared mutable state without synchronization (for JS/TS: only with Worker threads or interleaved Promise.all mutations; for Go: all shared state without mutex/channel)
- Implicit assumptions that aren't enforced (e.g. "array is sorted" but never checked)

For edge cases, explain the INPUT that triggers the bug and the CONSEQUENCE.

### Both modes:
- Cross-file consistency: if two files implement the same concept (e.g. hashing, serialization), flag mismatched implementations
- Do NOT report style, naming, or formatting issues`

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
		return "edge cases boundary conditions missing validation error handling regressions breaking changes"
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

// searchMemoryRich searches Supermemory with rerank + includes and returns enriched content.
// Non-fatal: returns nil on any error.
func searchMemoryRich(ctx context.Context, memClient *memory.Client, query, containerTag string, limit int) []string {
	resp, err := memClient.Search(ctx, memory.SearchRequest{
		Query:        query,
		ContainerTag: containerTag,
		SearchMode:   "hybrid",
		Limit:        limit,
		Threshold:    0.5,
		Rerank:       true,
		Include: &memory.SearchInclude{
			RelatedMemories: true,
			Summaries:       true,
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("memory search failed", "error", err, "tag", containerTag)
		}
		return nil
	}
	results := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		content := r.RichContent(2)
		if content != "" {
			results = append(results, util.Truncate(content, 500, true))
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

	var synthResults, repoResults, orgResults, negResults, posResults []string
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		if filePath != "" {
			synthResults = searchMemoryRich(searchCtx, memClient, "file synthesis "+filePath, repoTag, 2)
		}
	}()
	go func() {
		defer wg.Done()
		repoResults = searchMemoryRich(searchCtx, memClient, query, repoTag, 3)
	}()
	go func() {
		defer wg.Done()
		orgResults = searchMemoryRich(searchCtx, memClient, query, memory.OwnerTag(owner, "patterns"), 3)
	}()
	go func() {
		defer wg.Done()
		negResults = searchMemoryRich(searchCtx, memClient, query, memory.NegativePatternTag(owner, repo), 3)
	}()
	go func() {
		defer wg.Done()
		posResults = searchMemoryRich(searchCtx, memClient, query, memory.PositivePatternTag(owner, repo), 3)
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

	if block := formatMemoryBlock("\n## Known False Positives (DO NOT re-flag these patterns)\n", "", negResults); block != "" {
		sb.WriteString(block)
	}

	if block := formatMemoryBlock("\n## Approved Patterns (do not flag code following these)\n", "", posResults); block != "" {
		sb.WriteString(block)
	}

	if sb.Len() == 0 {
		return ""
	}
	footer := "\nUse this context to inform your review — issues matching known patterns are higher priority.\nWhen a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references."

	// Cap memory content to ~600 tokens (~2400 chars) BEFORE appending footer to avoid truncating instructions
	result := sb.String()
	if len(result) > 2400 {
		result = util.Truncate(result, 2400, true)
	}
	return result + footer
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

	var synthResults, repoPatterns, repoReviews, orgPatterns, orgRules, negResults, posResults []string
	var wg sync.WaitGroup
	wg.Add(7)
	go func() {
		defer wg.Done()
		if filePath != "" {
			synthResults = searchMemoryRich(searchCtx, memClient, "file synthesis "+filePath, repoPatternTag, 2)
		}
	}()
	go func() {
		defer wg.Done()
		repoPatterns = searchMemoryRich(searchCtx, memClient, query, repoPatternTag, 3)
	}()
	go func() {
		defer wg.Done()
		repoReviews = searchMemoryRich(searchCtx, memClient, query, repoReviewTag, 2)
	}()
	go func() {
		defer wg.Done()
		orgPatterns = searchMemoryRich(searchCtx, memClient, query, ownerPatternTag, 2)
	}()
	go func() {
		defer wg.Done()
		orgRules = searchMemoryRich(searchCtx, memClient, query, ownerRuleTag, 2)
	}()
	go func() {
		defer wg.Done()
		negResults = searchMemoryRich(searchCtx, memClient, query, memory.NegativePatternTag(owner, repo), 3)
	}()
	go func() {
		defer wg.Done()
		posResults = searchMemoryRich(searchCtx, memClient, query, memory.PositivePatternTag(owner, repo), 3)
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

	if block := formatMemoryBlock("\n## Known False Positives (DO NOT re-flag these patterns)\n", "", negResults); block != "" {
		sb.WriteString(block)
	}

	if block := formatMemoryBlock("\n## Approved Patterns (do not flag code following these)\n", "", posResults); block != "" {
		sb.WriteString(block)
	}

	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nApply these patterns and past findings when reviewing. When a finding matches a known pattern above, add a tag at the end of your comment: *[Matches pattern: <pattern description>]*. Only tag when there is a clear match — do not fabricate references.\n")

	// Cap total memory block to ~800 tokens (~3200 chars) to avoid drowning the review prompt
	result := sb.String()
	if len(result) > 3200 {
		result = util.Truncate(result, 3200, true)
	}
	return result
}