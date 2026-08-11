package graph

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

func generationTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func generationSeedInstallation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, settings string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO installations (installation_id, org_login, feature_flags) VALUES ((random()*1000000000)::bigint, 'graph-generation-test', $1::jsonb) RETURNING id`, settings).Scan(&id)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	return id
}

func generationSeedRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, installationID int64, fullName string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO repos (installation_id, github_id, full_name) VALUES ($1, (random()*1000000000)::bigint, $2) RETURNING id`, installationID, fullName).Scan(&id)
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	return id
}

func generationSeedNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, name, filePath string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language) VALUES ($1, 'function', $2, $3, 1, 2, 'go') RETURNING id`, repoID, name, filePath).Scan(&id)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return id
}

type fakeFullIndexGitHub struct {
	sha      string
	tree     ghpkg.RepoTree
	contents map[string]string
	fetchErr map[string]error
	treeRef  string
	fileRefs []string
}

func (f *fakeFullIndexGitHub) ResolveDefaultBranchCommit(context.Context, int64, string, string, string) (string, error) {
	return f.sha, nil
}
func (f *fakeFullIndexGitHub) GetRepoTree(_ context.Context, _ int64, _, _, ref string) (ghpkg.RepoTree, error) {
	f.treeRef = ref
	return f.tree, nil
}
func (f *fakeFullIndexGitHub) GetFileContent(_ context.Context, _ int64, _, _, path, ref string) (string, error) {
	f.fileRefs = append(f.fileRefs, ref)
	if err := f.fetchErr[path]; err != nil {
		return "", err
	}
	return f.contents[path], nil
}

func TestIndexRepoBoundedStagesThenAtomicallyPublishes(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/atomic")
	oldID := generationSeedNode(t, ctx, pool, repoID, "Old", "deleted.go")
	_ = oldID

	const snapshotSHA = "0123456789012345678901234567890123456789"
	gh := &fakeFullIndexGitHub{
		sha:  snapshotSHA,
		tree: ghpkg.RepoTree{Paths: []string{"a.go", "b.go", "README.md"}},
		contents: map[string]string{
			"a.go": "package p\nfunc Alpha() { Beta() }\n",
			"b.go": "package p\nfunc Beta() {}\n",
		},
		fetchErr: map[string]error{},
	}

	first, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	if first.Published || first.Snapshot.VisitedFiles != 1 || first.Snapshot.ExpectedFiles != 2 || first.Snapshot.SkippedFiles != 1 {
		t.Fatalf("first window = %+v", first)
	}
	var oldCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE id = $1`, oldID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 1 {
		t.Fatal("partial generation changed the published graph")
	}

	// A continuation belongs to the generation's immutable snapshot even when
	// the mutable default branch advances between bounded windows.
	gh.sha = "9999999999999999999999999999999999999999"
	second, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if !second.Published || !second.Snapshot.Complete {
		t.Fatalf("second window did not publish: %+v", second)
	}
	if gh.treeRef != snapshotSHA {
		t.Fatalf("tree ref = %q, want immutable generation SHA %q", gh.treeRef, snapshotSHA)
	}
	for _, ref := range gh.fileRefs {
		if ref != snapshotSHA {
			t.Fatalf("file ref = %q, want immutable generation SHA %q", ref, snapshotSHA)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE id = $1`, oldID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 {
		t.Fatal("deleted path survived full-generation reconciliation")
	}
	var edgeCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM code_edges ce
		JOIN code_nodes s ON s.id = ce.source_id
		JOIN code_nodes d ON d.id = ce.target_id
		WHERE ce.repo_id = $1 AND s.name = 'Alpha' AND d.name = 'Beta' AND ce.kind = 'calls'`, repoID).Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 {
		t.Fatalf("published edge count = %d, want 1", edgeCount)
	}
}

func TestIndexRepoBoundedRecordsAndRejectsTruncatedTree(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/truncated")
	gh := &fakeFullIndexGitHub{sha: "abcdef", tree: ghpkg.RepoTree{Paths: []string{"a.go"}, Truncated: true}}

	result, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 10, 0)
	if !errors.Is(err, ErrTruncatedTree) {
		t.Fatalf("error = %v, want ErrTruncatedTree", err)
	}
	if !result.Snapshot.TreeTruncated || result.Snapshot.Status != "failed" || result.Published {
		t.Fatalf("truncated snapshot = %+v", result)
	}
}

func TestIndexRepoBoundedRetriesFailedFileBeforePublishing(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/retry")
	gh := &fakeFullIndexGitHub{sha: "fedcba", tree: ghpkg.RepoTree{Paths: []string{"a.go"}}, contents: map[string]string{"a.go": "package p\nfunc A() {}\n"}, fetchErr: map[string]error{"a.go": errors.New("temporary fetch failure")}}
	first, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Published || first.Snapshot.FailedFiles != 1 {
		t.Fatalf("failed file published: %+v", first)
	}
	delete(gh.fetchErr, "a.go")
	second, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !second.Published || second.Snapshot.FailedFiles != 0 {
		t.Fatalf("retry did not publish: %+v", second)
	}
}

func TestConcurrentPRHeadsCannotMutatePublishedGeneration(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/pr-provenance")

	const baseSHA = "base-commit-sha"
	var generationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, published_at)
		VALUES ($1, $2, 'published', 1, 1, NOW()) RETURNING id`, repoID, baseSHA).Scan(&generationID); err != nil {
		t.Fatalf("seed published generation: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_published_generation_id = $2, graph_index_commit_sha = $3,
		  graph_index_expected_files = 1, graph_index_visited_files = 1, graph_indexed_at = NOW()
		WHERE id = $1`, repoID, generationID, baseSHA); err != nil {
		t.Fatalf("mark published generation: %v", err)
	}
	sourceID := generationSeedNode(t, ctx, pool, repoID, "BaseSource", "base.go")
	targetID := generationSeedNode(t, ctx, pool, repoID, "BaseTarget", "target.go")
	if _, err := pool.Exec(ctx, `INSERT INTO code_edges (repo_id, source_id, target_id, kind) VALUES ($1, $2, $3, 'calls')`, repoID, sourceID, targetID); err != nil {
		t.Fatalf("seed base edge: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, prSHA := range []string{"pr-head-one", "pr-head-two"} {
		prSHA := prSHA
		go func() {
			<-start
			// A nil GitHub client is intentional: provenance must reject the PR
			// ref before any fetch can occur.
			errs <- IndexFiles(ctx, st, nil, 1, "owner", "repo", prSHA, repoID, []string{"base.go"})
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; !errors.Is(err, ErrNonAuthoritativeGraphRef) {
			t.Fatalf("PR indexing error = %v, want ErrNonAuthoritativeGraphRef", err)
		}
	}

	var nodes, edges int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1`, repoID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_edges WHERE repo_id = $1`, repoID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if nodes != 2 || edges != 1 {
		t.Fatalf("published graph mutated by concurrent PR heads: nodes=%d edges=%d", nodes, edges)
	}
	snapshot, err := st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || snapshot.CommitSHA != baseSHA || snapshot.GenerationID != generationID {
		t.Fatalf("snapshot provenance changed: %+v", snapshot)
	}
}

func TestPublishedGenerationIncludesFileIdentityAndExplicitEdgeResolution(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := &store.Store{Pool: pool, Q: db.New(pool)}
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/identity")
	gh := &fakeFullIndexGitHub{
		sha:  "identity-sha",
		tree: ghpkg.RepoTree{Paths: []string{"a.go", "b.go", "c.go"}},
		contents: map[string]string{
			"a.go": "package p\nimport \"fmt\"\nfunc Alpha() { Handle(); Missing(); fmt.Println(1) }\n",
			"b.go": "package p\nfunc Handle() {}\n",
			"c.go": "package p\nfunc Handle() {}\n",
		},
		fetchErr: map[string]error{},
	}
	result, err := IndexRepoBounded(ctx, st, gh, 1, "o", "r", "main", repoID, 0, 0)
	if err != nil {
		t.Fatalf("publish generation: %v", err)
	}
	if !result.Published || !result.Snapshot.Complete {
		t.Fatalf("generation not complete: %+v", result)
	}

	var fileCount, aLOC int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1 AND kind = 'file'`, repoID).Scan(&fileCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT line_end FROM code_nodes WHERE repo_id = $1 AND kind = 'file' AND name = 'a.go'`, repoID).Scan(&aLOC); err != nil {
		t.Fatal(err)
	}
	if fileCount != 3 || aLOC != 3 {
		t.Fatalf("file identities/LOC = %d/%d, want 3/3", fileCount, aLOC)
	}

	assertEdge := func(sourceName, sourceKind, targetName, targetKind, kind string) {
		t.Helper()
		var count int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM code_edges e
			JOIN code_nodes s ON s.id = e.source_id
			JOIN code_nodes d ON d.id = e.target_id
			WHERE e.repo_id = $1 AND s.name = $2 AND s.kind = $3
			  AND d.name = $4 AND d.kind = $5 AND e.kind = $6`,
			repoID, sourceName, sourceKind, targetName, targetKind, kind).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("edge %s(%s) -%s-> %s(%s) count = %d, want 1", sourceName, sourceKind, kind, targetName, targetKind, count)
		}
	}
	assertEdge("a.go", "file", "module:fmt", "module", "imports")
	assertEdge("Alpha", "function", "ambiguous:Handle", "module", "calls")
	assertEdge("Alpha", "function", "unresolved:Missing", "module", "calls")

	var arbitrary int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM code_edges e
		JOIN code_nodes s ON s.id = e.source_id
		JOIN code_nodes d ON d.id = e.target_id
		WHERE e.repo_id = $1 AND s.name = 'Alpha' AND d.name = 'Handle' AND e.kind = 'calls'`, repoID).Scan(&arbitrary); err != nil {
		t.Fatal(err)
	}
	if arbitrary != 0 {
		t.Fatalf("ambiguous Handle attached to %d arbitrary concrete node(s)", arbitrary)
	}
}
