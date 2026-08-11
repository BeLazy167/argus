package graph

import (
	"context"
	"log/slog"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// EdgeCallsAPI is the kind written for a derived cross-repo API edge. Distinct
// from every parsed kind so a consumer can tell an inference from a fact
// without inspecting anything else; code_edges.inferred carries the same answer
// in a form that survives new kinds being added.
const EdgeCallsAPI EdgeKind = "calls_api"

// maxInferredEdgesPerRun bounds one linking pass.
//
// Matching is a cross product: N call sites for one path against M
// declarations of it gives N*M edges. That is fine at real sizes and
// pathological if a generated client repeats the same call a thousand times, so
// the pass stops and says so rather than writing an unbounded number of rows
// into a table the whole graph reads. The cap is applied inside
// MatchAPIEndpoints, on the RESULT SLICE, so the cross product is never
// materialised in memory either.
const maxInferredEdgesPerRun = 2000

// apiEndpointStore is the persistence surface LinkAPIEndpoints needs. Narrow on
// purpose: an in-memory fake in the tests then covers the whole decision —
// which pairs become edges — without a database.
type apiEndpointStore interface {
	ListAPIEndpointsForInstallationOf(ctx context.Context, repoID int64) ([]store.APIEndpointRow, bool, error)
	ReplaceInferredAPIEdges(ctx context.Context, repoID int64, edges []store.InferredEdgeRow) (int, error)
}

// LinkAPIEndpoints re-derives the cross-repo calls_api edges of the
// installation that owns repoID, and returns how many it wrote.
//
// The pass computes the FULL derived set for the installation and replaces the
// stored one, because a derived edge has no other way to die: the FK cascade
// from code_nodes only fires when a node disappears, and a route can be renamed
// or a call deleted while both endpoints of the edge survive. Whole-
// installation scope is forced by the same fact — a route change in one repo
// changes the edges owned by every repo that calls it.
//
// Only CROSS-repo pairs become edges; MatchAPIEndpoints enforces that and
// documents why. Cross-repo edges are invisible to the repo-scoped blast-radius
// query — it filters repo_id inside the recursion — so this pass cannot alter
// any existing answer. Consuming them needs the installation-scoped traversal
// from item 2 of issue #221, which is not built.
//
// The edge direction is client → server. Blast radius walks edges backwards
// from a changed node, so an edge FROM the caller TO the handler is what makes
// "I changed this handler" surface the callers in other repositories.
//
// The read and the replace are not one transaction. Two repos of the same
// installation indexing concurrently therefore converge rather than serialise:
// each pass writes the COMPLETE derived set it saw, so the last writer leaves a
// consistent set and the next endpoint change re-derives it. A partial set is
// never written — that is what the truncation refusal below protects.
func LinkAPIEndpoints(ctx context.Context, st apiEndpointStore, repoID int64) (int, error) {
	rows, truncated, err := st.ListAPIEndpointsForInstallationOf(ctx, repoID)
	if err != nil {
		return 0, err
	}
	if truncated {
		// A clipped window is biased against the freshest rows: the rewrite
		// path assigns new ids to whichever repo was just indexed, so ORDER BY
		// id puts them last. Replacing the edge set from a partial view would
		// delete links this pass cannot re-derive, so it does nothing and says
		// so instead.
		slog.Warn("graph: installation endpoint window truncated, skipping API linking",
			"repo_id", repoID, "limit", store.MaxInstallationAPIEndpoints)
		return 0, nil
	}

	endpoints := make([]APIEndpoint, 0, len(rows))
	for _, r := range rows {
		endpoints = append(endpoints, APIEndpoint{
			Role: r.Role, Method: r.Method, Path: r.PathPattern, RawPath: r.RawPath,
			FilePath: r.FilePath, Line: r.Line, RepoID: r.RepoID, NodeID: r.NodeID,
		})
	}

	matches := MatchAPIEndpoints(endpoints)
	// Two call sites in one function collapse to one edge, because code_edges
	// is keyed on (repo, source, target, kind). Deduplicating here rather than
	// leaning on ON CONFLICT keeps the returned count honest.
	seen := make(map[store.InferredEdgeRow]struct{}, len(matches))
	edges := make([]store.InferredEdgeRow, 0, len(matches))
	for _, m := range matches {
		// repo_id is the CLIENT's repo: the edge is a fact about the caller,
		// and code_edges.repo_id is what scopes deletion when a repo goes away.
		e := store.InferredEdgeRow{
			RepoID:   m.Client.RepoID,
			SourceID: m.Client.NodeID,
			TargetID: m.Server.NodeID,
			Kind:     string(EdgeCallsAPI),
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		edges = append(edges, e)
	}

	return st.ReplaceInferredAPIEdges(ctx, repoID, edges)
}
