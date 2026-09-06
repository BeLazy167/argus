package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

const (
	searchMemoryDefaultLimit  = 10
	searchMemoryMaxLimit      = 25
	briefingCharCapReview     = 3200
	briefingCharCapSpecialist = 2400
)

// scopedRepo is the authorization step every repo-taking tool runs first:
// GetRepoScoped fails unless one of the caller's installations owns the repo.
// The returned repo is ALSO the tenant for any memory call that follows — read
// installation and short name off it, never off the request.
func (t *mcpTools) scopedRepo(ctx context.Context, repoID int64) (*store.Repo, string, error) {
	repo, err := t.srv.store.GetRepoScoped(ctx, repoID, t.scope.installationIDs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", errNotAccessible
		}
		return nil, "", err
	}
	_, short, ok := strings.Cut(repo.FullName, "/")
	if !ok || short == "" {
		return nil, "", errNotAccessible
	}
	return repo, short, nil
}

type searchMemoryInput struct {
	RepoID         int64   `json:"repo_id,omitempty" jsonschema:"local repo id from list_repos; required unless scope is shared"`
	InstallationID int64   `json:"installation_id,omitempty" jsonschema:"local installation id from list_repos; required only when scope is shared"`
	Query          string  `json:"query" jsonschema:"natural-language search text; max 4000 chars"`
	Scope          string  `json:"scope,omitempty" jsonschema:"repo (default), shared (org-wide memory), or both"`
	Type           string  `json:"type,omitempty" jsonschema:"restrict to one memory type: pattern, scenario, trace, feedback, synthesis, pr_summary, review, topology, rule"`
	Limit          int     `json:"limit,omitempty" jsonschema:"max results, default 10, max 25"`
	Threshold      float64 `json:"threshold,omitempty" jsonschema:"minimum similarity 0..1; 0 or omitted uses the pipeline's enrichment floor"`
}

type memoryMatch struct {
	CustomID     string            `json:"custom_id" jsonschema:"memory identity — pass to retire_memory"`
	PatternIDs   []int64           `json:"pattern_ids" jsonschema:"every contributing pattern row in this installation, ordered by id; empty means the memory has no pattern row and can only be retired — pass one to delete_memory"`
	Content      string            `json:"content"`
	Score        float64           `json:"score"`
	Type         string            `json:"type,omitempty"`
	Source       string            `json:"source,omitempty"`
	ContainerTag string            `json:"container_tag,omitempty"`
	WrittenAt    string            `json:"written_at,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type searchMemoryOutput struct {
	InstallationID      int64         `json:"installation_id" jsonschema:"the installation these results belong to — pass to retire_memory"`
	Matches             []memoryMatch `json:"matches"`
	EmbeddingsAvailable bool          `json:"embeddings_available" jsonschema:"false means no embedder is configured for this installation and similarity search returned nothing for that reason, not because nothing matched"`
	Truncated           bool          `json:"truncated" jsonschema:"true when the result hit the limit"`
}

func (t *mcpTools) searchMemory(ctx context.Context, _ *mcp.CallToolRequest, in searchMemoryInput) (*mcp.CallToolResult, searchMemoryOutput, error) {
	var zero searchMemoryOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, zero, errors.New("query is required")
	}
	// Before the repo lookup and well before the indexer: the query is embedded
	// verbatim, so an unbounded one is a paid call sized only by the body cap.
	if err := checkTextLimit("query", in.Query); err != nil {
		return nil, zero, err
	}
	// An unknown type is not a narrower search — it compiles to `type = $1` and
	// returns zero rows with embeddings_available true, which reads exactly like
	// a genuine no-hit. Refuse it the way an unknown scope is refused.
	if in.Type != "" && !memory.IsValidMemoryType(memory.MemoryType(in.Type)) {
		return nil, zero, fmt.Errorf("type must be one of: %s", strings.Join(memory.MemoryTypeNames(), ", "))
	}
	scope := memory.ContainerScope(in.Scope)
	if in.Scope == "" {
		scope = memory.ScopeRepo
	}

	var installationID int64
	var repoShort, containerTag string
	switch scope {
	case memory.ScopeShared:
		if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
			return nil, zero, errNotAccessible
		}
		installationID, containerTag = in.InstallationID, memory.SharedTag
	case memory.ScopeRepo, memory.ScopeBoth:
		if in.RepoID == 0 {
			return nil, zero, errors.New("repo_id is required for scope repo or both")
		}
		repo, short, err := t.scopedRepo(ctx, in.RepoID)
		if err != nil {
			if errors.Is(err, errNotAccessible) {
				return nil, zero, err
			}
			return nil, zero, t.internalErr(ctx, "search_memory", err)
		}
		installationID, repoShort = repo.InstallationID, short
		if scope == memory.ScopeRepo {
			containerTag = memory.RepoTagNew(short)
		}
	default:
		return nil, zero, errors.New("scope must be repo, shared, or both")
	}

	idx := t.srv.resolveIndexer(ctx, installationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	limit := in.Limit
	if limit <= 0 {
		limit = searchMemoryDefaultLimit
	}
	if limit > searchMemoryMaxLimit {
		limit = searchMemoryMaxLimit
	}
	threshold := in.Threshold
	if threshold <= 0 {
		threshold = memory.NewThresholds().FindingEnrich
	}

	// Filters and PointLookup are deliberately not exposed: PointLookup
	// licenses a predicate scan with no score gate, and malformed filters
	// compile to false and silently return nothing.
	matches, err := idx.Search(ctx, memory.MemoryQuery{
		Query: in.Query, Repo: repoShort, Scope: scope, Type: memory.MemoryType(in.Type), Limit: limit, Threshold: threshold,
	})
	if err != nil {
		return nil, zero, t.internalErr(ctx, "search_memory", err)
	}

	// N9: every identity → all contributing pattern rows, one scoped batch.
	// A lookup failure is a tool error; an empty array is a real answer.
	identities := make([]string, 0, len(matches))
	for _, m := range matches {
		identities = append(identities, m.ID)
	}
	patternIDs, err := t.srv.store.ListPatternIDsByIdentity(ctx, installationID, identities)
	if err != nil {
		return nil, zero, t.internalErr(ctx, "search_memory", err)
	}

	out := searchMemoryOutput{
		InstallationID:      installationID,
		Matches:             make([]memoryMatch, 0, len(matches)),
		EmbeddingsAvailable: t.srv.indexers.EmbedderAvailable(ctx, installationID),
		Truncated:           len(matches) >= limit,
	}
	for _, m := range matches {
		ids := patternIDs[m.ID]
		if ids == nil {
			ids = []int64{}
		}
		tag := containerTag
		if mt := m.Metadata["container_tag"]; mt != "" {
			tag = mt
		}
		out.Matches = append(out.Matches, memoryMatch{
			CustomID: m.ID, PatternIDs: ids, Content: m.Content, Score: m.Score,
			Type: m.Metadata["type"], Source: m.Metadata["source"], ContainerTag: tag, WrittenAt: m.Metadata["created_at"],
			Metadata: m.Metadata,
		})
	}
	return nil, out, nil
}

type getMemoryBriefingInput struct {
	RepoID   int64  `json:"repo_id" jsonschema:"local repo id from list_repos"`
	Query    string `json:"query" jsonschema:"what the briefing should be about, e.g. a file path or a change description; max 4000 chars"`
	FilePath string `json:"file_path,omitempty" jsonschema:"optional file to focus the briefing on"`
	Profile  string `json:"profile,omitempty" jsonschema:"review (default, broader: includes org rules and past-review context) or specialist (deep-review block)"`
	CharCap  int    `json:"char_cap,omitempty" jsonschema:"max characters; default 3200 for review, 2400 for specialist"`
}

type getMemoryBriefingOutput struct {
	Markdown string `json:"markdown"`
	Empty    bool   `json:"empty" jsonschema:"true when memory had nothing relevant; an empty briefing is a legitimate result"`
}

func (t *mcpTools) getMemoryBriefing(ctx context.Context, _ *mcp.CallToolRequest, in getMemoryBriefingInput) (*mcp.CallToolResult, getMemoryBriefingOutput, error) {
	var zero getMemoryBriefingOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	if in.RepoID == 0 {
		return nil, zero, errors.New("repo_id is required")
	}
	// Same reason as search_memory: the briefing embeds this query.
	if err := checkTextLimit("query", in.Query); err != nil {
		return nil, zero, err
	}
	repo, short, err := t.scopedRepo(ctx, in.RepoID)
	if err != nil {
		if errors.Is(err, errNotAccessible) {
			return nil, zero, err
		}
		return nil, zero, t.internalErr(ctx, "get_memory_briefing", err)
	}
	idx := t.srv.resolveIndexer(ctx, repo.InstallationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	profile, charCap := memory.ProfileReview, briefingCharCapReview
	switch in.Profile {
	case "", "review":
	case "specialist":
		profile, charCap = memory.ProfileSpecialist, briefingCharCapSpecialist
	default:
		return nil, zero, errors.New("profile must be review or specialist")
	}
	if in.CharCap > 0 {
		charCap = in.CharCap
	}
	owner, _, _ := strings.Cut(repo.FullName, "/")
	// Briefing runs OpenConventionConflicts first and hard-fails if that
	// query errors; that surfaces here as a tool error, which is correct.
	md, err := idx.Briefing(ctx, memory.BriefingQuery{
		Owner: owner, Repo: short, FilePath: in.FilePath, Query: in.Query,
		Options: memory.BriefingOptions{Profile: profile, Thresholds: memory.NewThresholds(), CharCap: charCap},
	})
	if err != nil {
		return nil, zero, t.internalErr(ctx, "get_memory_briefing", err)
	}
	return nil, getMemoryBriefingOutput{Markdown: md, Empty: strings.TrimSpace(md) == ""}, nil
}
