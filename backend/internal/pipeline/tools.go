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

// agenticMemoryType pairs a queryable metadata.type value with the prose the
// agentic system prompt shows for it. It is the single source of truth for the
// optional `type` filter: buildAgenticSystemPrompt advertises exactly these,
// memoryTools embeds the values as the tool-schema enum, and
// ToolHandler.searchMemory validates against them — so the prompt, the schema,
// and the access check cannot drift. Values mirror the memory.Type* constants
// the indexer actually writes; TypeTrace/TypeTopology are intentionally omitted
// as they carry no review-time signal.
type agenticMemoryType struct {
	Value string
	Desc  string
}

func agenticMemoryTypes() []agenticMemoryType {
	return []agenticMemoryType{
		{string(memory.TypePattern), "learned conventions / best practices"},
		{string(memory.TypeScenario), "known issues and past incidents"},
		{string(memory.TypeFeedback), "developer confirmations (polarity=positive) / dismissals (polarity=negative) — prior false positives to avoid re-flagging"},
		{string(memory.TypeSynthesis), "file-scoped review-history summary"},
		{string(memory.TypePRSummary), "prior PR summaries in this repo"},
		{string(memory.TypeReview), "past review comments on this repo"},
		{string(memory.TypeRule), "org-wide review rules (shared container only)"},
	}
}

// agenticMemoryTypeValues returns just the type strings, for the tool-schema
// enum and the drift-guard test.
func agenticMemoryTypeValues() []string {
	types := agenticMemoryTypes()
	vals := make([]string, len(types))
	for i, t := range types {
		vals[i] = t.Value
	}
	return vals
}

// memoryTypeAllowed reports whether t is a metadata.type the search_memory tool
// accepts as a filter. Empty t means "no filter" and is allowed by the caller.
func memoryTypeAllowed(t string) bool {
	for _, at := range agenticMemoryTypes() {
		if t == at.Value {
			return true
		}
	}
	return false
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
				Description: "Search Argus memory (patterns, scenarios, feedback, syntheses, PR summaries, past reviews, rules) by semantic query within a container tag, optionally narrowed to one metadata type.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query":         map[string]any{"type": "string", "description": "Semantic search query"},
						"container_tag": map[string]any{"type": "string", "description": fmt.Sprintf("Container tag to scope the search: %q (this repo) or %q (cross-repo patterns and org rules)", tags[0], tags[1])},
						"type":          map[string]any{"type": "string", "description": "Optional metadata.type filter to narrow results to one kind of memory. Omit to search all kinds.", "enum": agenticMemoryTypeValues()},
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
// repo. repo scopes new-shape container access to this review's repo so a
// prompt-injected PR cannot steer search_memory into another repo's container.
type ToolHandler struct {
	indexer    memory.Indexer
	store      *store.Store
	repo       string
	thresholds memory.Thresholds
}

func NewToolHandler(indexer memory.Indexer, st *store.Store, repo string, thresholds memory.Thresholds) *ToolHandler {
	return &ToolHandler{indexer: indexer, store: st, repo: repo, thresholds: thresholds.WithDefaults()}
}

// tagAllowed reports whether the review may search container tag. It accepts
// exactly the new-shape tags for THIS repo (agenticMemoryTags: RepoTagNew(repo)
// + SharedTag) — an exact match on this repo's container, so a review for repo
// X can never reach repo Y's container.
func (th *ToolHandler) tagAllowed(tag string) bool {
	for _, allowed := range agenticMemoryTags(th.repo) {
		if tag == allowed {
			return true
		}
	}
	return false
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
		Type         string `json:"type"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parsing args: %w", err)
	}

	if !th.tagAllowed(args.ContainerTag) {
		return fmt.Sprintf("Access denied: tag must be one of the containers for this review (%v)", agenticMemoryTags(th.repo)), nil
	}

	if args.Type != "" && !memoryTypeAllowed(args.Type) {
		return fmt.Sprintf("Invalid type filter %q: must be one of %v (or omit to search all kinds)", args.Type, agenticMemoryTypeValues()), nil
	}

	// Map the (already-allowlisted) raw container tag to a scope: SharedTag →
	// shared, otherwise the review's repo container. The search error PROPAGATES
	// to the LLM (distinct from an empty result) — the agentic tool must not
	// present a broken search as "no results".
	scope, repo := memory.ScopeRepo, th.repo
	if args.ContainerTag == memory.SharedTag {
		scope, repo = memory.ScopeShared, ""
	}
	results, err := th.indexer.Search(ctx, memory.MemoryQuery{
		Query:     args.Query,
		Repo:      repo,
		Scope:     scope,
		Type:      memory.MemoryType(args.Type),
		Limit:     5,
		Threshold: th.thresholds.FindingEnrich,
	})
	if err != nil {
		return fmt.Sprintf("search failed: %s", err), nil
	}

	if len(results) == 0 {
		return "No results found.", nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- Result %d (score: %.2f) ---\n%s\n\n", i+1, r.Score, r.Content))
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
