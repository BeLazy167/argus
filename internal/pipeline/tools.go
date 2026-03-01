package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
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

// ToolHandler executes tool calls from the review LLM, scoped to a specific owner.
type ToolHandler struct {
	memClient *memory.Client
	store     *store.Store
	owner     string
}

func NewToolHandler(memClient *memory.Client, st *store.Store, owner string) *ToolHandler {
	return &ToolHandler{memClient: memClient, store: st, owner: owner}
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

	if !memory.ValidateTagScope(args.ContainerTag, th.owner) {
		return fmt.Sprintf("Access denied: tag must be scoped to owner %q", th.owner), nil
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
