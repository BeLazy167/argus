package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
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
	sha       string
	tree      ghpkg.RepoTree
	treeErr   error
	contents  map[string]string
	fetchErr  map[string]error
	treeRef   string
	fileRefs  []string
	filePaths []string
}

func (f *fakeFullIndexGitHub) ResolveDefaultBranchCommit(context.Context, int64, string, string, string) (string, error) {
	return f.sha, nil
}
func (f *fakeFullIndexGitHub) GetRepoTree(_ context.Context, _ int64, _, _, ref string) (ghpkg.RepoTree, error) {
	f.treeRef = ref
	if f.treeErr != nil {
		return ghpkg.RepoTree{}, f.treeErr
	}
	return f.tree, nil
}
func (f *fakeFullIndexGitHub) GetFileContent(_ context.Context, _ int64, _, _, path, ref string) (string, error) {
	f.fileRefs = append(f.fileRefs, ref)
	f.filePaths = append(f.filePaths, path)
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
	var stagedPayloads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files WHERE generation_id = $1`, second.Snapshot.GenerationID).Scan(&stagedPayloads); err != nil {
		t.Fatal(err)
	}
	if stagedPayloads != 0 {
		t.Fatalf("published generation retained %d staged payloads, want 0", stagedPayloads)
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

func TestIndexRepoBoundedFailsPinnedGenerationWhenImmutableTreeDisappears(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/permanent-tree")
	ghClient := &fakeFullIndexGitHub{
		sha:      "pinned-sha",
		tree:     ghpkg.RepoTree{Paths: []string{"a.go", "b.go"}},
		contents: map[string]string{"a.go": "package p\n", "b.go": "package p\n"},
		fetchErr: map[string]error{},
	}
	first, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil || first.Snapshot.Status != "building" {
		t.Fatalf("first window = %+v, err=%v", first, err)
	}

	ghClient.treeErr = &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	second, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err == nil || !ghpkg.IsPermanentGitObjectError(err) {
		t.Fatalf("tree error = %v, want permanent error", err)
	}
	if second.Snapshot.GenerationID != first.Snapshot.GenerationID || second.Snapshot.Status != "failed" || second.Published {
		t.Fatalf("terminal generation = %+v, want same failed generation", second)
	}
}

func TestIndexRepoBoundedKeepsFirstWindowGenerationForTransientTreeFailure(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/transient-first-tree")
	ghClient := &fakeFullIndexGitHub{
		sha:      "pinned-sha",
		treeErr:  &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}},
		tree:     ghpkg.RepoTree{Paths: []string{"a.go"}},
		contents: map[string]string{"a.go": "package p\n"},
		fetchErr: map[string]error{},
	}
	first, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err == nil || ghpkg.IsPermanentGitObjectError(err) {
		t.Fatalf("first tree error = %v, want transient error", err)
	}
	if first.Snapshot.GenerationID == 0 || first.Snapshot.Status != "building" || first.Published {
		t.Fatalf("retryable first generation = %+v, want persisted building generation", first)
	}

	// A transient retry resumes the immutable head even if the mutable branch
	// advances before GitHub recovers.
	ghClient.sha = "new-default-head"
	ghClient.treeErr = nil
	second, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil || !second.Published {
		t.Fatalf("resumed window = %+v, err=%v", second, err)
	}
	if second.Snapshot.GenerationID != first.Snapshot.GenerationID || second.Snapshot.CommitSHA != "pinned-sha" || ghClient.treeRef != "pinned-sha" {
		t.Fatalf("resumed generation = %+v tree_ref=%q, want first immutable head", second, ghClient.treeRef)
	}
}

func TestFirstWindowPermanentTreeFailureBacksOffPromptAndBackfillByHead(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationDBID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationDBID, "generation/permanent-first-tree")
	if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = $1`, repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE repos SET enabled = false WHERE id = $1`, repoID)
	})

	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		SELECT i.installation_id, r.github_id FROM repos r
		JOIN installations i ON i.id = r.installation_id WHERE r.id = $1`, repoID).
		Scan(&githubInstallationID, &githubRepoID); err != nil {
		t.Fatal(err)
	}
	const failedHead = "fffffffffffffffffffffffffffffffffffffff4"
	observedAt := time.Now().UTC()
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"generation/permanent-first-tree", "main", failedHead, observedAt); err != nil || !scheduled {
		t.Fatalf("schedule failed head = %v, err=%v", scheduled, err)
	}
	permanent := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	ghClient := &fakeFullIndexGitHub{sha: failedHead, treeErr: permanent}
	failed, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID,
		"generation", "permanent-first-tree", "main", repoID, 0, 0)
	if !errors.Is(err, permanent) || failed.Snapshot.GenerationID == 0 ||
		failed.Snapshot.Status != "failed" || failed.Snapshot.CommitSHA != failedHead || failed.Published {
		t.Fatalf("first permanent tree failure = %+v, err=%v", failed, err)
	}
	var publishedGenerationID *int64
	var nodeCount int
	if err := pool.QueryRow(ctx, `
		SELECT graph_published_generation_id,
		  (SELECT count(*)::int FROM code_nodes WHERE repo_id = repos.id)
		FROM repos WHERE id = $1`, repoID).Scan(&publishedGenerationID, &nodeCount); err != nil {
		t.Fatal(err)
	}
	if publishedGenerationID != nil || nodeCount != 0 {
		t.Fatalf("failed first generation partially published: generation=%v nodes=%d", publishedGenerationID, nodeCount)
	}

	assertDue := func(want bool, phase string) {
		t.Helper()
		prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
		if err != nil {
			t.Fatal(err)
		}
		backfill, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
		if err != nil {
			t.Fatal(err)
		}
		promptDue := containsGraphTarget(prompt, repoID, "main")
		backfillDue := containsGraphTarget(backfill, repoID, "main")
		if promptDue != want || backfillDue != want {
			t.Fatalf("%s due state: prompt=%v backfill=%v, want both %v", phase, promptDue, backfillDue, want)
		}
	}
	assertDue(false, "recent permanent failure")

	if _, err := pool.Exec(ctx, `
		UPDATE graph_index_generations SET updated_at = NOW() - INTERVAL '7 hours'
		WHERE id = $1`, failed.Snapshot.GenerationID); err != nil {
		t.Fatal(err)
	}
	assertDue(true, "expired permanent failure")

	if _, err := pool.Exec(ctx, `
		UPDATE graph_index_generations SET updated_at = NOW() WHERE id = $1`, failed.Snapshot.GenerationID); err != nil {
		t.Fatal(err)
	}
	assertDue(false, "renewed permanent failure")

	const newHead = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee5"
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"generation/permanent-first-tree", "main", newHead, observedAt.Add(time.Minute)); err != nil || !scheduled {
		t.Fatalf("schedule new head = %v, err=%v", scheduled, err)
	}
	assertDue(true, "different head")
}

func TestIndexRepoBoundedFailsPermanentFailureWithoutMutatingPublishedGeneration(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/permanent-failure")
	oldID := generationSeedNode(t, ctx, pool, repoID, "PublishedBeforeFailure", "old.go")
	var publishedGenerationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, published_at)
		VALUES ($1, 'published-sha', 'published', 1, 1, NOW()) RETURNING id`, repoID).Scan(&publishedGenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_published_generation_id = $2, graph_indexed_at = NOW(),
		  graph_index_commit_sha = 'published-sha', graph_index_expected_files = 1,
		  graph_index_visited_files = 1
		WHERE id = $1`, repoID, publishedGenerationID); err != nil {
		t.Fatal(err)
	}
	permanent := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	ghClient := &fakeFullIndexGitHub{
		sha:      "immutable-sha",
		tree:     ghpkg.RepoTree{Paths: []string{"a.go", "b.go"}},
		contents: map[string]string{"b.go": "package p\nfunc B() {}\n"},
		fetchErr: map[string]error{"a.go": permanent},
	}

	first, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	if first.Published || first.Snapshot.Status != "failed" || first.Snapshot.Complete ||
		first.Snapshot.FailedFiles != 1 || first.Snapshot.UnavailableFiles != 1 {
		t.Fatalf("permanent-failure window = %+v, want terminal failed generation", first)
	}
	if got := ghClient.filePaths; len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("fetches = %v, want stop immediately after permanent failure", got)
	}
	var oldCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE id = $1`, oldID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 1 {
		t.Fatal("failed generation changed the published projection")
	}
	var currentPublishedID int64
	if err := pool.QueryRow(ctx, `SELECT graph_published_generation_id FROM repos WHERE id = $1`, repoID).Scan(&currentPublishedID); err != nil {
		t.Fatal(err)
	}
	if currentPublishedID != publishedGenerationID {
		t.Fatalf("published generation = %d, want unchanged %d", currentPublishedID, publishedGenerationID)
	}
	var unavailable int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'unavailable'`, first.Snapshot.GenerationID).Scan(&unavailable); err != nil {
		t.Fatal(err)
	}
	if unavailable != 0 {
		t.Fatalf("terminal generation retained %d unavailable payloads, want 0", unavailable)
	}

	waitingRepoID := generationSeedRepo(t, ctx, pool, installationID, "generation/waiting-after-permanent")
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET enabled = true,
		  graph_index_attempted_at = CASE WHEN id = $1 THEN NOW() ELSE NULL END
		WHERE id = ANY($2::bigint[])`, repoID, []int64{repoID, waitingRepoID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE repos SET enabled = false WHERE id = ANY($1::bigint[])`, []int64{repoID, waitingRepoID})
	})
	targets, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].RepoID != waitingRepoID {
		t.Fatalf("target after permanent failure = %+v, want waiting repo %d", targets, waitingRepoID)
	}
}

func TestListReposDueForGraphIndexDoesNotLetBuildingGenerationStarveQueue(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	buildingRepoID := generationSeedRepo(t, ctx, pool, installationID, "scheduler/building")
	waitingRepoID := generationSeedRepo(t, ctx, pool, installationID, "scheduler/waiting")
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET enabled = true,
		  graph_index_attempted_at = CASE WHEN id = $1 THEN NOW() ELSE NULL END
		WHERE id = ANY($2::bigint[])`, buildingRepoID, []int64{buildingRepoID, waitingRepoID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE repos SET enabled = false WHERE id = ANY($1::bigint[])`, []int64{buildingRepoID, waitingRepoID})
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_index_generations (repo_id, commit_sha, status, expected_files)
		VALUES ($1, 'building-sha', 'building', 2)`, buildingRepoID); err != nil {
		t.Fatal(err)
	}

	targets, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].RepoID != waitingRepoID {
		t.Fatalf("first target = %+v, want never-attempted repo %d", targets, waitingRepoID)
	}
}

func TestPublishedGenerationIncludesFileIdentityAndExplicitEdgeResolution(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
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

func TestDefaultBranchRefreshIsScopedIdempotentAndSurvivesInFlightGeneration(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "refresh/scoped")
	if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = $1`, repoID); err != nil {
		t.Fatal(err)
	}
	generationSeedNode(t, ctx, pool, repoID, "PublishedBeforeRefresh", "old.go")

	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		SELECT i.installation_id, r.github_id FROM repos r
		JOIN installations i ON i.id = r.installation_id WHERE r.id = $1`, repoID).
		Scan(&githubInstallationID, &githubRepoID); err != nil {
		t.Fatal(err)
	}
	observedA := time.Now().UTC()
	const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID+1, githubRepoID,
		"refresh/scoped", "main", commitA, observedA); err != nil || scheduled {
		t.Fatalf("cross-tenant schedule = %v, %v", scheduled, err)
	}
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"other/scoped", "main", commitA, observedA); err != nil || scheduled {
		t.Fatalf("wrong-repo schedule = %v, %v", scheduled, err)
	}
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/scoped", "feature", commitA, observedA); err != nil || scheduled {
		t.Fatalf("non-default-branch schedule = %v, %v", scheduled, err)
	}
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/scoped", "main", commitA, observedA); err != nil || !scheduled {
		t.Fatalf("default-head schedule = %v, %v", scheduled, err)
	}
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/scoped", "main", commitA, observedA.Add(time.Minute)); err != nil || scheduled {
		t.Fatalf("duplicate schedule = %v, %v", scheduled, err)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT graph_refresh_version FROM repos WHERE id = $1`, repoID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("refresh version after duplicate = %d, want 1", version)
	}

	ghClient := &fakeFullIndexGitHub{
		sha:  commitA,
		tree: ghpkg.RepoTree{Paths: []string{"a.go", "b.go"}},
		contents: map[string]string{
			"a.go": "package refresh\nfunc A() {}\n",
			"b.go": "package refresh\nfunc B() {}\n",
		},
		fetchErr: map[string]error{},
	}
	first, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "scoped", "main", repoID, 1, 0)
	if err != nil || first.Published {
		t.Fatalf("first generation window = %+v, %v", first, err)
	}
	var oldCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1 AND file_path = 'old.go'`, repoID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 1 {
		t.Fatal("staging a refresh mutated the published graph")
	}

	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/scoped", "main", commitB, observedA.Add(2*time.Minute)); err != nil || !scheduled {
		t.Fatalf("second default-head schedule = %v, %v", scheduled, err)
	}
	second, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "scoped", "main", repoID, 1, 0)
	if err != nil || !second.Published {
		t.Fatalf("finish immutable generation A = %+v, %v", second, err)
	}
	snapshot, err := st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PublishedCommitSHA != commitA || snapshot.DefaultHeadSHA != commitB || snapshot.Current || snapshot.RefreshRequestedAt == nil {
		t.Fatalf("freshness after raced publish = %+v", snapshot)
	}
	prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range prompt {
		found = found || target.RepoID == repoID
	}
	if !found {
		t.Fatalf("prompt queue omitted refreshed repo %d: %+v", repoID, prompt)
	}

	// An older delayed delivery cannot replace the newer observed head.
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/scoped", "main", commitA, observedA); err != nil || scheduled {
		t.Fatalf("older delivery schedule = %v, %v", scheduled, err)
	}

	ghClient.sha = commitB
	final, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "scoped", "main", repoID, 0, 0)
	if err != nil || !final.Published {
		t.Fatalf("publish generation B = %+v, %v", final, err)
	}
	snapshot, err = st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Current || snapshot.PublishedCommitSHA != commitB || snapshot.DefaultHeadSHA != commitB || snapshot.RefreshRequestedAt != nil {
		t.Fatalf("final freshness = %+v", snapshot)
	}
}

func TestFailedDefaultHeadGenerationRemainsQueuedAndRetriesFromPublishedAuthority(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "refresh/failed-next-poll")
	if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = $1`, repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE repos SET enabled = false WHERE id = $1`, repoID)
	})

	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		SELECT i.installation_id, r.github_id FROM repos r
		JOIN installations i ON i.id = r.installation_id WHERE r.id = $1`, repoID).
		Scan(&githubInstallationID, &githubRepoID); err != nil {
		t.Fatal(err)
	}
	const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
	ghClient := &fakeFullIndexGitHub{
		sha: commitA, tree: ghpkg.RepoTree{Paths: []string{"a.go"}},
		contents: map[string]string{"a.go": "package refresh\nfunc PublishedA() {}\n"},
		fetchErr: map[string]error{},
	}
	publishedA, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "failed-next-poll", "main", repoID, 0, 0)
	if err != nil || !publishedA.Published {
		t.Fatalf("publish A = %+v, err=%v", publishedA, err)
	}

	observedB := time.Now().UTC()
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/failed-next-poll", "main", commitB, observedB); err != nil || !scheduled {
		t.Fatalf("schedule B = %v, err=%v", scheduled, err)
	}
	permanent := &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	ghClient.sha = commitB
	ghClient.tree = ghpkg.RepoTree{Paths: []string{"b.go"}}
	ghClient.contents = map[string]string{"b.go": "package refresh\nfunc PublishedB() {}\n"}
	ghClient.fetchErr = map[string]error{"b.go": permanent}
	failedB, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "failed-next-poll", "main", repoID, 0, 0)
	if err != nil {
		t.Fatalf("stage failed B: %v", err)
	}
	if failedB.Unchanged || failedB.Published || failedB.Snapshot.Status != "failed" {
		t.Fatalf("failed B = %+v", failedB)
	}
	failedGenerationID := failedB.Snapshot.GenerationID

	snapshot, err := st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PublishedCommitSHA != commitA || snapshot.Current || snapshot.RefreshRequestedAt == nil {
		t.Fatalf("freshness after failed B = %+v", snapshot)
	}
	var publishedAStillVisible int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1 AND name = 'PublishedA'`, repoID).Scan(&publishedAStillVisible); err != nil {
		t.Fatal(err)
	}
	if publishedAStillVisible != 1 {
		t.Fatal("failed B changed published generation A")
	}
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/failed-next-poll", "main", commitB, observedB.Add(time.Minute)); err != nil || scheduled {
		t.Fatalf("duplicate failed B delivery = %v, err=%v", scheduled, err)
	}
	snapshot, err = st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RefreshRequestedAt == nil {
		t.Fatal("duplicate failed B delivery consumed the pending retry")
	}
	for poll := 0; poll < 3; poll++ {
		prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if containsGraphTarget(prompt, repoID, "main") {
			t.Fatalf("prompt poll %d retried permanently failed B inside quota backoff: %+v", poll, prompt)
		}
	}

	// A different requested head is not charged to B's failure backoff.
	const commitC = "ccccccccccccccccccccccccccccccccccccccc3"
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/failed-next-poll", "main", commitC, observedB.Add(2*time.Minute)); err != nil || !scheduled {
		t.Fatalf("schedule C = %v, err=%v", scheduled, err)
	}
	prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsGraphTarget(prompt, repoID, "main") {
		t.Fatalf("new head C did not bypass failed B backoff: %+v", prompt)
	}

	// A force-push back to B is still protected until B's immutable terminal
	// timestamp expires, then it becomes due and creates a replacement generation.
	if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		"refresh/failed-next-poll", "main", commitB, observedB.Add(3*time.Minute)); err != nil || !scheduled {
		t.Fatalf("reschedule B = %v, err=%v", scheduled, err)
	}
	prompt, err = st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if containsGraphTarget(prompt, repoID, "main") {
		t.Fatalf("recent failed B was due before backoff expiry: %+v", prompt)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE graph_index_generations SET updated_at = NOW() - INTERVAL '7 hours'
		WHERE id = $1 AND status = 'failed'`, failedGenerationID); err != nil {
		t.Fatalf("age immutable failed generation: %v", err)
	}
	prompt, err = st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsGraphTarget(prompt, repoID, "main") {
		t.Fatalf("backoff expiry did not make B due: %+v", prompt)
	}

	delete(ghClient.fetchErr, "b.go")
	retriedB, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "refresh", "failed-next-poll", "main", repoID, 0, 0)
	if err != nil {
		t.Fatalf("retry B: %v", err)
	}
	if retriedB.Unchanged || !retriedB.Published || retriedB.Snapshot.PublishedCommitSHA != commitB {
		t.Fatalf("retry B consumed as unchanged = %+v", retriedB)
	}
	if retriedB.Snapshot.GenerationID == failedGenerationID {
		t.Fatalf("retry mutated failed generation %d instead of creating an immutable replacement", failedGenerationID)
	}
	var failedStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM graph_index_generations WHERE id = $1`, failedGenerationID).Scan(&failedStatus); err != nil {
		t.Fatal(err)
	}
	if failedStatus != "failed" {
		t.Fatalf("failed generation status = %q, want failed", failedStatus)
	}
}

func TestPermanentFailureBackoffProtectsPromptAndBackfillQuotaByHead(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	failedRepoID := generationSeedRepo(t, ctx, pool, installationID, "quota/permanent-failure")
	buildingRepoID := generationSeedRepo(t, ctx, pool, installationID, "quota/transient-building")
	if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = ANY($1::bigint[])`,
		[]int64{failedRepoID, buildingRepoID}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE repos SET enabled = false WHERE id = ANY($1::bigint[])`,
			[]int64{failedRepoID, buildingRepoID})
	})

	const failedHead = "fffffffffffffffffffffffffffffffffffffff1"
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_default_head_sha = $2,
		  graph_refresh_requested_at = NOW(), graph_refresh_commit_sha = $2
		WHERE id = $1`, failedRepoID, failedHead); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, failed_files, error, updated_at)
		VALUES ($1, $2, 'failed', 1, 1, 1, 'permanent object failure', NOW())`, failedRepoID, failedHead); err != nil {
		t.Fatal(err)
	}

	prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if containsGraphTarget(prompt, failedRepoID, "main") || containsGraphTarget(backfill, failedRepoID, "main") {
		t.Fatalf("recent permanent failure consumed quota: prompt=%+v backfill=%+v", prompt, backfill)
	}

	const newHead = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee2"
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_default_head_sha = $2,
		  graph_refresh_requested_at = NOW(), graph_refresh_commit_sha = $2
		WHERE id = $1`, failedRepoID, newHead); err != nil {
		t.Fatal(err)
	}
	prompt, err = st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	backfill, err = st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsGraphTarget(prompt, failedRepoID, "main") || !containsGraphTarget(backfill, failedRepoID, "main") {
		t.Fatalf("new head did not bypass old failure: prompt=%+v backfill=%+v", prompt, backfill)
	}

	const buildingHead = "ddddddddddddddddddddddddddddddddddddddd3"
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_default_head_sha = $2,
		  graph_refresh_requested_at = NOW(), graph_refresh_commit_sha = $2
		WHERE id = $1`, buildingRepoID, buildingHead); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_index_generations (repo_id, commit_sha, status, expected_files)
		VALUES ($1, $2, 'building', 2)`, buildingRepoID, buildingHead); err != nil {
		t.Fatal(err)
	}
	prompt, err = st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	backfill, err = st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsGraphTarget(prompt, buildingRepoID, "main") || !containsGraphTarget(backfill, buildingRepoID, "main") {
		t.Fatalf("resumable building work was throttled: prompt=%+v backfill=%+v", prompt, backfill)
	}
}

func TestDefaultBranchMutationInvalidatesFreshnessAndQueuesPromptRefresh(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *store.Store, int64, int64, int64, string) error
	}{
		{
			name: "installation sync upsert",
			mutate: func(ctx context.Context, st *store.Store, repoID, installationID, githubRepoID int64, fullName string) error {
				_, err := st.UpsertRepo(ctx, installationID, githubRepoID, fullName, "trunk")
				return err
			},
		},
		{
			name: "authenticated update",
			mutate: func(ctx context.Context, st *store.Store, repoID, _, _ int64, _ string) error {
				branch := "trunk"
				_, err := st.UpdateRepo(ctx, repoID, nil, &branch, nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, ctx := generationTestPool(t)
			st := store.NewWithDB(pool)
			installationID := generationSeedInstallation(t, ctx, pool, "{}")
			fullName := "branch-change/" + strings.ReplaceAll(tc.name, " ", "-")
			repoID := generationSeedRepo(t, ctx, pool, installationID, fullName)
			if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = $1`, repoID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `UPDATE repos SET enabled = false WHERE id = $1`, repoID)
			})

			var githubInstallationID, githubRepoID int64
			if err := pool.QueryRow(ctx, `
				SELECT i.installation_id, r.github_id FROM repos r
				JOIN installations i ON i.id = r.installation_id WHERE r.id = $1`, repoID).
				Scan(&githubInstallationID, &githubRepoID); err != nil {
				t.Fatal(err)
			}
			const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3"
			ghClient := &fakeFullIndexGitHub{
				sha: commitA, tree: ghpkg.RepoTree{Paths: []string{"a.go"}},
				contents: map[string]string{"a.go": "package branchchange\nfunc MainA() {}\n"},
				fetchErr: map[string]error{},
			}
			publishedA, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "branch-change", strings.TrimPrefix(fullName, "branch-change/"), "main", repoID, 0, 0)
			if err != nil || !publishedA.Published || !publishedA.Snapshot.Current {
				t.Fatalf("publish main A = %+v, err=%v", publishedA, err)
			}

			// A trunk push delivered before this authoritative mutation is ignored
			// because main is still stored as the default. The mutation must itself
			// queue a branch-head resolve; no second push is required.
			if scheduled, err := st.ScheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
				fullName, "trunk", "ignored-trunk-head", time.Now().UTC()); err != nil || scheduled {
				t.Fatalf("pre-sync trunk push = %v, err=%v", scheduled, err)
			}
			if err := tc.mutate(ctx, st, repoID, installationID, githubRepoID, fullName); err != nil {
				t.Fatalf("mutate default branch: %v", err)
			}

			snapshot, err := st.GetGraphSnapshot(ctx, repoID)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Current || snapshot.RefreshRequestedAt == nil || snapshot.PublishedCommitSHA != commitA || snapshot.DefaultHeadSHA != "" {
				t.Fatalf("freshness after main->trunk = %+v", snapshot)
			}
			prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
			if err != nil {
				t.Fatal(err)
			}
			if !containsGraphTarget(prompt, repoID, "trunk") {
				t.Fatalf("prompt queue omitted trunk refresh: %+v", prompt)
			}

			const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb4"
			ghClient.sha = commitB
			ghClient.tree = ghpkg.RepoTree{Paths: []string{"b.go"}}
			ghClient.contents = map[string]string{"b.go": "package branchchange\nfunc TrunkB() {}\n"}
			refreshed, err := IndexRepoBounded(ctx, st, ghClient, githubInstallationID, "branch-change", strings.TrimPrefix(fullName, "branch-change/"), "trunk", repoID, 0, 0)
			if err != nil || !refreshed.Published || !refreshed.Snapshot.Current || refreshed.Snapshot.PublishedCommitSHA != commitB {
				t.Fatalf("publish trunk B = %+v, err=%v", refreshed, err)
			}
		})
	}
}

func containsGraphTarget(targets []store.RepoIndexTarget, repoID int64, defaultBranch string) bool {
	for _, target := range targets {
		if target.RepoID == repoID && target.DefaultBranch == defaultBranch {
			return true
		}
	}
	return false
}

func TestGraphGenerationTerminalCleanupKeepsOnlyBuildingPayloads(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/terminal-cleanup")
	var publishedID int64
	if err := pool.QueryRow(ctx, `INSERT INTO graph_index_generations
		(repo_id, commit_sha, status, expected_files, visited_files, published_at)
		VALUES ($1, 'published-head', 'published', 1, 1, NOW()) RETURNING id`, repoID).Scan(&publishedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO graph_index_generation_files (generation_id, file_path, status) VALUES ($1, 'old.go', 'ready')`, publishedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE repos SET graph_published_generation_id = $2 WHERE id = $1`, repoID, publishedID); err != nil {
		t.Fatal(err)
	}

	first, err := st.BeginGraphGeneration(ctx, repoID, "head-0", 1, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	var retainedPublishedID int64
	var publishedStatus string
	var publishedPayloads int
	if err := pool.QueryRow(ctx, `SELECT r.graph_published_generation_id, g.status,
		(SELECT count(*)::int FROM graph_index_generation_files WHERE generation_id = g.id)
		FROM repos r JOIN graph_index_generations g ON g.id = r.graph_published_generation_id
		WHERE r.id = $1`, repoID).Scan(&retainedPublishedID, &publishedStatus, &publishedPayloads); err != nil {
		t.Fatal(err)
	}
	if retainedPublishedID != publishedID || publishedStatus != "published" || publishedPayloads != 0 {
		t.Fatalf("published audit cleanup: pointer=%d status=%s payloads=%d", retainedPublishedID, publishedStatus, publishedPayloads)
	}
	if _, err := st.StageGraphGenerationFile(ctx, repoID, first.GenerationID, "a.go", []byte("[]"), []byte("[]"), []byte("[]"), nil, false); err != nil {
		t.Fatal(err)
	}
	resumed, err := st.BeginGraphGeneration(ctx, repoID, "head-0", 1, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GenerationID != first.GenerationID {
		t.Fatalf("same head created generation %d, want resumed %d", resumed.GenerationID, first.GenerationID)
	}
	var currentPayloads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files WHERE generation_id = $1`, first.GenerationID).Scan(&currentPayloads); err != nil {
		t.Fatal(err)
	}
	if currentPayloads != 1 {
		t.Fatalf("building generation payloads = %d, want 1", currentPayloads)
	}

	previousID := first.GenerationID
	for i := 1; i <= 4; i++ {
		head := fmt.Sprintf("head-%d", i)
		next, err := st.BeginGraphGeneration(ctx, repoID, head, 1, 0, false, int64(i))
		if err != nil {
			t.Fatal(err)
		}
		var previousStatus string
		var previousPayloads, allPayloads int
		if err := pool.QueryRow(ctx, `SELECT status FROM graph_index_generations WHERE id = $1`, previousID).Scan(&previousStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files WHERE generation_id = $1`, previousID).Scan(&previousPayloads); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files f JOIN graph_index_generations g ON g.id = f.generation_id WHERE g.repo_id = $1`, repoID).Scan(&allPayloads); err != nil {
			t.Fatal(err)
		}
		if previousStatus != "superseded" || previousPayloads != 0 || allPayloads != 0 {
			t.Fatalf("head %d cleanup: previous status=%s payloads=%d all=%d", i, previousStatus, previousPayloads, allPayloads)
		}
		if _, err := st.StageGraphGenerationFile(ctx, repoID, next.GenerationID, "a.go", []byte("[]"), []byte("[]"), []byte("[]"), nil, false); err != nil {
			t.Fatal(err)
		}
		previousID = next.GenerationID
	}

	if err := st.FailGraphGeneration(ctx, repoID, previousID, errors.New("terminal test")); err != nil {
		t.Fatal(err)
	}
	var failedPayloads int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files WHERE generation_id = $1`, previousID).Scan(&failedPayloads); err != nil {
		t.Fatal(err)
	}
	if failedPayloads != 0 {
		t.Fatalf("failed generation retained %d payloads, want 0", failedPayloads)
	}
}

func TestFullAndIncrementalQualifiedMissResolutionParity(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/qualified-miss-parity")

	defs := []Symbol{{Kind: KindMethod, Name: "Alpha.Handle", Receiver: "Alpha", FilePath: "defs.go", LineStart: 1, LineEnd: 1}}
	caller := []Symbol{{Kind: KindFunction, Name: "Caller", FilePath: "caller.go", LineStart: 1, LineEnd: 1}}
	callerEdges := []Edge{{SourceName: "Caller", TargetName: "Missing.Handle", Kind: EdgeCalls}}
	snapshot, err := st.BeginGraphGeneration(ctx, repoID, "qualified-miss-head", 2, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	stage := func(filePath string, symbols []Symbol, edges []Edge) {
		t.Helper()
		symbolJSON, err := json.Marshal(symbols)
		if err != nil {
			t.Fatal(err)
		}
		edgeJSON, err := json.Marshal(edges)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.StageGraphGenerationFile(ctx, repoID, snapshot.GenerationID, filePath, symbolJSON, edgeJSON, []byte("[]"), nil, false); err != nil {
			t.Fatal(err)
		}
	}
	stage("defs.go", defs, nil)
	stage("caller.go", caller, callerEdges)
	if err := publishGraphGeneration(ctx, st, repoID, snapshot.GenerationID); err != nil {
		t.Fatal(err)
	}

	assertUnresolved := func(phase string) {
		t.Helper()
		var unresolved, wrongConcrete int
		if err := pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE target.name = 'unresolved:Missing.Handle')::int,
			count(*) FILTER (WHERE target.name = 'Alpha.Handle')::int
			FROM code_edges edge
			JOIN code_nodes source ON source.id = edge.source_id
			JOIN code_nodes target ON target.id = edge.target_id
			WHERE edge.repo_id = $1 AND source.name = 'Caller' AND edge.kind = 'calls'`, repoID).
			Scan(&unresolved, &wrongConcrete); err != nil {
			t.Fatal(err)
		}
		if unresolved != 1 || wrongConcrete != 0 {
			t.Fatalf("%s resolution: unresolved=%d concrete_alias=%d, want 1/0", phase, unresolved, wrongConcrete)
		}
	}
	assertUnresolved("full")

	caller[0].LineEnd = 2 // force the incremental hash/update path
	if err := indexParsedSymbols(ctx, st, repoID, map[string]fileResult{
		"caller.go": {symbols: caller, edges: callerEdges},
	}); err != nil {
		t.Fatal(err)
	}
	assertUnresolved("incremental")
}

func TestParserDuplicateGoMethodsPublishDistinctQualifiedNodes(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/duplicate-methods")
	const filePath = "handlers.go"
	const source = `package duplicate
	type Alpha struct{}
	type Beta struct{}
	func AlphaDone() {}
	func BetaDone() {}
	func (Alpha) Handle() { AlphaDone() }
	func (Beta) Handle() { BetaDone() }
	func Caller() { Alpha{}.Handle(); Beta{}.Handle(); Handle(); Missing{}.Handle() }`

	symbols, edges := ParseFileSymbols(filePath, source)
	symbols = append(symbols, fileSymbol(filePath, source))
	symbolJSON, err := json.Marshal(symbols)
	if err != nil {
		t.Fatal(err)
	}
	edgeJSON, err := json.Marshal(edges)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.BeginGraphGeneration(ctx, repoID, "duplicate-method-head", 1, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StageGraphGenerationFile(ctx, repoID, snapshot.GenerationID, filePath, symbolJSON, edgeJSON, []byte("[]"), nil, false); err != nil {
		t.Fatal(err)
	}
	if err := publishGraphGeneration(ctx, st, repoID, snapshot.GenerationID); err != nil {
		t.Fatalf("publish parser fixture with legal duplicate method names: %v", err)
	}

	var status string
	var methodCount, qualifiedCallerCalls, qualifiedSourceCalls, ambiguousCalls, unresolvedQualifiedCalls, buildingCount, stagedPayloads int
	if err := pool.QueryRow(ctx, `SELECT status FROM graph_index_generations WHERE id = $1`, snapshot.GenerationID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_nodes WHERE repo_id = $1 AND file_path = $2 AND kind = 'method' AND name IN ('Alpha.Handle', 'Beta.Handle')`, repoID, filePath).Scan(&methodCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_edges e
		JOIN code_nodes source ON source.id = e.source_id
		JOIN code_nodes target ON target.id = e.target_id
		WHERE e.repo_id = $1 AND source.name = 'Caller'
		  AND target.name IN ('Alpha.Handle', 'Beta.Handle') AND e.kind = 'calls'`, repoID).Scan(&qualifiedCallerCalls); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_edges e
		JOIN code_nodes source ON source.id = e.source_id
		JOIN code_nodes target ON target.id = e.target_id
		WHERE e.repo_id = $1 AND ((source.name = 'Alpha.Handle' AND target.name = 'AlphaDone')
		  OR (source.name = 'Beta.Handle' AND target.name = 'BetaDone')) AND e.kind = 'calls'`, repoID).Scan(&qualifiedSourceCalls); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_edges e
		JOIN code_nodes source ON source.id = e.source_id
		JOIN code_nodes target ON target.id = e.target_id
		WHERE e.repo_id = $1 AND source.name = 'Caller' AND target.name = 'ambiguous:Handle' AND e.kind = 'calls'`, repoID).Scan(&ambiguousCalls); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_edges e
		JOIN code_nodes source ON source.id = e.source_id
		JOIN code_nodes target ON target.id = e.target_id
		WHERE e.repo_id = $1 AND source.name = 'Caller'
		  AND target.name = 'unresolved:Missing.Handle' AND e.kind = 'calls'`, repoID).Scan(&unresolvedQualifiedCalls); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM graph_index_generations WHERE repo_id = $1 AND status = 'building'`, repoID).Scan(&buildingCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM graph_index_generation_files WHERE generation_id = $1`, snapshot.GenerationID).Scan(&stagedPayloads); err != nil {
		t.Fatal(err)
	}
	if status != "published" || methodCount != 2 || qualifiedCallerCalls != 2 || qualifiedSourceCalls != 2 || ambiguousCalls != 1 || unresolvedQualifiedCalls != 1 || buildingCount != 0 || stagedPayloads != 0 {
		t.Fatalf("published duplicate fixture: status=%s methods=%d caller_calls=%d source_calls=%d ambiguous=%d unresolved_qualified=%d building=%d staged=%d", status, methodCount, qualifiedCallerCalls, qualifiedSourceCalls, ambiguousCalls, unresolvedQualifiedCalls, buildingCount, stagedPayloads)
	}
}

func TestPublishGraphGenerationResourceLimitsAreTerminalAndAtomic(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	original := defaultGraphGenerationPublishLimits
	defaultGraphGenerationPublishLimits = graphGenerationPublishLimits{
		JSONBytes: 1 << 20, Files: 10, Symbols: 1, Edges: 10, Endpoints: 10,
		ResolutionNodes: 10, PublishedEdges: 10,
	}
	t.Cleanup(func() { defaultGraphGenerationPublishLimits = original })

	t.Run("exact bound publishes with small-graph parity", func(t *testing.T) {
		installationID := generationSeedInstallation(t, ctx, pool, "{}")
		repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/exact-resource-limit")
		snapshot, err := st.BeginGraphGeneration(ctx, repoID, "exact-head", 1, 0, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		symbolJSON, _ := json.Marshal([]Symbol{{Kind: "file", Name: "a.go", FilePath: "a.go", LineStart: 1, LineEnd: 1}})
		if _, err := st.StageGraphGenerationFile(ctx, repoID, snapshot.GenerationID, "a.go", symbolJSON, []byte("[]"), []byte("[]"), nil, false); err != nil {
			t.Fatal(err)
		}
		if err := publishGraphGeneration(ctx, st, repoID, snapshot.GenerationID); err != nil {
			t.Fatalf("publish at exact symbol bound: %v", err)
		}
		var status string
		var nodes int
		if err := pool.QueryRow(ctx, `SELECT status, (SELECT count(*)::int FROM code_nodes WHERE repo_id = $2)
			FROM graph_index_generations WHERE id = $1`, snapshot.GenerationID, repoID).Scan(&status, &nodes); err != nil {
			t.Fatal(err)
		}
		if status != "published" || nodes != 1 {
			t.Fatalf("exact-bound status/nodes = %s/%d, want published/1", status, nodes)
		}
	})

	t.Run("one over fails terminal without replacing published graph", func(t *testing.T) {
		installationID := generationSeedInstallation(t, ctx, pool, "{}")
		repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/over-resource-limit")
		if _, err := pool.Exec(ctx, `UPDATE repos SET enabled = true WHERE id = $1`, repoID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `UPDATE repos SET enabled = false WHERE id = $1`, repoID)
		})
		var oldGenerationID int64
		if err := pool.QueryRow(ctx, `INSERT INTO graph_index_generations
			(repo_id, commit_sha, status, expected_files, visited_files, published_at)
			VALUES ($1, 'old-head', 'published', 0, 0, NOW()) RETURNING id`, repoID).Scan(&oldGenerationID); err != nil {
			t.Fatal(err)
		}
		oldNodeID := generationSeedNode(t, ctx, pool, repoID, "Old", "old.go")
		if _, err := pool.Exec(ctx, `UPDATE repos SET graph_published_generation_id = $2,
			graph_refresh_requested_at = NOW(), graph_refresh_commit_sha = 'large-head',
			graph_default_head_sha = 'large-head' WHERE id = $1`, repoID, oldGenerationID); err != nil {
			t.Fatal(err)
		}

		snapshot, err := st.BeginGraphGeneration(ctx, repoID, "large-head", 1, 0, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		symbolJSON, _ := json.Marshal([]Symbol{
			{Kind: "file", Name: "large.go", FilePath: "large.go"},
			{Kind: "function", Name: "TooMuch", FilePath: "large.go"},
		})
		if _, err := st.StageGraphGenerationFile(ctx, repoID, snapshot.GenerationID, "large.go", symbolJSON, []byte("[]"), []byte("[]"), nil, false); err != nil {
			t.Fatal(err)
		}
		err = publishGraphGeneration(ctx, st, repoID, snapshot.GenerationID)
		if !errors.Is(err, ErrGraphGenerationResourceLimit) {
			t.Fatalf("publish error = %v, want resource limit", err)
		}
		var status string
		var publishedID *int64
		var oldNodes, newNodes int
		if err := pool.QueryRow(ctx, `SELECT g.status, r.graph_published_generation_id,
			(SELECT count(*)::int FROM code_nodes WHERE id = $3),
			(SELECT count(*)::int FROM code_nodes WHERE repo_id = $2 AND file_path = 'large.go')
			FROM graph_index_generations g JOIN repos r ON r.id = g.repo_id
			WHERE g.id = $1`, snapshot.GenerationID, repoID, oldNodeID).
			Scan(&status, &publishedID, &oldNodes, &newNodes); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || publishedID == nil || *publishedID != oldGenerationID || oldNodes != 1 || newNodes != 0 {
			t.Fatalf("terminal publication = status %s published %v old/new nodes %d/%d", status, publishedID, oldNodes, newNodes)
		}
		var stagedPayloads int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM graph_index_generation_files WHERE generation_id = $1`, snapshot.GenerationID).Scan(&stagedPayloads); err != nil {
			t.Fatal(err)
		}
		if stagedPayloads != 0 {
			t.Fatalf("preflight-failed generation retained %d staged payloads, want 0", stagedPayloads)
		}

		prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
		if err != nil {
			t.Fatal(err)
		}
		backfill, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
		if err != nil {
			t.Fatal(err)
		}
		for _, targets := range [][]store.RepoIndexTarget{prompt, backfill} {
			for _, target := range targets {
				if target.RepoID == repoID {
					t.Fatal("oversized immutable generation remained immediately retryable")
				}
			}
		}
	})

	t.Run("dynamic resolution limit failure removes staging atomically", func(t *testing.T) {
		limits := defaultGraphGenerationPublishLimits
		defaultGraphGenerationPublishLimits.ResolutionNodes = 1
		defer func() { defaultGraphGenerationPublishLimits = limits }()
		installationID := generationSeedInstallation(t, ctx, pool, "{}")
		repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/dynamic-resource-limit")
		snapshot, err := st.BeginGraphGeneration(ctx, repoID, "dynamic-head", 1, 0, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		symbolJSON, _ := json.Marshal([]Symbol{{Kind: "file", Name: "a.go", FilePath: "a.go"}})
		edgeJSON, _ := json.Marshal([]Edge{{SourceName: "a.go", TargetName: "Missing", Kind: "calls"}})
		if _, err := st.StageGraphGenerationFile(ctx, repoID, snapshot.GenerationID, "a.go", symbolJSON, edgeJSON, []byte("[]"), nil, false); err != nil {
			t.Fatal(err)
		}
		err = publishGraphGeneration(ctx, st, repoID, snapshot.GenerationID)
		if !errors.Is(err, ErrGraphGenerationResourceLimit) {
			t.Fatalf("publish error = %v, want dynamic resource limit", err)
		}
		var status string
		var nodes, stagedPayloads int
		if err := pool.QueryRow(ctx, `SELECT status FROM graph_index_generations WHERE id = $1`, snapshot.GenerationID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM code_nodes WHERE repo_id = $1`, repoID).Scan(&nodes); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM graph_index_generation_files WHERE generation_id = $1`, snapshot.GenerationID).Scan(&stagedPayloads); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || nodes != 0 || stagedPayloads != 0 {
			t.Fatalf("dynamic limit cleanup: status=%s nodes=%d staged=%d", status, nodes, stagedPayloads)
		}
	})
}

func TestForEachStagedGraphFileFetchesBoundedBatches(t *testing.T) {
	pool, ctx := generationTestPool(t)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/batched-cursor")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE repos SET enabled = false WHERE id = $1`, repoID)
	})

	const fileCount = stagedGraphFileFetchBatchSize*2 + 7
	var generationID int64
	if err := pool.QueryRow(ctx, `INSERT INTO graph_index_generations
		(repo_id, commit_sha, status, expected_files, visited_files)
		VALUES ($1, 'batched-cursor-head', 'building', $2, $2) RETURNING id`, repoID, fileCount).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO graph_index_generation_files
		(generation_id, file_path, status, symbols, edges, endpoints)
		SELECT $1, 'file-' || lpad(n::text, 4, '0') || '.go', 'ready', '[]'::jsonb, '[]'::jsonb, '[]'::jsonb
		FROM generate_series(1, $2) AS n`, generationID, fileCount); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	paths := make([]string, 0, fileCount)
	stats, err := forEachStagedGraphFile(ctx, tx, generationID, "graph_batch_test", func(file stagedGraphFile) error {
		if len(paths) == 0 {
			var one int
			if err := tx.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
				return fmt.Errorf("query inside callback: %w", err)
			}
			if one != 1 {
				return fmt.Errorf("query inside callback = %d, want 1", one)
			}
		}
		paths = append(paths, file.FilePath)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != fileCount {
		t.Fatalf("visited files = %d, want %d", stats.Files, fileCount)
	}
	const wantFetches = 4 // three populated batches plus the terminating empty fetch.
	if stats.Fetches != wantFetches {
		t.Fatalf("FETCH statements = %d, want %d", stats.Fetches, wantFetches)
	}
	for i, path := range paths {
		want := fmt.Sprintf("file-%04d.go", i+1)
		if path != want {
			t.Fatalf("path %d = %q, want %q", i, path, want)
		}
	}

	// The cursor must be closed before the helper returns, leaving the pgx
	// transaction protocol ready for the publication pass's next statement.
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query after batched cursor = %d, %v", one, err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	callbacks := 0
	canceledStats, err := forEachStagedGraphFile(cancelCtx, tx, generationID, "graph_batch_cancel_test", func(stagedGraphFile) error {
		callbacks++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled iteration error = %v, want context.Canceled", err)
	}
	if callbacks != 1 || canceledStats.Files != 1 || canceledStats.Fetches != 1 {
		t.Fatalf("canceled iteration callbacks/files/fetches = %d/%d/%d, want 1/1/1", callbacks, canceledStats.Files, canceledStats.Fetches)
	}
	if err := tx.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query after canceled cursor = %d, %v", one, err)
	}
}

func TestListReposDueForGraphIndexSelectsLegacyRecentTimestampWithoutPublishedAuthority(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "upgrade/legacy-recent")
	generationSeedNode(t, ctx, pool, repoID, "LegacyGraphRow", "legacy.go")
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET enabled = true, graph_indexed_at = NOW(), graph_published_generation_id = NULL
		WHERE id = $1`, repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `UPDATE repos SET enabled = false WHERE id = $1`, repoID) })

	targets, err := st.ListReposDueForGraphIndex(ctx, 14*24*time.Hour, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !containsGraphTarget(targets, repoID, "main") {
		t.Fatalf("legacy repo with recent timestamp and null generation authority not immediately due: %+v", targets)
	}
}
