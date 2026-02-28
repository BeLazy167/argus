package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/acmeorg/argus/internal/llm"
	"github.com/acmeorg/argus/internal/memory"
	"github.com/acmeorg/argus/internal/store"
	"github.com/acmeorg/argus/pkg/diff"
)

// ReviewStage handles the per-file parallel review using LLM.
type ReviewStage struct {
	registry    *llm.Registry
	store       *store.Store
	memClient   *memory.Client
	maxWorkers  int
	maxToolIter int // max tool-use iterations per file
}

func NewReviewStage(registry *llm.Registry, st *store.Store, memClient *memory.Client, maxWorkers int) *ReviewStage {
	return &ReviewStage{
		registry:    registry,
		store:       st,
		memClient:   memClient,
		maxWorkers:  maxWorkers,
		maxToolIter: 5,
	}
}

func (rs *ReviewStage) Execute(ctx context.Context, run *PipelineRun) error {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return nil
	}

	// Build triage lookup
	triageLookup := make(map[string]TriageAction)
	for _, t := range run.TriageResults {
		triageLookup[t.File] = t.Action
	}

	// Filter files by triage result
	var filesToReview []diff.FileDiff
	for _, f := range run.Diff.Files {
		action := triageLookup[f.NewName]
		if action == TriageSkip {
			continue
		}
		if action == TriageSkim {
			f = truncateDiff(f, 100) // skim: limit to 100 lines
		}
		filesToReview = append(filesToReview, f)
	}

	if len(filesToReview) == 0 {
		return nil
	}

	// Cancellable context so workers exit fast on first error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		review FileReview
		tokens StageTokens
		err    error
	}

	fileCh := make(chan diff.FileDiff, len(filesToReview))
	resultCh := make(chan result, len(filesToReview))

	var wg sync.WaitGroup
	workers := rs.maxWorkers
	if workers > len(filesToReview) {
		workers = len(filesToReview)
	}
	if workers > 2 {
		workers = 2
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range fileCh {
				review, tokens, err := rs.reviewFile(ctx, run, file)
				resultCh <- result{review: review, tokens: tokens, err: err}
				select {
				case <-time.After(2 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, f := range filesToReview {
		fileCh <- f
	}
	close(fileCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var firstErr error
	for r := range resultCh {
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("reviewing file %s: %w", r.review.Path, r.err)
			cancel()
		}
		if firstErr == nil {
			run.Tokens.Review = append(run.Tokens.Review, r.tokens)
			run.Tokens.addToTotal(r.tokens)
			if len(r.review.Comments) > 0 {
				run.FileReviews = append(run.FileReviews, r.review)
			}
		}
	}
	return firstErr
}

// truncateDiff limits a file diff to maxLines of raw diff content.
func truncateDiff(f diff.FileDiff, maxLines int) diff.FileDiff {
	lines := strings.Split(f.RawDiff, "\n")
	if len(lines) > maxLines {
		f.RawDiff = strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (truncated, %d more lines)", len(lines)-maxLines)
	}
	return f
}

func (rs *ReviewStage) reviewFile(ctx context.Context, run *PipelineRun, file diff.FileDiff) (FileReview, StageTokens, error) {
	review := FileReview{Path: file.NewName}
	var tokens StageTokens
	tokens.File = file.NewName

	var repoConfigs []llm.ModelConfig
	if dbConfigs, err := rs.store.ListModelConfigs(ctx, run.PREvent.RepoID); err == nil {
		repoConfigs = storeToLLMConfigs(dbConfigs)
	}
	cfg := rs.registry.GetConfig(run.PREvent.RepoID, llm.StageReview, repoConfigs)
	provider, err := rs.registry.GetProviderForRepo(ctx, run.PREvent.InstallationID, &run.PREvent.RepoID, cfg.Provider)
	if err != nil {
		return review, tokens, err
	}

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return review, tokens, err
	}
	prompt := buildFileReviewPrompt(run, file)
	messages := []llm.Message{{Role: "user", Content: prompt}}

	var tools []llm.Tool
	var toolHandler *ToolHandler
	systemPrompt := baseSystemPrompt
	if rs.memClient != nil {
		tools = memoryTools()
		toolHandler = NewToolHandler(rs.memClient, rs.store, owner)
		systemPrompt = buildAgenticSystemPrompt(owner, repo)
	}

	// Tool-use loop
	for i := 0; i < rs.maxToolIter; i++ {
		resp, err := provider.Complete(ctx, llm.CompletionRequest{
			Model:       cfg.Model,
			System:      systemPrompt,
			Messages:    messages,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
			Tools:       tools,
		})
		if err != nil {
			return review, tokens, fmt.Errorf("LLM completion: %w", err)
		}

		// Accumulate tokens from this iteration
		tokens.PromptTokens += resp.TokensUsed.PromptTokens
		tokens.CompletionTokens += resp.TokensUsed.CompletionTokens
		tokens.TotalTokens += resp.TokensUsed.TotalTokens
		tokens.Cost += resp.Cost

		// If no tool calls, we have the final response
		if len(resp.ToolCalls) == 0 {
			comments, err := parseReviewResponse(resp.Content)
			if err != nil {
				return review, tokens, fmt.Errorf("parsing response: %w", err)
			}
			review.Comments = validateComments(comments)
			return review, tokens, nil
		}

		// Guard: if LLM returns tool calls but no handler is available
		if toolHandler == nil {
			return review, tokens, fmt.Errorf("LLM requested tools but memory is not configured")
		}

		// Process tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			result, err := toolHandler.Handle(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("Error: %s", err)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return review, tokens, fmt.Errorf("exceeded max tool iterations (%d) for %s", rs.maxToolIter, file.NewName)
}

func buildFileReviewPrompt(run *PipelineRun, file diff.FileDiff) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`Review the following code changes in file "%s" from PR #%d: "%s" by %s.`,
		file.NewName, run.PREvent.PRNumber, run.PREvent.PRTitle, run.PREvent.PRAuthor))
	sb.WriteString(fmt.Sprintf(`

Diff:
%s

Respond with a JSON array of comments. Each comment must have:
- "line": int, line number in the new file (required, must be > 0)
- "start_line": int, start of multi-line range (0 if single-line)
- "body": string, the review comment in markdown
- "severity": one of "critical", "warning", "suggestion", "praise"
- "category": one of "security", "performance", "style", "bug", "readability", "error_handling", "type_design", "testing"
- "code_snippet": string, the exact 3-10 lines of source code this comment references, preserving original indentation. Include enough context for a reader to understand the issue without seeing the full file.

Example:
[{"line": 42, "start_line": 0, "body": "Potential nil dereference", "severity": "critical", "category": "bug", "code_snippet": "    val := getResult()\n    fmt.Println(val.Name)"}]

Only comment on meaningful issues with high confidence. Return [] if the changes look good.
JSON array only, no other text.`, file.RawDiff))
	return sb.String()
}

func parseReviewResponse(content string) ([]FileComment, error) {
	return unmarshalLLMArray[FileComment](content)
}

// validateComments filters out comments with invalid fields from LLM output.
func validateComments(comments []FileComment) []FileComment {
	valid := make([]FileComment, 0, len(comments))
	for _, c := range comments {
		if c.Line <= 0 || c.Body == "" {
			continue
		}
		if !ValidSeverities[c.Severity] {
			c.Severity = SeveritySuggestion
		}
		if !ValidCategories[c.Category] {
			c.Category = CategoryReadability
		}
		valid = append(valid, c)
	}
	return valid
}

const baseSystemPrompt = `You are an expert code reviewer. You review pull request diffs and provide actionable, specific feedback.

## Review Focus Areas

### Bugs & Logic Errors
- Off-by-one errors, nil/null dereferences, missing nil checks
- Race conditions, deadlocks, incorrect concurrency patterns
- Incorrect boolean logic, missing edge cases, unreachable code
- Broken invariants or violated assumptions

### Security
- Injection vulnerabilities (SQL, XSS, command injection)
- Hardcoded secrets, leaked credentials, insecure defaults
- Missing input validation at system boundaries
- Unsafe deserialization, path traversal, SSRF

### Error Handling & Silent Failures
- Empty catch blocks or swallowed errors that hide real failures
- Overly broad error handling (catch-all) that masks specific issues
- Fallback behavior that silently degrades without logging or alerting
- Missing error propagation — callers unaware an operation failed
- Retry logic without backoff or circuit breaking

### Performance
- N+1 queries, unbounded loops, missing pagination
- Unnecessary allocations, inefficient data structures
- Missing caching opportunities, redundant computation
- Goroutine/thread leaks, unclosed resources

### Type Design
- Structs/types that expose internal state without encapsulation
- Types that can represent invalid states (prefer making illegal states unrepresentable)
- Missing or incorrect type constraints/validations at construction time

### Readability & Maintainability
- Misleading variable/function names, unclear abstractions
- Functions doing too many things (violating single responsibility)
- Dead code, unused parameters, redundant conditions

### Testing Gaps (if test files are in the diff)
- Missing edge case coverage, untested error paths
- Tests that pass but don't actually verify behavior (weak assertions)
- Flaky test patterns (time-dependent, order-dependent)

## Review Principles
- Only comment on the CHANGED lines in the diff — do not review unchanged code
- Be specific: reference exact line numbers and quote the problematic code
- Explain WHY something is a problem and suggest a concrete fix
- Use confidence-based filtering: only report issues you are confident about
- Don't nitpick trivial style issues unless they genuinely harm readability
- Praise genuinely good patterns briefly — developers benefit from positive reinforcement
- Fewer, high-quality comments beat many low-value ones
- If the changes look good, return an empty array

## Output Format
Respond ONLY with a JSON array of comments. No other text.`

func buildAgenticSystemPrompt(owner, repo string) string {
	return baseSystemPrompt + fmt.Sprintf(`

## Memory Access

You have access to Argus memory via tools. Use them to find relevant context before reviewing.

**Container tag convention:**
- %s — owner-wide learned patterns
- %s — owner-wide review rules
- %s — repo-specific patterns
- %s — repo-specific rules
- %s — past review comments for this repo

**Guidelines:**
- Search for relevant patterns/rules BEFORE writing review comments
- For changes that might affect other repos, use list_repos to discover related repos, then search their memory
- Prefer repo-specific memory over owner-wide when both exist (most specific wins)
`,
		memory.OwnerTag(owner, "patterns"),
		memory.OwnerTag(owner, "rules"),
		memory.RepoTag(owner, repo, "patterns"),
		memory.RepoTag(owner, repo, "rules"),
		memory.RepoTag(owner, repo, "reviews"),
	)
}
