package api

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listReposInput struct{}

type repoSummary struct {
	RepoID         int64  `json:"repo_id" jsonschema:"local repos.id — pass as repo_id to other tools"`
	FullName       string `json:"full_name" jsonschema:"owner/name as on GitHub"`
	InstallationID int64  `json:"installation_id" jsonschema:"local installations.id that owns this repo — pass as installation_id to other tools"`
	Enabled        bool   `json:"enabled" jsonschema:"whether Argus reviews are enabled for this repo"`
	DefaultBranch  string `json:"default_branch"`
}

type listReposOutput struct {
	Repos []repoSummary `json:"repos"`
}

// listRepos exists because three surfaces disagree about identifiers:
// MemoryQuery.Repo takes the short name, memory tenancy takes the local
// installation id, and review detail carries no full_name at all. One call
// resolves all three. ListReposScoped filters by installation in SQL.
func (t *mcpTools) listRepos(ctx context.Context, _ *mcp.CallToolRequest, _ listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	if err := t.requireScope(scopeRead); err != nil {
		return nil, listReposOutput{}, err
	}
	repos, err := t.srv.store.ListReposScoped(ctx, t.scope.installationIDs)
	if err != nil {
		return nil, listReposOutput{}, t.internalErr(ctx, "list_repos", err)
	}
	out := listReposOutput{Repos: make([]repoSummary, 0, len(repos))}
	for _, r := range repos {
		out.Repos = append(out.Repos, repoSummary{RepoID: r.ID, FullName: r.FullName, InstallationID: r.InstallationID, Enabled: r.Enabled, DefaultBranch: r.DefaultBranch})
	}
	return nil, out, nil
}
