package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
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

// workUnit represents a single LLM review call — either a skim single-pass or a specialist deep pass.
type workUnit struct {
	file       diff.FileDiff
	action     TriageAction
	specialist Specialist // empty for skim single-pass
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

	// Filter files and build work units
	var units []workUnit
	for _, f := range run.Diff.Files {
		action := triageLookup[f.NewName]
		if action == TriageSkip {
			continue
		}
		if action == TriageSkim {
			f = truncateDiff(f, 100)
		}
		switch {
		case action == TriageSecuritySkim:
			// Security-only specialist pass — saves tokens vs full deep
			units = append(units, workUnit{file: f, action: action, specialist: SpecialistSecurity})
		case run.DeepReview && action == TriageDeep:
			// Deep files get all specialist passes
			for _, s := range AllSpecialists() {
				units = append(units, workUnit{file: f, action: action, specialist: s})
			}
		default:
			// Skim or deep-review-disabled: single-pass
			units = append(units, workUnit{file: f, action: action})
		}
	}

	if len(units) == 0 {
		return nil
	}

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return fmt.Errorf("invalid repo name %q: %w", run.PREvent.RepoFullName, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		review FileReview
		tokens StageTokens
		err    error
	}

	// Collect unique files for prefetch
	seen := make(map[string]bool)
	var filesToPrefetch []diff.FileDiff
	for _, u := range units {
		if !seen[u.file.NewName] {
			seen[u.file.NewName] = true
			filesToPrefetch = append(filesToPrefetch, u.file)
		}
	}
	fileContents := prefetchFiles(ctx, rs.ghClient, run, owner, repo, filesToPrefetch)

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

	unitCh := make(chan workUnit, len(units))
	resultCh := make(chan result, len(units))

	workers := min(rs.maxWorkers, len(units), 3)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range unitCh {
				if ctx.Err() != nil {
					// Drain remaining units to avoid blocking sender, count what we skip
					skipped := 1 // current unit
					for range unitCh {
						skipped++
					}
					slog.Warn("review worker exiting: context cancelled", "skipped_units", skipped)
					return
				}
				p := reviewParams{file: u.file, action: u.action, specialist: u.specialist, deepReview: run.DeepReview}
				if u.specialist != "" {
					p.systemBase = specialistPrompt(u.specialist) + specialistMemoryBlock(ctx, rs.memClient, owner, repo, u.specialist, u.file.NewName)
				} else {
					p.systemBase = baseSystemPrompt
					p.promptExtra = PersonaPromptOverlay(run.Persona)
				}
				rev, tok, err := rs.reviewFile(ctx, run, p, fileContents, owner, repo, cfg, provider)
				resultCh <- result{review: rev, tokens: tok, err: err}
			}
		}()
	}

	for _, u := range units {
		unitCh <- u
	}
	close(unitCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results, merging specialist comments per file
	fileReviewMap := make(map[string]*FileReview)
	var skipped int
	for r := range resultCh {
		if r.err != nil {
			slog.Warn("skipping review unit", "file", r.review.Path, "error", r.err)
			skipped++
			continue
		}
		run.Tokens.Review = append(run.Tokens.Review, r.tokens)
		run.Tokens.addToTotal(r.tokens)
		if len(r.review.Comments) > 0 {
			if existing, ok := fileReviewMap[r.review.Path]; ok {
				existing.Comments = append(existing.Comments, r.review.Comments...)
			} else {
				fr := r.review
				fileReviewMap[fr.Path] = &fr
			}
		}
	}

	for _, fr := range fileReviewMap {
		run.FileReviews = append(run.FileReviews, *fr)
	}
	sort.Slice(run.FileReviews, func(i, j int) bool {
		return run.FileReviews[i].Path < run.FileReviews[j].Path
	})

	if skipped == len(units) {
		return fmt.Errorf("all %d review units failed", len(units))
	}
	if skipped > 0 {
		slog.Warn("review completed with skipped units", "skipped", skipped, "total", len(units))
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
	f.RawDiff = truncateLines(f.RawDiff, maxLines)
	return f
}

// reviewParams configures a single LLM review call (normal or specialist).
type reviewParams struct {
	file        diff.FileDiff
	action      TriageAction
	specialist  Specialist // empty for normal single-pass
	systemBase  string     // base system prompt before memory/tools
	promptExtra string     // appended to system prompt (persona or language guidance)
	deepReview  bool       // controls agentic memory access for deep files
}

func (rs *ReviewStage) reviewFile(ctx context.Context, run *PipelineRun, p reviewParams, fileContents map[string]string, owner, repo string, cfg llm.ModelConfig, provider llm.Provider) (FileReview, StageTokens, error) {
	review := FileReview{Path: p.file.NewName}
	var tokens StageTokens
	if p.specialist != "" {
		tokens.File = fmt.Sprintf("%s[%s]", p.file.NewName, p.specialist)
	} else {
		tokens.File = p.file.NewName
	}

	prompt := buildFileReviewPrompt(run, p.file, fileContents[p.file.NewName])
	messages := []llm.Message{{Role: "user", Content: prompt}}

	var tools []llm.Tool
	var toolHandler *ToolHandler
	systemPrompt := p.systemBase
	if rs.memClient != nil && p.action == TriageDeep && p.deepReview {
		tools = memoryTools()
		toolHandler = NewToolHandler(rs.memClient, rs.store, owner)
		// Prepend agentic base; keep specialist overlay via systemBase
		if p.specialist != "" {
			systemPrompt = buildAgenticSystemPrompt(owner, repo) + specialistOverlay(p.specialist)
		} else {
			systemPrompt = buildAgenticSystemPrompt(owner, repo)
		}
	}
	systemPrompt += p.promptExtra

	label := string(p.specialist) // empty for normal pass
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
			return review, tokens, fmt.Errorf("LLM completion %s: %w", label, err)
		}

		tokens.PromptTokens += resp.TokensUsed.PromptTokens
		tokens.CompletionTokens += resp.TokensUsed.CompletionTokens
		tokens.TotalTokens += resp.TokensUsed.TotalTokens
		tokens.Cost += resp.Cost

		if len(resp.ToolCalls) == 0 {
			comments, err := parseReviewResponse(resp.Content)
			if err != nil {
				return review, tokens, fmt.Errorf("parsing response %s: %w", label, err)
			}
			validated := validateComments(comments)
			for i := range validated {
				validated[i].Specialist = p.specialist
			}
			review.Comments = validated
			return review, tokens, nil
		}

		if toolHandler == nil {
			return review, tokens, fmt.Errorf("LLM requested tools but memory is not configured %s", label)
		}

		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			result, err := toolHandler.Handle(ctx, tc)
			if err != nil {
				slog.Warn("tool call failed", "tool", tc.Function.Name, "error", err, "file", p.file.NewName, "specialist", label)
				result = fmt.Sprintf("Error: %s", err)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return review, tokens, fmt.Errorf("exceeded max tool iterations (%d) for %s %s", rs.maxToolIter, p.file.NewName, label)
}

func buildFileReviewPrompt(run *PipelineRun, file diff.FileDiff, fileContent string) string {
	var sb strings.Builder
	// Truncate user-controlled fields for prompt injection resistance
	safeTitle := truncate(run.PREvent.PRTitle, 200)
	safeAuthor := truncate(run.PREvent.PRAuthor, 100)
	sb.WriteString(fmt.Sprintf("Review changes in \"%s\" from PR #%d: \"%s\" by %s.\n",
		file.NewName, run.PREvent.PRNumber, safeTitle, safeAuthor))

	if run.PREvent.PRBody != "" {
		sb.WriteString(fmt.Sprintf("\nPR Description: %s\n", run.PREvent.PRBody))
	}

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
- asyncio.run() inside already-async context (RuntimeError)
- datetime.now() without timezone (silent UTC/local bugs — use datetime.now(tz=...))
- SQLAlchemy lazy loading in async contexts (greenlet error)
- Missing __all__ on public modules (leaking internals)
- logging vs print in production paths
- f-string with = debug syntax leaking to production
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
7. A false positive that wastes a developer's time is worse than missing a minor issue. If you can't point to the exact line that proves the bug, don't file it

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
- If no relevant patterns or rules are found, proceed with the base review only. Do NOT infer or hallucinate patterns from the code itself
`,
		memory.OwnerTag(owner, "patterns"),
		memory.OwnerTag(owner, "rules"),
		memory.RepoTag(owner, repo, "patterns"),
		memory.RepoTag(owner, repo, "rules"),
		memory.RepoTag(owner, repo, "reviews"),
	)
}
