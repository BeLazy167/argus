package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/jackc/pgx/v5/pgxpool"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/graph"
	"github.com/BeLazy167/argus/backend/internal/store"
)

type renamedDefaultBranchGitHub struct {
	commitSHA     string
	repoID        int64
	defaultBranch string
	metadataErr   error
	metadataCalls int
	headCalls     int
	treeRef       string
	fileRefs      []string
}

func (f *renamedDefaultBranchGitHub) GetRepositoryMetadata(_ context.Context, _ int64, owner, repo string) (ghpkg.RepositoryMetadata, error) {
	f.metadataCalls++
	if f.metadataErr != nil {
		return ghpkg.RepositoryMetadata{}, f.metadataErr
	}
	return ghpkg.RepositoryMetadata{ID: f.repoID, FullName: owner + "/" + repo, DefaultBranch: f.defaultBranch}, nil
}

func (f *renamedDefaultBranchGitHub) ResolveDefaultBranchCommit(context.Context, int64, string, string, string) (string, error) {
	f.headCalls++
	return f.commitSHA, nil
}

func (f *renamedDefaultBranchGitHub) GetRepoTree(_ context.Context, _ int64, _, _, ref string) (ghpkg.RepoTree, error) {
	f.treeRef = ref
	return ghpkg.RepoTree{Files: []ghpkg.RepoTreeFile{{Path: "b.go", SHA: "blob-b"}, {Path: "c.go", SHA: "blob-c"}}}, nil
}

func (f *renamedDefaultBranchGitHub) GetBlobContent(_ context.Context, _ int64, _, _ string, file ghpkg.RepoTreeFile) (string, error) {
	f.fileRefs = append(f.fileRefs, file.SHA)
	return "package renamed\nfunc " + map[string]string{"b.go": "TrunkB", "c.go": "TrunkC"}[file.Path] + "() {}\n", nil
}

type interleavedDefaultHeadGitHub struct {
	mu            sync.Mutex
	repoID        int64
	fullName      string
	currentHead   string
	blockedHead   string
	headResolved  chan struct{}
	releaseResult chan struct{}
	signalOnce    sync.Once
}

func (f *interleavedDefaultHeadGitHub) GetRepositoryMetadata(context.Context, int64, string, string) (ghpkg.RepositoryMetadata, error) {
	return ghpkg.RepositoryMetadata{ID: f.repoID, FullName: f.fullName, DefaultBranch: "main"}, nil
}

func (f *interleavedDefaultHeadGitHub) ResolveDefaultBranchCommit(context.Context, int64, string, string, string) (string, error) {
	f.mu.Lock()
	head := f.currentHead
	f.mu.Unlock()
	if head == f.blockedHead {
		f.signalOnce.Do(func() { close(f.headResolved) })
		<-f.releaseResult
	}
	return head, nil
}

func (f *interleavedDefaultHeadGitHub) setCurrentHead(head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.currentHead = head
}

func webhookGraphTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
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

func TestVerifiedEqualTimeHeadCannotOverwriteNewerVerifiedHead(t *testing.T) {
	pool, ctx := webhookGraphTestPool(t)
	st := store.NewWithDB(pool)

	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	fullName := "interleaved-head/repo-" + unique
	var installationDBID, repoID int64
	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, $1) RETURNING id, installation_id`, "interleaved-head-"+unique).
		Scan(&installationDBID, &githubInstallationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name, default_branch, enabled)
		VALUES ($1, (random()*1000000000)::bigint, $2, 'main', true) RETURNING id, github_id`, installationDBID, fullName).
		Scan(&repoID, &githubRepoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, installationDBID)
	})

	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb7"
	const commitC = "ccccccccccccccccccccccccccccccccccccccc8"
	const commitD = "ddddddddddddddddddddddddddddddddddddddd9"
	observedAt := time.Now().UTC().Truncate(time.Second)
	pushUpdate := func(commitSHA string) ghpkg.DefaultBranchUpdate {
		push := &gh.PushEvent{
			After: gh.Ptr(commitSHA), Ref: gh.Ptr("refs/heads/main"),
			Installation: &gh.Installation{ID: gh.Ptr(githubInstallationID)},
			Repo: &gh.PushEventRepository{
				ID: gh.Ptr(githubRepoID), FullName: gh.Ptr(fullName), DefaultBranch: gh.Ptr("main"),
				PushedAt: &gh.Timestamp{Time: observedAt},
			},
		}
		update, ok := ghpkg.DefaultBranchUpdateFromPush(&ghpkg.WebhookEvent{Type: "push", Payload: push})
		if !ok {
			t.Fatalf("valid default-head push %s was rejected", commitSHA)
		}
		return update
	}

	metadataClient := &interleavedDefaultHeadGitHub{
		repoID: githubRepoID, fullName: fullName, currentHead: commitB,
		blockedHead: commitC, headResolved: make(chan struct{}), releaseResult: make(chan struct{}),
	}
	server := &Server{
		store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoMetadata: metadataClient,
	}
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitB)); err != nil {
		t.Fatalf("schedule B: %v", err)
	}

	metadataClient.setCurrentHead(commitC)
	_, conflictErr := st.ScheduleGraphIndexRefreshFromPush(ctx, githubInstallationID, githubRepoID,
		fullName, "main", commitC, observedAt)
	if !errors.Is(conflictErr, store.ErrGraphDefaultBranchMismatch) {
		t.Fatalf("conflict error %v does not unwrap to ErrGraphDefaultBranchMismatch", conflictErr)
	}
	var conflict *store.GraphDefaultBranchMismatchError
	if !errors.As(conflictErr, &conflict) {
		t.Fatalf("conflict error type = %T, want *store.GraphDefaultBranchMismatchError", conflictErr)
	}

	cResult := make(chan error, 1)
	go func() { cResult <- server.scheduleGraphRefresh(ctx, pushUpdate(commitC)) }()
	select {
	case <-metadataClient.headResolved:
	case <-ctx.Done():
		t.Fatalf("C live-head verification did not resolve: %v", ctx.Err())
	}

	// C has read a live C head, but has not retried the database mutation. D
	// wins a complete conflict -> live verification -> verified retry cycle.
	metadataClient.setCurrentHead(commitD)
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitD)); err != nil {
		t.Fatalf("schedule interleaved D: %v", err)
	}
	close(metadataClient.releaseResult)
	if err := <-cResult; err != nil {
		t.Fatalf("finish stale verified C: %v", err)
	}

	var head string
	var requestedCommit *string
	var version int64
	if err := pool.QueryRow(ctx, `
		SELECT graph_default_head_sha, graph_refresh_commit_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).Scan(&head, &requestedCommit, &version); err != nil {
		t.Fatal(err)
	}
	if head != commitD || requestedCommit == nil || *requestedCommit != commitD || version != 2 {
		t.Fatalf("stale verified C overwrote D: head=%q requested=%v version=%d", head, requestedCommit, version)
	}
}

func TestVerifiedEqualTimeHeadCannotOverwriteWorkerConfirmedHead(t *testing.T) {
	pool, ctx := webhookGraphTestPool(t)
	st := store.NewWithDB(pool)

	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	fullName := "interleaved-worker-head/repo-" + unique
	var installationDBID, repoID int64
	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, $1) RETURNING id, installation_id`, "interleaved-worker-head-"+unique).
		Scan(&installationDBID, &githubInstallationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name, default_branch, enabled)
		VALUES ($1, (random()*1000000000)::bigint, $2, 'main', true) RETURNING id, github_id`, installationDBID, fullName).
		Scan(&repoID, &githubRepoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, installationDBID)
	})

	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1"
	const commitC = "ccccccccccccccccccccccccccccccccccccccc2"
	const commitD = "ddddddddddddddddddddddddddddddddddddddd3"
	observedAt := time.Now().UTC().Truncate(time.Second)
	pushUpdate := func(commitSHA string) ghpkg.DefaultBranchUpdate {
		push := &gh.PushEvent{
			After: gh.Ptr(commitSHA), Ref: gh.Ptr("refs/heads/main"),
			Installation: &gh.Installation{ID: gh.Ptr(githubInstallationID)},
			Repo: &gh.PushEventRepository{
				ID: gh.Ptr(githubRepoID), FullName: gh.Ptr(fullName), DefaultBranch: gh.Ptr("main"),
				PushedAt: &gh.Timestamp{Time: observedAt},
			},
		}
		update, ok := ghpkg.DefaultBranchUpdateFromPush(&ghpkg.WebhookEvent{Type: "push", Payload: push})
		if !ok {
			t.Fatalf("valid default-head push %s was rejected", commitSHA)
		}
		return update
	}

	metadataClient := &interleavedDefaultHeadGitHub{
		repoID: githubRepoID, fullName: fullName, currentHead: commitB,
		blockedHead: commitC, headResolved: make(chan struct{}), releaseResult: make(chan struct{}),
	}
	releaseResult := sync.OnceFunc(func() { close(metadataClient.releaseResult) })
	t.Cleanup(releaseResult)
	server := &Server{
		store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoMetadata: metadataClient,
	}
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitB)); err != nil {
		t.Fatalf("schedule B: %v", err)
	}

	metadataClient.setCurrentHead(commitC)
	cResult := make(chan error, 1)
	go func() { cResult <- server.scheduleGraphRefresh(ctx, pushUpdate(commitC)) }()
	select {
	case <-metadataClient.headResolved:
	case <-ctx.Done():
		t.Fatalf("C live-head verification did not resolve: %v", ctx.Err())
	}

	// C has read a live C head but has not retried the database mutation. The
	// worker then resolves newer D against the same refresh version and confirms
	// it before C returns its stale conflict token.
	alreadyCurrent, confirmed, err := st.ConfirmGraphDefaultHead(ctx, repoID, 1, commitD)
	if err != nil {
		t.Fatalf("worker confirm D: %v", err)
	}
	if alreadyCurrent || !confirmed {
		t.Fatalf("worker confirm D = already current %v, confirmed %v", alreadyCurrent, confirmed)
	}
	releaseResult()
	if err := <-cResult; err != nil {
		t.Fatalf("finish stale verified C: %v", err)
	}

	var head string
	var requestedCommit *string
	var version int64
	if err := pool.QueryRow(ctx, `
		SELECT graph_default_head_sha, graph_refresh_commit_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).Scan(&head, &requestedCommit, &version); err != nil {
		t.Fatal(err)
	}
	if head != commitD || requestedCommit == nil || *requestedCommit != commitD || version != 1 {
		t.Fatalf("stale verified C overwrote worker-confirmed D: head=%q requested=%v version=%d", head, requestedCommit, version)
	}
}

func TestEqualTimeConflictingDefaultHeadPushesUseLiveHeadAuthority(t *testing.T) {
	pool, ctx := webhookGraphTestPool(t)
	st := store.NewWithDB(pool)
	metadataClient := &renamedDefaultBranchGitHub{defaultBranch: "main"}
	server := &Server{
		store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoMetadata: metadataClient,
	}

	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	fullName := "equal-head/repo-" + unique
	var installationDBID, repoID int64
	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, $1) RETURNING id, installation_id`, "equal-head-"+unique).
		Scan(&installationDBID, &githubInstallationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name, default_branch, enabled)
		VALUES ($1, (random()*1000000000)::bigint, $2, 'main', true) RETURNING id, github_id`, installationDBID, fullName).
		Scan(&repoID, &githubRepoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	metadataClient.repoID = githubRepoID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, installationDBID)
	})

	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb5"
	const commitC = "ccccccccccccccccccccccccccccccccccccccc6"
	observedAt := time.Now().UTC().Truncate(time.Second)
	pushUpdate := func(commitSHA string) ghpkg.DefaultBranchUpdate {
		push := &gh.PushEvent{
			After: gh.Ptr(commitSHA), Ref: gh.Ptr("refs/heads/main"),
			Installation: &gh.Installation{ID: gh.Ptr(githubInstallationID)},
			Repo: &gh.PushEventRepository{
				ID: gh.Ptr(githubRepoID), FullName: gh.Ptr(fullName), DefaultBranch: gh.Ptr("main"),
				PushedAt: &gh.Timestamp{Time: observedAt},
			},
		}
		update, ok := ghpkg.DefaultBranchUpdateFromPush(&ghpkg.WebhookEvent{Type: "push", Payload: push})
		if !ok {
			t.Fatalf("valid default-head push %s was rejected", commitSHA)
		}
		return update
	}
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitB)); err != nil {
		t.Fatalf("schedule B: %v", err)
	}

	// GitHub now names C as the current main head. The genuine C delivery and
	// the delayed B delivery have the same second-precision pushed_at value, so
	// delivery order cannot arbitrate them.
	metadataClient.commitSHA = commitC
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitC)); err != nil {
		t.Fatalf("schedule live C: %v", err)
	}
	if err := server.scheduleGraphRefresh(ctx, pushUpdate(commitB)); err != nil {
		t.Fatalf("schedule delayed B: %v", err)
	}

	var head string
	var requestedCommit *string
	var version int64
	if err := pool.QueryRow(ctx, `
		SELECT graph_default_head_sha, graph_refresh_commit_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).Scan(&head, &requestedCommit, &version); err != nil {
		t.Fatal(err)
	}
	if head != commitC || requestedCommit == nil || *requestedCommit != commitC || version != 2 {
		t.Fatalf("equal-time authority regressed: head=%q requested=%v version=%d", head, requestedCommit, version)
	}
	if metadataClient.metadataCalls != 2 || metadataClient.headCalls != 2 {
		t.Fatalf("live authority lookups = metadata %d head %d, want C arbitration and delayed B rejection",
			metadataClient.metadataCalls, metadataClient.headCalls)
	}
}

func TestDefaultBranchRenamePushPersistsAuthorityAndPublishesImmutableRefresh(t *testing.T) {
	pool, ctx := webhookGraphTestPool(t)
	st := store.NewWithDB(pool)
	metadataClient := &renamedDefaultBranchGitHub{defaultBranch: "trunk"}
	server := &Server{
		store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		repoMetadata: metadataClient,
	}

	unique := strconv.FormatInt(time.Now().UnixNano(), 10)
	fullName := "rename-push/repo-" + unique
	var installationDBID, repoID int64
	var githubInstallationID, githubRepoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, $1) RETURNING id, installation_id`, "rename-push-"+unique).
		Scan(&installationDBID, &githubInstallationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name, default_branch, enabled)
		VALUES ($1, (random()*1000000000)::bigint, $2, 'main', true) RETURNING id, github_id`, installationDBID, fullName).
		Scan(&repoID, &githubRepoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	metadataClient.repoID = githubRepoID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, installationDBID)
	})

	const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa5"
	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb6"
	observedA := time.Now().UTC().Add(-time.Hour)
	observedB := observedA // GitHub records pushed_at at second precision; rename deliveries can tie.
	var generationA int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO graph_index_generations
		  (repo_id, commit_sha, status, expected_files, visited_files, refresh_version, published_at)
		VALUES ($1, $2, 'published', 1, 1, 0, $3) RETURNING id`, repoID, commitA, observedA).
		Scan(&generationA); err != nil {
		t.Fatalf("seed generation A: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE repos SET graph_published_generation_id = $2, graph_indexed_at = $3,
		  graph_index_commit_sha = $4, graph_default_head_sha = $4,
		  graph_default_head_observed_at = $3, graph_default_head_event_at = $3
		WHERE id = $1`, repoID, generationA, observedA, commitA); err != nil {
		t.Fatalf("mark main A current: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO code_nodes
		  (repo_id, installation_id, kind, name, file_path, line_start, line_end, language)
		VALUES ($1, $2, 'function', 'PublishedMainA', 'a.go', 1, 2, 'go')`, repoID, installationDBID); err != nil {
		t.Fatalf("seed published A topology: %v", err)
	}

	push := &gh.PushEvent{
		After: gh.Ptr(commitB), Ref: gh.Ptr("refs/heads/trunk"),
		Installation: &gh.Installation{ID: gh.Ptr(githubInstallationID)},
		Repo: &gh.PushEventRepository{
			ID: gh.Ptr(githubRepoID), FullName: gh.Ptr(fullName), DefaultBranch: gh.Ptr("trunk"),
			PushedAt: &gh.Timestamp{Time: observedB},
		},
	}
	update, ok := ghpkg.DefaultBranchUpdateFromPush(&ghpkg.WebhookEvent{Type: "push", Payload: push})
	if !ok {
		t.Fatal("valid renamed-default-branch push was rejected")
	}
	// This is the production scheduling seam used by handleWebhook. In
	// particular, no installation sync or UpsertRepo runs before it.
	for name, mutate := range map[string]func(*ghpkg.DefaultBranchUpdate){
		"installation": func(candidate *ghpkg.DefaultBranchUpdate) { candidate.InstallationID++ },
		"repository":   func(candidate *ghpkg.DefaultBranchUpdate) { candidate.RepoID++ },
		"full name":    func(candidate *ghpkg.DefaultBranchUpdate) { candidate.RepoFullName += "-other" },
	} {
		t.Run("rejects wrong "+name, func(t *testing.T) {
			candidate := update
			mutate(&candidate)
			if err := server.scheduleGraphRefresh(ctx, candidate); err != nil {
				t.Fatalf("schedule wrongly scoped push: %v", err)
			}
			var branch, head string
			var version int64
			if err := pool.QueryRow(ctx, `SELECT default_branch, graph_default_head_sha, graph_refresh_version FROM repos WHERE id = $1`, repoID).
				Scan(&branch, &head, &version); err != nil {
				t.Fatal(err)
			}
			if branch != "main" || head != commitA || version != 0 {
				t.Fatalf("wrongly scoped push mutated repo: branch=%q head=%q version=%d", branch, head, version)
			}
		})
	}
	metadataClient.metadataErr = errors.New("repository metadata unavailable")
	if err := server.scheduleGraphRefresh(ctx, update); !errors.Is(err, metadataClient.metadataErr) {
		t.Fatalf("metadata failure = %v, want lookup error", err)
	}
	var failedBranch, failedHead string
	var failedVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT default_branch, graph_default_head_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).Scan(&failedBranch, &failedHead, &failedVersion); err != nil {
		t.Fatal(err)
	}
	if failedBranch != "main" || failedHead != commitA || failedVersion != 0 {
		t.Fatalf("metadata failure mutated repo: branch=%q head=%q version=%d", failedBranch, failedHead, failedVersion)
	}
	metadataClient.metadataErr = nil
	metadataClient.commitSHA = commitB
	if err := server.scheduleGraphRefresh(ctx, update); err != nil {
		t.Fatalf("schedule renamed-default-branch push: %v", err)
	}

	var storedBranch string
	var storedHead, requestedCommit *string
	var requestedAt *time.Time
	var refreshVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT default_branch, graph_default_head_sha, graph_refresh_requested_at,
		       graph_refresh_commit_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).
		Scan(&storedBranch, &storedHead, &requestedAt, &requestedCommit, &refreshVersion); err != nil {
		t.Fatal(err)
	}
	if storedBranch != "trunk" || storedHead == nil || *storedHead != commitB ||
		requestedAt == nil || requestedCommit == nil || *requestedCommit != commitB || refreshVersion != 1 {
		t.Fatalf("renamed push state = branch %q head %v requested %v commit %v version %d",
			storedBranch, storedHead, requestedAt, requestedCommit, refreshVersion)
	}
	duplicate := update
	duplicate.ObservedAt = observedB.Add(time.Minute)
	if err := server.scheduleGraphRefresh(ctx, duplicate); err != nil {
		t.Fatalf("schedule duplicate trunk B: %v", err)
	}
	delayedMainPush := &gh.PushEvent{
		After: gh.Ptr(commitA), Ref: gh.Ptr("refs/heads/main"),
		Installation: &gh.Installation{ID: gh.Ptr(githubInstallationID)},
		Repo: &gh.PushEventRepository{
			ID: gh.Ptr(githubRepoID), FullName: gh.Ptr(fullName), DefaultBranch: gh.Ptr("main"),
			PushedAt: &gh.Timestamp{Time: observedA},
		},
	}
	delayedMain, ok := ghpkg.DefaultBranchUpdateFromPush(&ghpkg.WebhookEvent{Type: "push", Payload: delayedMainPush})
	if !ok {
		t.Fatal("delayed main push fixture was rejected")
	}
	if err := server.scheduleGraphRefresh(ctx, delayedMain); err != nil {
		t.Fatalf("schedule delayed main A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT default_branch, graph_default_head_sha, graph_refresh_version
		FROM repos WHERE id = $1`, repoID).Scan(&storedBranch, &storedHead, &refreshVersion); err != nil {
		t.Fatal(err)
	}
	if storedBranch != "trunk" || storedHead == nil || *storedHead != commitB || refreshVersion != 1 {
		t.Fatalf("equal-time delayed push regressed authority: branch=%q head=%v version=%d",
			storedBranch, storedHead, refreshVersion)
	}
	if metadataClient.metadataCalls != 3 {
		t.Fatalf("live default-branch metadata lookups = %d, want failed rename, accepted rename, and delayed mismatch", metadataClient.metadataCalls)
	}
	snapshot, err := st.GetGraphSnapshot(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current || snapshot.PublishedCommitSHA != commitA || snapshot.DefaultHeadSHA != commitB || snapshot.RefreshRequestedAt == nil {
		t.Fatalf("freshness after renamed push = %+v", snapshot)
	}
	prompt, err := st.ListReposDueForPromptGraphIndex(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range prompt {
		found = found || target.RepoID == repoID && target.DefaultBranch == "trunk"
	}
	if !found {
		t.Fatalf("prompt queue omitted trunk target %d: %+v", repoID, prompt)
	}

	metadataClient.commitSHA = commitB
	fake := metadataClient
	owner, repo := "rename-push", "repo-"+unique
	first, err := graph.IndexRepoBounded(ctx, st, fake, githubInstallationID, owner, repo, "trunk", repoID, 1, 0)
	if err != nil || first.Published {
		t.Fatalf("stage trunk B = %+v, err=%v", first, err)
	}
	var publishedACount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM code_nodes WHERE repo_id = $1 AND name = 'PublishedMainA'`, repoID).Scan(&publishedACount); err != nil {
		t.Fatal(err)
	}
	if publishedACount != 1 {
		t.Fatal("partial trunk B replaced published main A")
	}
	second, err := graph.IndexRepoBounded(ctx, st, fake, githubInstallationID, owner, repo, "trunk", repoID, 1, 0)
	if err != nil || !second.Published || !second.Snapshot.Current || second.Snapshot.PublishedCommitSHA != commitB {
		t.Fatalf("publish trunk B = %+v, err=%v", second, err)
	}
	if fake.treeRef != commitB {
		t.Fatalf("tree ref = %q, want immutable B %q", fake.treeRef, commitB)
	}
	for _, sha := range fake.fileRefs {
		if sha != "blob-b" && sha != "blob-c" {
			t.Fatalf("blob SHA = %q, want immutable tree blob", sha)
		}
	}
	var mainA, trunkB int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE name = 'PublishedMainA'),
		       count(*) FILTER (WHERE name IN ('TrunkB', 'TrunkC'))
		FROM code_nodes WHERE repo_id = $1`, repoID).Scan(&mainA, &trunkB); err != nil {
		t.Fatal(err)
	}
	if mainA != 0 || trunkB != 2 {
		t.Fatalf("published topology after B: main A=%d trunk B=%d", mainA, trunkB)
	}
}
