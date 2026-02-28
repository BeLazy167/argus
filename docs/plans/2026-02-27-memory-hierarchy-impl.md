# Memory Hierarchy Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace hardcoded ContextStage with agentic RAG — the review LLM gets tool-use access to Supermemory and decides what context to fetch.

**Architecture:** Extend LLM package with OpenAI tool-use support. Replace ContextStage with memory tools in ReviewStage. Add post-review promotion step. New container tag hierarchy: `{owner}-{kind}` and `{owner}-{repo}-{kind}`.

**Tech Stack:** Go, OpenAI-compatible tool-use API, Supermemory REST API, pgx/pgxpool

---

### Task 1: Container Tag Refactor

**Files:**
- Modify: `internal/memory/supermemory.go`
- Modify: `internal/memory/indexer.go`

**Step 1: Replace ContainerTag with OwnerTag/RepoTag**

In `internal/memory/supermemory.go`, replace the existing `ContainerTag` function:

```go
// OwnerTag returns a container tag scoped to an owner (user or org).
func OwnerTag(owner, kind string) string {
	return sanitizeTag(fmt.Sprintf("%s-%s", owner, kind))
}

// RepoTag returns a container tag scoped to a specific repo under an owner.
func RepoTag(owner, repo, kind string) string {
	return sanitizeTag(fmt.Sprintf("%s-%s-%s", owner, repo, kind))
}

// sanitizeTag replaces chars invalid in Supermemory container tags.
func sanitizeTag(tag string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(tag)
}
```

Delete the old `ContainerTag` function.

**Step 2: Update indexer.go callers**

`IndexReviewComment`: change `ContainerTag("repo", repoFullName+":reviews")` → split repoFullName into owner/repo, use `RepoTag(owner, repo, "reviews")`

`IndexRule`: change `ContainerTag("org", "rules")` → needs owner param. Add `owner string` to `IndexRule` signature, use `OwnerTag(owner, "rules")`

`SearchPastReviews`: change `ContainerTag("repo", repoFullName+":reviews")` → split and use `RepoTag(owner, repo, "reviews")`

`SearchRules`: change `ContainerTag("org", "rules")` → add `owner string` param, use `OwnerTag(owner, "rules")`

Add helper to indexer.go:
```go
func splitOwnerRepo(fullName string) (string, string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return fullName, ""
	}
	return parts[0], parts[1]
}
```

**Step 3: Verify compilation**

Run: `go build ./...`

**Step 4: Commit**

```
feat: container tag hierarchy — {owner}-{kind}, {owner}-{repo}-{kind}
```

---

### Task 2: Tool-Use Support in LLM Package

**Files:**
- Modify: `internal/llm/provider.go`
- Modify: `internal/llm/chat.go`

**Step 1: Add tool types to provider.go**

```go
// Tool describes a function the LLM can call.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes the function signature.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is an LLM request to invoke a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
```

Add to `CompletionRequest`:
```go
Tools []Tool `json:"tools,omitempty"`
```

Add to `Message`:
```go
ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
ToolCallID string     `json:"tool_call_id,omitempty"`
```

Add to `CompletionResponse`:
```go
ToolCalls []ToolCall
```

**Step 2: Update chat.go request/response structs**

Update `chatMessage`:
```go
type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
```

Update `chatRequest`:
```go
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Tools       []Tool        `json:"tools,omitempty"`
}
```

Update `chatResponse` choices to include tool_calls:
```go
Choices []struct {
	Message struct {
		Role      string     `json:"role"`
		Content   string     `json:"content"`
		ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
} `json:"choices"`
```

Update `Complete` method:
- Pass `req.Tools` into `chatRequest.Tools`
- Map `req.Messages` tool fields into `chatMessage` (ToolCalls, ToolCallID)
- Return `ToolCalls` from response in `CompletionResponse`
- Set `FinishReason` on `CompletionResponse` (add field: `FinishReason string`)

**Step 3: Verify compilation**

Run: `go build ./...`

**Step 4: Commit**

```
feat: OpenAI-compatible tool-use in LLM package
```

---

### Task 3: Memory Tools for Review

**Files:**
- Create: `internal/pipeline/tools.go`
- Modify: `internal/memory/indexer.go` (add new search methods)
- Modify: `internal/store/queries.go` (add ListReposByOwner)
- Modify: `internal/store/models.go` (no change needed, Repo already has fields)

**Step 1: Add ListReposByOwner query**

In `internal/store/queries.go`:
```go
func (s *Store) ListReposByOwner(ctx context.Context, ownerPrefix string) ([]Repo, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE full_name LIKE $1 ORDER BY full_name
	`, ownerPrefix+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Repo])
	if result == nil {
		result = []Repo{}
	}
	return result, err
}
```

**Step 2: Create tools.go with tool definitions and handlers**

In `internal/pipeline/tools.go`:

```go
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qbriad/argus/internal/llm"
	"github.com/qbriad/argus/internal/memory"
	"github.com/qbriad/argus/internal/store"
)

// memoryTools returns the tool definitions for agentic RAG.
func memoryTools() []llm.Tool {
	return []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "search_memory",
				Description: "Search Argus memory (past reviews, patterns, rules) by semantic query within a specific container tag scope.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":         map[string]any{"type": "string", "description": "Semantic search query"},
						"container_tag": map[string]any{"type": "string", "description": "Container tag to scope the search, e.g. '{owner}-patterns', '{owner}-{repo}-reviews'"},
					},
					"required": []string{"query", "container_tag"},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "list_repos",
				Description: "List all repos under an owner to understand cross-repo relationships.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"owner": map[string]any{"type": "string", "description": "Owner (user or org) name"},
					},
					"required": []string{"owner"},
				},
			},
		},
	}
}

// ToolHandler executes tool calls from the review LLM.
type ToolHandler struct {
	memClient *memory.Client
	store     *store.Store
}

func NewToolHandler(memClient *memory.Client, st *store.Store) *ToolHandler {
	return &ToolHandler{memClient: memClient, store: st}
}

// Handle dispatches a tool call and returns the result as a string.
func (th *ToolHandler) Handle(ctx context.Context, call llm.ToolCall) (string, error) {
	switch call.Function.Name {
	case "search_memory":
		return th.searchMemory(ctx, call.Function.Arguments)
	case "list_repos":
		return th.listRepos(ctx, call.Function.Arguments)
	default:
		return "", fmt.Errorf("unknown tool: %s", call.Function.Name)
	}
}

func (th *ToolHandler) searchMemory(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Query        string `json:"query"`
		ContainerTag string `json:"container_tag"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parsing args: %w", err)
	}

	resp, err := th.memClient.Search(ctx, memory.SearchRequest{
		Query:        args.Query,
		ContainerTag: args.ContainerTag,
		SearchMode:   "hybrid",
		Limit:        5,
		Threshold:    0.5,
	})
	if err != nil {
		return fmt.Sprintf("search failed: %s", err), nil
	}

	if len(resp.Results) == 0 {
		return "No results found.", nil
	}

	var sb strings.Builder
	for i, r := range resp.Results {
		content := r.Memory
		if content == "" {
			content = r.Chunk
		}
		sb.WriteString(fmt.Sprintf("--- Result %d (score: %.2f) ---\n%s\n\n", i+1, r.Similarity, content))
	}
	return sb.String(), nil
}

func (th *ToolHandler) listRepos(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Owner string `json:"owner"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parsing args: %w", err)
	}

	repos, err := th.store.ListReposByOwner(ctx, args.Owner)
	if err != nil {
		return fmt.Sprintf("query failed: %s", err), nil
	}

	if len(repos) == 0 {
		return "No repos found for this owner.", nil
	}

	var sb strings.Builder
	for _, r := range repos {
		sb.WriteString(fmt.Sprintf("- %s (branch: %s, enabled: %v)\n", r.FullName, r.DefaultBranch, r.Enabled))
	}
	return sb.String(), nil
}
```

**Step 3: Verify compilation**

Run: `go build ./...`

**Step 4: Commit**

```
feat: memory tools — search_memory + list_repos for agentic review
```

---

### Task 4: Agentic Review Stage

**Files:**
- Modify: `internal/pipeline/review.go`
- Modify: `internal/pipeline/types.go`
- Modify: `internal/pipeline/states.go`
- Modify: `internal/pipeline/orchestrator.go`

**Step 1: Update ReviewStage to accept memory dependencies**

Add `memClient` and update constructor:
```go
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
```

**Step 2: Rewrite reviewFile with tool-use loop**

Replace the current `reviewFile` method:

```go
func (rs *ReviewStage) reviewFile(ctx context.Context, run *PipelineRun, file diff.FileDiff) (FileReview, error) {
	review := FileReview{Path: file.NewName}

	var repoConfigs []llm.ModelConfig
	if dbConfigs, err := rs.store.ListModelConfigs(ctx, run.PREvent.RepoID); err == nil {
		repoConfigs = storeToLLMConfigs(dbConfigs)
	}
	cfg := rs.registry.GetConfig(run.PREvent.RepoID, llm.StageReview, repoConfigs)
	provider, err := rs.registry.GetProvider(cfg.Provider)
	if err != nil {
		return review, err
	}

	owner, repo, _ := splitRepoFullName(run.PREvent.RepoFullName)
	systemPrompt := buildAgenticSystemPrompt(owner, repo)
	prompt := buildFileReviewPrompt(run, file)

	messages := []llm.Message{{Role: "user", Content: prompt}}

	var tools []llm.Tool
	var toolHandler *ToolHandler
	if rs.memClient != nil {
		tools = memoryTools()
		toolHandler = NewToolHandler(rs.memClient, rs.store)
	}

	// Tool-use loop
	for i := 0; i <= rs.maxToolIter; i++ {
		resp, err := provider.Complete(ctx, llm.CompletionRequest{
			Model:       cfg.Model,
			System:      systemPrompt,
			Messages:    messages,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
			Tools:       tools,
		})
		if err != nil {
			return review, fmt.Errorf("LLM completion: %w", err)
		}

		// If no tool calls, we have the final response
		if len(resp.ToolCalls) == 0 {
			comments, err := parseReviewResponse(resp.Content)
			if err != nil {
				return review, fmt.Errorf("parsing response: %w", err)
			}
			review.Comments = validateComments(comments)
			return review, nil
		}

		// Process tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
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

	return review, fmt.Errorf("exceeded max tool iterations (%d) for %s", rs.maxToolIter, file.NewName)
}
```

**Step 3: Update system prompt for agentic review**

Add `buildAgenticSystemPrompt` to review.go:

```go
func buildAgenticSystemPrompt(owner, repo string) string {
	return baseSystemPrompt + fmt.Sprintf(`

## Memory Access

You have access to Argus memory via tools. Use them to find relevant context before reviewing.

**Container tag convention:**
- %s-patterns — owner-wide learned patterns
- %s-rules — owner-wide review rules
- %s-%s-patterns — repo-specific patterns
- %s-%s-rules — repo-specific rules
- %s-%s-reviews — past review comments for this repo

**Guidelines:**
- Search for relevant patterns/rules BEFORE writing review comments
- For changes that might affect other repos, use list_repos to discover related repos, then search their memory
- Prefer repo-specific memory over owner-wide when both exist (most specific wins)
- If no memory tools are available, proceed with the review without context
`, owner, owner, owner, repo, owner, repo, owner, repo)
}
```

**Step 4: Remove ContextStage from pipeline**

In `states.go`, remove `StateRetrievingContext` and update transitions:
```go
const (
	StatePending      PipelineState = "pending"
	StateTriaging     PipelineState = "triaging"
	StateReviewing    PipelineState = "reviewing"
	StateSynthesizing PipelineState = "synthesizing"
	StatePosting      PipelineState = "posting"
	StateCompleted    PipelineState = "completed"
	StateFailed       PipelineState = "failed"
)

func transitions() map[PipelineState]PipelineState {
	return map[PipelineState]PipelineState{
		StatePending:      StateTriaging,
		StateTriaging:     StateReviewing,
		StateReviewing:    StateSynthesizing,
		StateSynthesizing: StatePosting,
		StatePosting:      StateCompleted,
	}
}
```

In `orchestrator.go`:
- Remove `contextStage` field and constructor param
- Remove `sm.RegisterStage(StateRetrievingContext, contextStage.Execute)`
- Update `NewReviewStage` call to pass `memClient`
- Remove `Context` references from `buildFileReviewPrompt` (past reviews injection)

In `types.go`:
- Remove `ReviewContext` struct
- Remove `Context map[string]ReviewContext` from `PipelineRun`

Delete `internal/pipeline/context.go` entirely.

In `app.go`:
- Remove `contextStage` variable and creation
- Update `NewReviewStage` call: `pipeline.NewReviewStage(registry, db, memClient, cfg.MaxConcurrentReviews)` where `memClient` is the `*memory.Client` (nil if Supermemory not configured)
- Update `NewOrchestrator` call to remove `contextStage` param

**Step 5: Update buildFileReviewPrompt**

Remove the past reviews injection block (lines 160-167 in current review.go). The LLM now fetches its own context via tools.

```go
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

Only comment on meaningful issues with high confidence. Return [] if the changes look good.
JSON array only, no other text.`, file.RawDiff))
	return sb.String()
}
```

**Step 6: Verify compilation**

Run: `go build ./...`

**Step 7: Commit**

```
feat: agentic review — LLM decides what memory to fetch via tool-use
```

---

### Task 5: Post-Review Promotion

**Files:**
- Modify: `internal/pipeline/orchestrator.go`
- Modify: `internal/memory/indexer.go`

**Step 1: Add IndexOwnerPattern to indexer**

```go
// IndexOwnerPattern stores a promoted pattern at owner scope.
func (idx *Indexer) IndexOwnerPattern(ctx context.Context, owner, content string, metadata map[string]string) error {
	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{OwnerTag(owner, "patterns")},
		Metadata:      metadata,
	})
	if err != nil {
		return fmt.Errorf("indexing owner pattern: %w", err)
	}
	idx.logger.Debug("indexed owner pattern", "owner", owner)
	return nil
}

// IndexRepoTopology stores inferred repo role/dependencies at owner scope.
func (idx *Indexer) IndexRepoTopology(ctx context.Context, owner, content string) error {
	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: []string{OwnerTag(owner, "patterns")},
		Metadata:      map[string]string{"type": "topology"},
	})
	return err
}
```

**Step 2: Update indexer.go IndexReviewComment to use new tags**

Already done in Task 1, but also add repo-level pattern indexing for criticals:

```go
func (idx *Indexer) IndexReviewComment(ctx context.Context, owner, repo string, comment ReviewMemory) error {
	content := fmt.Sprintf("File: %s\nSeverity: %s\nCategory: %s\n\n%s\n\nContext:\n%s",
		comment.FilePath, comment.Severity, comment.Category, comment.Body, comment.DiffContext)

	tags := []string{RepoTag(owner, repo, "reviews")}

	_, err := idx.client.AddMemory(ctx, AddRequest{
		Content:       content,
		ContainerTags: tags,
		Metadata: map[string]string{
			"file_path": comment.FilePath,
			"severity":  comment.Severity,
			"category":  comment.Category,
			"pr_number": fmt.Sprintf("%d", comment.PRNumber),
			"review_id": comment.ReviewID,
		},
	})
	if err != nil {
		return fmt.Errorf("indexing review comment: %w", err)
	}
	idx.logger.Debug("indexed review comment", "owner", owner, "repo", repo, "file", comment.FilePath)
	return nil
}
```

**Step 3: Add promotion step in orchestrator.indexComments**

After indexing all comments, if any are critical, promote to owner scope:

```go
func (o *Orchestrator) indexComments(ctx context.Context, run *PipelineRun) {
	owner, repo, _ := splitRepoFullName(run.PREvent.RepoFullName)
	side := "RIGHT"

	var hasCritical bool
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			sev := string(c.Severity)
			cat := string(c.Category)
			line := c.Line
			var startLine *int
			if c.StartLine > 0 {
				startLine = &c.StartLine
			}

			if err := o.st.CreateReviewComment(ctx, run.ReviewID, fr.Path, startLine, &line, &side, c.Body, &sev, &cat); err != nil {
				o.logger.Error("persisting review comment", "error", err, "file", fr.Path)
			}

			if o.indexer != nil {
				err := o.indexer.IndexReviewComment(ctx, owner, repo, memory.ReviewMemory{
					ReviewID:    run.ReviewID.String(),
					PRNumber:    run.PREvent.PRNumber,
					FilePath:    fr.Path,
					Body:        c.Body,
					Severity:    sev,
					Category:    cat,
					DiffContext: getDiffContext(run, fr.Path),
				})
				if err != nil {
					o.logger.Error("indexing review comment", "error", err, "file", fr.Path)
				}

				if c.Severity == SeverityCritical {
					hasCritical = true
				}
			}
		}
	}

	// Auto-promote criticals to owner scope
	if hasCritical && o.indexer != nil {
		o.promoteFindings(ctx, run, owner)
	}
}

func (o *Orchestrator) promoteFindings(ctx context.Context, run *PipelineRun, owner string) {
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Severity != SeverityCritical {
				continue
			}
			// Generalize: strip specific file paths for owner-level pattern
			pattern := fmt.Sprintf("[%s] %s — %s", c.Category, c.Body, "Promoted from "+run.PREvent.RepoFullName)
			if err := o.indexer.IndexOwnerPattern(ctx, owner, pattern, map[string]string{
				"source_repo": run.PREvent.RepoFullName,
				"reason":      "critical",
				"category":    string(c.Category),
			}); err != nil {
				o.logger.Error("promoting finding", "error", err)
			}
		}
	}
}
```

**Step 4: Verify compilation**

Run: `go build ./...`

**Step 5: Commit**

```
feat: post-review promotion — criticals auto-promote to owner memory
```

---

### Task 6: Wire Up in app.go

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/pipeline/orchestrator.go` (constructor signature)

**Step 1: Update Orchestrator constructor**

Remove `contextStage` param, add `memClient`:
```go
func NewOrchestrator(db *pgxpool.Pool, st *store.Store, ghClient *ghpkg.Client, reviewStage *ReviewStage, triageStage *TriageStage, indexer *memory.Indexer, logger *slog.Logger) *Orchestrator {
```

**Step 2: Update app.go wiring**

```go
// Pipeline
triageStage := pipeline.NewTriageStage(registry, db)
reviewStage := pipeline.NewReviewStage(registry, db, memClient, cfg.MaxConcurrentReviews)
orchestrator := pipeline.NewOrchestrator(db.Pool, db, ghClient, reviewStage, triageStage, indexer, logger)
```

Where `memClient` is:
```go
var memClient *memory.Client
var indexer *memory.Indexer
if cfg.SupermemoryAPIKey != "" {
	memClient = memory.NewClient(cfg.SupermemoryAPIKey)
	indexer = memory.NewIndexer(memClient, logger)
}
```

**Step 3: Verify compilation + full build**

Run: `go build ./...`

**Step 4: Commit**

```
feat: wire memory hierarchy into app — complete agentic review pipeline
```

---

## Execution Order

Tasks 1, 2, 3 are **independent** — can run in parallel.
Task 4 depends on Tasks 1, 2, 3.
Task 5 depends on Task 1.
Task 6 depends on Tasks 4, 5.

```
[Task 1: Tags] ──────┐
[Task 2: Tool-Use] ───┼──→ [Task 4: Agentic Review] ──→ [Task 6: Wiring]
[Task 3: Tools] ──────┘                                      ↑
[Task 5: Promotion] ─────────────────────────────────────────┘
```

## Unresolved Questions

- Max tool iterations per file? (currently set to 5)
- Should we index `.argus/rules.md` to Supermemory on each run or keep the rules engine?
- Token budget: tool-use adds overhead — need cost monitoring
- Topology inference: do it in promotion step or separate async job?
