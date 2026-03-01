package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/memory"
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

// specialistPrompt returns the full system prompt for a specialist agent.
// Specialists do NOT get persona overlay — they have fixed roles.
func specialistPrompt(s Specialist) string {
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
- Race conditions and concurrency bugs
- Edge cases the author didn't consider
- Silent data corruption and wrong return values
- Type coercion traps and implicit conversions

Ignore style, naming, documentation. Only report real bugs with concrete failure scenarios.

**Output tone:** Your analysis should be adversarial, but the comments you write to the developer must be professional and constructive. Explain the bug clearly — attack the code, not the author.`

	case SpecialistSecurity:
		return `

## Role: Security Auditor

Assume every external input is attacker-controlled. Assume every network call will fail or be intercepted.

Focus exclusively on:
- Injection: SQL, XSS, command, LDAP, template
- Hardcoded secrets, API keys, credentials in code
- Authentication and authorization flaws
- Input validation gaps at every trust boundary
- Unsafe deserialization, path traversal, SSRF
- Cryptographic misuse (weak algos, hardcoded IVs, predictable randomness)
- Missing rate limiting on sensitive endpoints
- Information leakage in error messages

Lower your threshold — flag anything suspicious even at "warning" level.
Ignore non-security issues entirely.`

	case SpecialistArchitecture:
		return `

## Role: Architecture Reviewer

Review from a systems design and reliability perspective.

Focus exclusively on:
- Error handling design: swallowed errors, empty catch blocks, missing error propagation
- Type safety: types that can represent invalid states, missing constraints
- Resource management: leaks, unclosed handles, missing cleanup
- Coupling and dependency direction issues
- API contract problems: backwards compatibility, missing validation
- Silent failures: async operations that fail without logging
- Missing timeouts on network calls or database queries
- Concurrency: missing locks, deadlock-prone patterns

Ignore style, naming, minor formatting. Only report architectural and reliability issues.`

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

For each issue, explain WHAT previously worked and HOW this change breaks it.
Ignore new code that doesn't modify existing behavior.`

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

// truncateSnippet caps a string at maxLen bytes (rune-safe) and appends "..." if truncated.
func truncateSnippet(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Walk backward to avoid splitting a multi-byte UTF-8 rune
	for maxLen > 0 && s[maxLen]&0xC0 == 0x80 {
		maxLen--
	}
	return s[:maxLen] + "..."
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
			results = append(results, truncateSnippet(c, 300))
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
