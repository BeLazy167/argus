package store

import (
	"context"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// publishGraphTestGeneration gives a repository publication authority without
// changing its physical code_nodes/code_edges projection. A later building or
// failed generation must not replace this pointer.
func publishGraphTestGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, commitSHA string) int64 {
	t.Helper()

	var generationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, published_at)
		VALUES ($1, $2, 'published', 2, 2, NOW())
		RETURNING id`, repoID, commitSHA).Scan(&generationID); err != nil {
		t.Fatalf("seed published graph generation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_published_generation_id = $2
		WHERE id = $1`, repoID, generationID); err != nil {
		t.Fatalf("publish graph generation: %v", err)
	}
	return generationID
}

type graphReadSnapshot struct {
	blastCTE     int
	blastPublic  int
	blastPGGraph int
	fileNodes    int
	chokePoints  int
	fileFanIn    int
	archNodes    int
	archEdges    int
	graphNodes   int
	graphEdges   int
}

func readGraphSnapshot(t *testing.T, ctx context.Context, st *Store, installationID, repoID int64, pgGraph bool) graphReadSnapshot {
	t.Helper()

	var got graphReadSnapshot
	var err error

	blast, err := st.blastRadiusCTE(ctx, installationID, repoID, []string{"target.go"}, 2)
	if err != nil {
		t.Fatalf("blast radius CTE: %v", err)
	}
	got.blastCTE = len(blast)

	blast, err = st.GetBlastRadius(ctx, installationID, repoID, []string{"target.go"}, 2)
	if err != nil {
		t.Fatalf("public blast radius: %v", err)
	}
	got.blastPublic = len(blast)

	if pgGraph {
		blast, err = st.blastRadiusPGGraph(ctx, installationID, repoID, []string{"target.go"}, 2)
		if err != nil {
			t.Fatalf("pgGraph blast radius: %v", err)
		}
		got.blastPGGraph = len(blast)
	}

	fileNodes, err := st.GetCodeNodesForFile(ctx, repoID, "target.go")
	if err != nil {
		t.Fatalf("code nodes for file: %v", err)
	}
	got.fileNodes = len(fileNodes)

	chokePoints, err := st.GetTopChokePoints(ctx, repoID, 10)
	if err != nil {
		t.Fatalf("top choke points: %v", err)
	}
	got.chokePoints = len(chokePoints)

	got.fileFanIn, err = st.q.GetFileFanIn(ctx, db.GetFileFanInParams{RepoID: repoID, FilePath: "target.go"})
	if err != nil {
		t.Fatalf("file fan-in: %v", err)
	}

	archNodes, err := st.ListArchNodes(ctx, repoID)
	if err != nil {
		t.Fatalf("architecture nodes: %v", err)
	}
	got.archNodes = len(archNodes)

	archEdges, err := st.ListArchFileEdges(ctx, repoID)
	if err != nil {
		t.Fatalf("architecture edges: %v", err)
	}
	got.archEdges = len(archEdges)

	graphNodes, err := st.ListGraphNodes(ctx, repoID)
	if err != nil {
		t.Fatalf("graph nodes: %v", err)
	}
	got.graphNodes = len(graphNodes)

	graphEdges, err := st.ListGraphEdges(ctx, repoID)
	if err != nil {
		t.Fatalf("graph edges: %v", err)
	}
	got.graphEdges = len(graphEdges)

	return got
}

// TestGraphSemanticReadsRequirePublishedAuthority is the publication boundary:
// retained legacy rows are staging/storage, not review, memory, metrics, or UI
// facts. Once A is published, a building or failed B leaves readers on A.
func TestGraphSemanticReadsRequirePublishedAuthority(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}
	tenant := seedBlastTenant(t, ctx, pool, "graph-read-authority")

	target := seedNode(t, ctx, st, tenant.repoA, "Target", "target.go")
	dependent := seedNode(t, ctx, st, tenant.repoA, "Dependent", "dependent.go")
	seedEdge(t, ctx, pool, tenant.repoA, dependent, target)

	pgGraph := st.pgGraphAvailable(ctx)
	if pgGraph {
		if err := st.RebuildCodeGraphProjection(ctx); err != nil {
			t.Fatalf("rebuild pgGraph fixture: %v", err)
		}
	} else {
		t.Log("pgGraph extension absent: direct pgGraph reader not exercised")
	}

	zero := graphReadSnapshot{}
	assertZero := func(stage string) {
		t.Helper()
		if got := readGraphSnapshot(t, ctx, st, tenant.installID, tenant.repoA, pgGraph); got != zero {
			t.Fatalf("%s exposed retained rows without publication authority:\n got %+v\nwant %+v", stage, got, zero)
		}
	}
	assertZero("NULL publication pointer")

	// A non-NULL pointer to another repository's generation is not authority.
	// graph_index_generations has a plain FK, so every reader must bind the
	// pointed generation back to the requested repository itself.
	foreignGeneration := publishGraphTestGeneration(t, ctx, pool, tenant.repoB, "published-other-repo")
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_published_generation_id = $2 WHERE id = $1`,
		tenant.repoA, foreignGeneration); err != nil {
		t.Fatalf("seed mismatched publication pointer: %v", err)
	}
	assertZero("mismatched repository publication pointer")

	publishedA := publishGraphTestGeneration(t, ctx, pool, tenant.repoA, "published-a")
	wantA := graphReadSnapshot{
		blastCTE: 2, blastPublic: 2, fileNodes: 1, chokePoints: 1,
		fileFanIn: 1, archNodes: 2, archEdges: 1, graphNodes: 2, graphEdges: 1,
	}
	if pgGraph {
		wantA.blastPGGraph = 2
	}
	assertA := func(stage string) {
		t.Helper()
		if got := readGraphSnapshot(t, ctx, st, tenant.installID, tenant.repoA, pgGraph); got != wantA {
			t.Fatalf("%s changed published A readers:\n got %+v\nwant %+v", stage, got, wantA)
		}
		var current int64
		if err := pool.QueryRow(ctx, `SELECT graph_published_generation_id FROM repos WHERE id = $1`, tenant.repoA).Scan(&current); err != nil {
			t.Fatalf("read publication pointer: %v", err)
		}
		if current != publishedA {
			t.Fatalf("%s moved publication pointer to %d, want A %d", stage, current, publishedA)
		}
	}

	assertA("published A")

	var buildingB int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations (repo_id, commit_sha, status, expected_files)
		VALUES ($1, 'building-b', 'building', 2) RETURNING id`, tenant.repoA).Scan(&buildingB); err != nil {
		t.Fatalf("seed building B: %v", err)
	}
	assertA("building B")

	if _, err := pool.Exec(ctx, `
		UPDATE graph_index_generations
		SET status = 'failed', error = 'fixture failure', updated_at = NOW()
		WHERE id = $1`, buildingB); err != nil {
		t.Fatalf("fail B: %v", err)
	}
	assertA("failed B")
}
