package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// agenticMemoryTags returns the container tags a deep review of repo may
// search, most-specific first: the repo's unified container and the shared
// cross-repo container. buildAgenticSystemPrompt advertises exactly this list,
// memoryTools embeds it in the tool schema, and ToolHandler.searchMemory
// validates against it — one source of truth so the prompt and the access
// check cannot drift. Routed through memory.RepoTagNew so repo-name
// sanitization/collision handling lives in the memory package only.
func agenticMemoryTags(repo string) []string {
	return []string{memory.RepoTagNew(repo), memory.SharedTag}
}

// memoryTools returns the tool definitions for agentic RAG. repo scopes the
// container_tag description to the concrete tags this review may search.
func memoryTools(repo string) []llm.Tool {
	tags := agenticMemoryTags(repo)
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
						"container_tag": map[string]any{"type": "string", "description": fmt.Sprintf("Container tag to scope the search: %q (this repo) or %q (cross-repo patterns and org rules)", tags[0], tags[1])},
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

// ToolHandler executes tool calls from the review LLM, scoped to a specific
// owner/repo. repo scopes new-shape container access to this review's repo so a
// prompt-injected PR cannot steer search_memory into another repo's container.
type ToolHandler struct {
	memClient *memory.Client
	store     *store.Store
	owner     string
	repo      string
}

func NewToolHandler(memClient *memory.Client, st *store.Store, owner, repo string) *ToolHandler {
	return &ToolHandler{memClient: memClient, store: st, owner: owner, repo: repo}
}

// tagAllowed reports whether the review may search container tag. It accepts the
// new-shape tags for THIS repo (agenticMemoryTags: RepoTagNew(repo) + SharedTag)
// plus, during the dual-read window, legacy owner-prefixed tags that still hold
// pre-migration data. The new-shape check is an exact match on this repo's
// container, so a review for repo X can never reach repo Y's container.
func (th *ToolHandler) tagAllowed(tag string) bool {
	for _, allowed := range agenticMemoryTags(th.repo) {
		if tag == allowed {
			return true
		}
	}
	return memory.ValidateTagScope(tag, th.owner)
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

	if !th.tagAllowed(args.ContainerTag) {
		return fmt.Sprintf("Access denied: tag must be one of the containers for this review (%v)", agenticMemoryTags(th.repo)), nil
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
		sb.WriteString(fmt.Sprintf("--- Result %d (score: %.2f) ---\n%s\n\n", i+1, r.Similarity, r.Content()))
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
