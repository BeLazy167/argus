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
	"github.com/BeLazy167/argus/backend/internal/store/db"
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
	st := &store.Store{Pool: pool, Q: db.New(pool)}
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
	st := &store.Store{Pool: pool, Q: db.New(pool)}
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
	st := &store.Store{Pool: pool, Q: db.New(pool)}
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
