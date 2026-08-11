package store

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func TestReplaceArchitectureAnnotationsDoesNotMutateCodeFacts(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	installationID := seedInstallation(t, ctx, pool, "{}")
	repoID := apiSeedRepo(t, ctx, pool, installationID, "annotation/facts")

	factID, err := st.UpsertCodeNodeFullWithHash(ctx, repoID, "function", "Serve", "serve.go", 10, 20, "go", 0, "error", "ctx context.Context", "exported", false, "", "package", "parser-hash")
	if err != nil {
		t.Fatalf("seed deterministic node: %v", err)
	}

	nodes := []ArchitectureAnnotationNode{
		{Name: "Serve", Kind: "function", FilePath: "serve.go", Language: "go"},
		{Name: "Store", Kind: "class", FilePath: "store.go", Language: "go"},
	}
	edges := []ArchitectureAnnotationEdgeInput{{Source: "Serve", Target: "Store", Kind: "calls"}}
	writtenNodes, writtenEdges, err := st.ReplaceArchitectureAnnotations(ctx, repoID, 42, nodes, edges)
	if err != nil {
		t.Fatalf("replace annotations: %v", err)
	}
	if writtenNodes != 2 || writtenEdges != 1 {
		t.Fatalf("written nodes/edges = %d/%d, want 2/1", writtenNodes, writtenEdges)
	}

	var gotID int64
	var start, end int
	var hash string
	if err := pool.QueryRow(ctx, `
		SELECT id, line_start, line_end, content_hash
		FROM code_nodes WHERE repo_id = $1 AND file_path = 'serve.go' AND name = 'Serve'`, repoID).
		Scan(&gotID, &start, &end, &hash); err != nil {
		t.Fatalf("load deterministic node: %v", err)
	}
	if gotID != factID || start != 10 || end != 20 || hash != "parser-hash" {
		t.Fatalf("deterministic fact mutated: id=%d lines=%d-%d hash=%q", gotID, start, end, hash)
	}

	// An empty edge snapshot must remove the previous annotation edge without
	// touching deterministic code_edges.
	if _, _, err := st.ReplaceArchitectureAnnotations(ctx, repoID, 42, nodes, nil); err != nil {
		t.Fatalf("replace with zero edges: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM architecture_annotation_edges WHERE repo_id = $1 AND pr_number = 42`, repoID).Scan(&count); err != nil {
		t.Fatalf("count annotation edges: %v", err)
	}
	if count != 0 {
		t.Fatalf("zero-edge snapshot left %d stale annotations", count)
	}
}

func TestReplaceArchitectureAnnotationsDropsAmbiguousEdges(t *testing.T) {
	pool, ctx := apiEndpointTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	installationID := seedInstallation(t, ctx, pool, "{}")
	repoID := apiSeedRepo(t, ctx, pool, installationID, "annotation/ambiguous")

	nodes := []ArchitectureAnnotationNode{
		{Name: "Handle", Kind: "function", FilePath: "a.go"},
		{Name: "Handle", Kind: "function", FilePath: "b.go"},
		{Name: "Store", Kind: "class", FilePath: "store.go"},
	}
	_, writtenEdges, err := st.ReplaceArchitectureAnnotations(ctx, repoID, 7, nodes, []ArchitectureAnnotationEdgeInput{{Source: "Handle", Target: "Store", Kind: "calls"}})
	if err != nil {
		t.Fatalf("replace annotations: %v", err)
	}
	if writtenEdges != 0 {
		t.Fatalf("ambiguous name produced %d edge(s), want 0", writtenEdges)
	}
}
