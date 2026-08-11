package graph

import (
	"context"
	"errors"
	"net/http"
	"os"
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

func TestIndexRepoBoundedKeepsPinnedGenerationForTransientTreeFailure(t *testing.T) {
	pool, ctx := generationTestPool(t)
	st := store.NewWithDB(pool)
	installationID := generationSeedInstallation(t, ctx, pool, "{}")
	repoID := generationSeedRepo(t, ctx, pool, installationID, "generation/transient-tree")
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

	ghClient.treeErr = &gh.ErrorResponse{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}
	second, err := IndexRepoBounded(ctx, st, ghClient, 1, "o", "r", "main", repoID, 1, 0)
	if err == nil || ghpkg.IsPermanentGitObjectError(err) {
		t.Fatalf("tree error = %v, want transient error", err)
	}
	if second.Snapshot.GenerationID != first.Snapshot.GenerationID || second.Snapshot.Status != "building" {
		t.Fatalf("retryable generation = %+v, want same building generation", second)
	}
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
	if unavailable != 1 {
		t.Fatalf("unavailable files = %d, want 1", unavailable)
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
