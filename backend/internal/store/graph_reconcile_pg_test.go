package store

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func TestReplaceCodeEdgesForFilesIsAuthoritative(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	installationID := seedInstallation(t, ctx, pool, "{}")
	repoID := apiSeedRepo(t, ctx, pool, installationID, "edges/exact")
	sourceID := apiSeedNode(t, ctx, pool, repoID, "Source", "source.go")
	targetID := apiSeedNode(t, ctx, pool, repoID, "Target", "target.go")

	if err := st.ReplaceCodeEdgesForFiles(ctx, repoID, []string{"source.go"}, []CodeEdgeRow{{SourceID: sourceID, TargetID: targetID, Kind: "calls"}}); err != nil {
		t.Fatalf("write edge: %v", err)
	}
	if err := st.ReplaceCodeEdgesForFiles(ctx, repoID, []string{"source.go"}, nil); err != nil {
		t.Fatalf("write empty edge snapshot: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_edges WHERE repo_id = $1 AND source_id = $2`, repoID, sourceID).Scan(&count); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty snapshot left %d stale edge(s)", count)
	}
}

func TestDeleteGraphFilesReconcilesRenamedPath(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	installationID := seedInstallation(t, ctx, pool, "{}")
	repoID := apiSeedRepo(t, ctx, pool, installationID, "paths/rename")
	nodeID := apiSeedNode(t, ctx, pool, repoID, "Old", "old.go")
	if _, err := st.ReplaceAPIEndpointsForFiles(ctx, repoID, []string{"old.go"}, []APIEndpointRow{{RepoID: repoID, NodeID: nodeID, Role: "server", Method: "GET", PathPattern: "/api/v1/old", RawPath: "/api/v1/old", FilePath: "old.go", Line: 1}}); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	if err := st.DeleteGraphFiles(ctx, repoID, []string{"old.go"}); err != nil {
		t.Fatalf("delete old path: %v", err)
	}
	var nodes, endpoints int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1 AND file_path = 'old.go'`, repoID).Scan(&nodes); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_endpoints WHERE repo_id = $1 AND file_path = 'old.go'`, repoID).Scan(&endpoints); err != nil {
		t.Fatalf("count endpoints: %v", err)
	}
	if nodes != 0 || endpoints != 0 {
		t.Fatalf("old path survived: nodes=%d endpoints=%d", nodes, endpoints)
	}
}
