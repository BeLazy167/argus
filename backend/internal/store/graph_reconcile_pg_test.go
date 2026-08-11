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
