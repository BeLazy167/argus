package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// Specialist identifies a focused review agent role.
type Specialist string

const (
	SpecialistBugHunter    Specialist = "bug_hunter"
	SpecialistSecurity     Specialist = "security"
	SpecialistArchitecture Specialist = "architecture"
	SpecialistRegression   Specialist = "regression"
	// SpecialistScript is the single balanced reviewer used instead of the
	// 4-specialist squad when the ReviewContract classifies the PR as a
	// one-time script (spike/prototype/tooling). Not part of AllSpecialists —
	// it is only dispatched via the contract path in ReviewStage.Execute.
	SpecialistScript Specialist = "script_review"
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

Only report real bugs with concrete failure scenarios. Your ANALYSIS is adversarial; your findings follow the Review Laws.`

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

	case SpecialistScript:
		return `

## Role: Script Reviewer

This PR is classified as a one-time script (spike, prototype, migration helper, tooling). It will likely run a handful of times, by its author, then be deleted. Review it accordingly.

Focus exclusively on:
- Correctness: does the script do what its name/comments claim? Wrong flags, inverted conditions, off-by-one in batch loops
- Data safety: anything that mutates or deletes data (DB writes, DELETE/UPDATE without WHERE, file removal, S3/bucket operations, force-push). Flag missing dry-run modes, missing backups, and irreversible operations without confirmation
- Blast radius: credentials or production endpoints hardcoded; operations that could touch prod when the author intends staging
- Idempotency: what happens if the script is run twice, or dies halfway through?

Explicitly IGNORE:
- Style, naming, structure, DRY violations
- Missing tests, missing docs
- Performance (unless it would make the script effectively never finish)
- Architectural concerns — this code is not a long-lived module

Only report findings that would corrupt data, target the wrong environment, or silently do the wrong thing.`

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
	case SpecialistScript:
		return "script correctness data safety destructive operations idempotency"
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

// specialistBriefing returns the deep-review specialist memory block for a
// file, or "" when the indexer is nil. The memory module owns retrieval, typed
// dispatch, per-section truncation, and the 2400-char cap; this wrapper only
// picks the specialist's semantic query and the specialist render profile.
func specialistBriefing(ctx context.Context, indexer memory.Indexer, owner, repo string, s Specialist, filePath string, thresholds memory.Thresholds) string {
	if indexer == nil {
		return ""
	}
	return indexer.Briefing(ctx, owner, repo, filePath, specialistSearchQuery(s), memory.BriefingOptions{
		Profile:                 memory.ProfileSpecialist,
		Thresholds:              thresholds,
		CharCap:                 2400,
		EmphasizeFalsePositives: true,
	})
}

// reviewBriefing returns the single-pass reviewer memory block for a file, or
// "" when the indexer is nil or repo is empty. The review profile additionally
// folds in org rules + past-review context (side-searches specialists skip);
// the module caps the whole block at 3200 chars.
func reviewBriefing(ctx context.Context, indexer memory.Indexer, owner, repo, filePath string, thresholds memory.Thresholds) string {
	if indexer == nil || repo == "" {
		return ""
	}
	return indexer.Briefing(ctx, owner, repo, filePath, "code review patterns conventions "+filePath, memory.BriefingOptions{
		Profile:    memory.ProfileReview,
		Thresholds: thresholds,
		CharCap:    3200,
	})
}