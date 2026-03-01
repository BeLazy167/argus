package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/pkg/diff"
	"golang.org/x/sync/errgroup"
)

// ReviewStage handles the per-file parallel review using LLM.
type ReviewStage struct {
	registry    *llm.Registry
	store       *store.Store
	ghClient    *ghpkg.Client
	memClient   *memory.Client
	maxWorkers  int
	maxToolIter int // max tool-use iterations per file
}

func NewReviewStage(registry *llm.Registry, st *store.Store, ghClient *ghpkg.Client, memClient *memory.Client, maxWorkers int) *ReviewStage {
	return &ReviewStage{
		registry:    registry,
		store:       st,
		ghClient:    ghClient,
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

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return fmt.Errorf("invalid repo name %q: %w", run.PREvent.RepoFullName, err)
	}

	// Cancellable context so workers exit fast on first error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		review FileReview
		tokens StageTokens
		err    error
	}

	fileContents := prefetchFiles(ctx, rs.ghClient, run, owner, repo, filesToReview)

	// Resolve model config once for all files
	dbConfigs, err := rs.store.ListModelConfigs(ctx, run.DBRepoID)
	if err != nil {
		return fmt.Errorf("loading model configs for repo %d: %w", run.DBRepoID, err)
	}
	repoConfigs := storeToLLMConfigs(dbConfigs)
	cfg, err := rs.registry.GetConfig(run.DBRepoID, llm.StageReview, repoConfigs)
	if err != nil {
		return err
	}
	provider, err := rs.registry.GetProviderForRepo(ctx, run.DBInstallationID, &run.DBRepoID, cfg.Provider)
	if err != nil {
		return err
	}

	fileCh := make(chan diff.FileDiff, len(filesToReview))
	resultCh := make(chan result, len(filesToReview))

	var wg sync.WaitGroup
	workers := rs.maxWorkers
	if workers > len(filesToReview) {
		workers = len(filesToReview)
	}
	if workers > 3 {
		workers = 3
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range fileCh {
				review, tokens, err := rs.reviewFile(ctx, run, file, fileContents, triageLookup[file.NewName], owner, repo, cfg, provider)
				resultCh <- result{review: review, tokens: tokens, err: err}
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

	var skipped int
	for r := range resultCh {
		if r.err != nil {
			slog.Warn("skipping file review", "file", r.review.Path, "error", r.err)
			skipped++
			continue
		}
		run.Tokens.Review = append(run.Tokens.Review, r.tokens)
		run.Tokens.addToTotal(r.tokens)
		if len(r.review.Comments) > 0 {
			run.FileReviews = append(run.FileReviews, r.review)
		}
	}
	if skipped > 0 {
		slog.Warn("review completed with skipped files", "skipped", skipped, "total", len(filesToReview))
	}
	return nil
}

func prefetchFiles(ctx context.Context, ghClient *ghpkg.Client, run *PipelineRun, owner, repo string, files []diff.FileDiff) map[string]string {
	if ghClient == nil {
		return nil
	}
	contents := make(map[string]string, len(files))
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(5) // bound GitHub API concurrency
	for _, f := range files {
		path := f.NewName
		g.Go(func() error {
			content, err := ghClient.GetFileContent(ctx, run.PREvent.InstallationID, owner, repo, path, run.PREvent.HeadSHA)
			if err != nil {
				slog.Warn("prefetch file content failed", "file", path, "error", err)
				return nil // non-fatal, review proceeds without full file content
			}
			mu.Lock()
			contents[path] = content
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // errors are non-fatal (logged above)
	return contents
}

// truncateDiff limits a file diff to maxLines of raw diff content.
func truncateDiff(f diff.FileDiff, maxLines int) diff.FileDiff {
	lines := strings.Split(f.RawDiff, "\n")
	if len(lines) > maxLines {
		f.RawDiff = strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (truncated, %d more lines)", len(lines)-maxLines)
	}
	return f
}

func (rs *ReviewStage) reviewFile(ctx context.Context, run *PipelineRun, file diff.FileDiff, fileContents map[string]string, action TriageAction, owner, repo string, cfg llm.ModelConfig, provider llm.Provider) (FileReview, StageTokens, error) {
	review := FileReview{Path: file.NewName}
	var tokens StageTokens
	tokens.File = file.NewName

	fileContent := fileContents[file.NewName]

	prompt := buildFileReviewPrompt(run, file, fileContent)
	messages := []llm.Message{{Role: "user", Content: prompt}}

	var tools []llm.Tool
	var toolHandler *ToolHandler
	systemPrompt := baseSystemPrompt
	if rs.memClient != nil && action == TriageDeep {
		tools = memoryTools()
		toolHandler = NewToolHandler(rs.memClient, rs.store, owner)
		systemPrompt = buildAgenticSystemPrompt(owner, repo)
	}

	systemPrompt += PersonaPromptOverlay(run.Persona)

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
				slog.Warn("tool call failed", "tool", tc.Function.Name, "error", err, "file", file.NewName)
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

func buildFileReviewPrompt(run *PipelineRun, file diff.FileDiff, fileContent string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Review changes in \"%s\" from PR #%d: \"%s\" by %s.\n",
		file.NewName, run.PREvent.PRNumber, run.PREvent.PRTitle, run.PREvent.PRAuthor))

	if guide := languageGuidance(file.NewName); guide != "" {
		sb.WriteString(guide)
	}

	sb.WriteString("\nDiff:\n")
	sb.WriteString(file.RawDiff)
	sb.WriteString("\n")

	if fileContent != "" {
		sb.WriteString("\nFull file content:\n```\n")
		sb.WriteString(truncateLines(fileContent, 500))
		sb.WriteString("\n```\n")
	}

	sb.WriteString(`
Respond with a JSON array of comments:
[{
  "line": 42,                // line number in new file (required, > 0)
  "start_line": 40,          // start of multi-line range (0 if single-line)
  "body": "Why this is a problem and what could go wrong",
  "severity": "critical",    // critical | warning | suggestion | praise
  "category": "bug",         // bug | security | performance | error_handling | style | readability | type_design | testing
  "suggestion": "fixed code" // exact replacement for start_line..line (omit for praise)
}]

Return [] if changes look good. JSON array only.`)
	return sb.String()
}

// languageGuidance returns language-specific review hints based on file extension.
func languageGuidance(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return `
## Language: TypeScript/JavaScript
Watch specifically for these common pitfalls:
- == vs === (type coercion bugs)
- var in loops (closure captures final value — use let/const)
- forEach with async callback (doesn't await — use for...of or Promise.all+map)
- parseInt() without radix argument
- Array.sort() mutates the original array
- Missing await on async function calls
- getYear() vs getFullYear()
- Unanchored or unescaped regex patterns
`
	case ".go":
		return `
## Language: Go
Watch specifically for these common pitfalls:
- Goroutine leaks (missing context cancellation or done channel)
- Deferred close on potentially nil values
- Range variable capture in goroutines (pre-Go 1.22)
- Error shadowing with := in nested scopes
- Slice append without pre-allocation in hot paths
- Missing mutex for shared state across goroutines
`
	case ".py":
		return `
## Language: Python
Watch specifically for these common pitfalls:
- Mutable default arguments (def f(x=[]))
- Late binding closures in loops
- Bare except: catches SystemExit/KeyboardInterrupt
- is vs == for value comparison
- Missing async/await in coroutine calls
`
	case ".rs":
		return `
## Language: Rust
Watch specifically for these common pitfalls:
- Unwrap/expect on Result/Option in library code
- Clone where a borrow would suffice
- Missing error propagation (? operator)
- Deadlock-prone lock ordering
`
	default:
		return ""
	}
}

// truncateLines returns content limited to maxLines, appending a note if truncated.
func truncateLines(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

func parseReviewResponse(content string) ([]FileComment, error) {
	return unmarshalLLMArray[FileComment](content)
}

// validateComments filters out comments with invalid fields from LLM output.
func validateComments(comments []FileComment) []FileComment {
	valid := make([]FileComment, 0, len(comments))
	for _, c := range comments {
		if c.Line <= 0 || c.Body == "" {
			slog.Debug("dropped invalid LLM comment", "line", c.Line, "body_empty", c.Body == "")
			continue
		}
		if !ValidSeverities[c.Severity] {
			c.Severity = SeveritySuggestion
		}
		if !ValidCategories[c.Category] {
			c.Category = CategoryReadability
		}
		// Clear suggestion if line range is invalid
		if c.Suggestion != "" && c.StartLine > c.Line {
			c.Suggestion = ""
		}
		valid = append(valid, c)
	}
	return valid
}

const baseSystemPrompt = `You are a senior engineer reviewing a pull request. You are thorough, skeptical, and direct.

Assume the code has bugs until proven otherwise. For every function, ask yourself: "What input would break this? What edge case did the author miss? What happens when this fails at 3 AM?"

## Principles
1. Only comment on CHANGED lines — never review unchanged code
2. For every issue, explain WHY it matters and what breaks in production
3. High confidence only — if you're unsure, don't comment
4. Fewer high-quality comments beat many low-value ones
5. Don't nitpick style. Focus on correctness, security, and reliability
6. Return [] if the changes look good

## Priority (highest first)
1. **Bugs** — logic errors, off-by-one, null dereferences, broken invariants, race conditions, incorrect boundary checks
2. **Security** — injection (SQL/XSS/command), hardcoded secrets, missing input validation, SSRF, path traversal
3. **Silent failures** — swallowed errors, empty catch blocks, missing error propagation, async operations that silently fail
4. **Performance** — N+1 queries, unbounded operations, resource leaks, missing pagination
5. **Type safety** — types that can represent invalid states, missing constraints at construction

## Output
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
