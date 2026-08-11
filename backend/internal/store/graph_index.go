package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RepoIndexTarget is one repository due for a full code-graph index.
//
// It carries BOTH installation identifiers on purpose, because they are
// different numbers and mixing them fails silently:
//   - RepoID and DBInstallationID are local surrogate keys. repos.installation_id
//     is a foreign key to installations.id.
//   - GitHubInstallationID is installations.installation_id, the number GitHub
//     knows. Every GitHub API call needs THIS one.
//
// Passing the local id to the GitHub client authenticates as an installation
// that does not exist; passing the GitHub id to a repo query matches no rows and
// returns empty rather than erroring.
type RepoIndexTarget struct {
	RepoID               int64
	DBInstallationID     int64
	GitHubInstallationID int64
	Owner                string
	Repo                 string
	DefaultBranch        string
	// IndexCursor is where the last capped run stopped. Zero means start at the
	// beginning, which is also the right answer for a repo under the cap.
	IndexCursor int
}

// ListReposDueForGraphIndex returns enabled repos never fully indexed, or
// indexed longer ago than staleAfter, most overdue first.
//
// NULLS FIRST puts never-indexed repos ahead of merely stale ones, so a newly
// connected repository gets its first real graph before an existing one is
// refreshed — the first index is what takes a repo from PR-shaped fragments to
// a usable dependency graph, while a refresh only corrects drift.
//
// The ATTEMPT time is the first sort key, and it is what stops one building or
// broken repository from starving every other. A bounded generation remains
// immediately due, but stamping its attempt moves it behind every less-recently
// attempted due repository. Once all due repositories have had a turn, the
// oldest attempt is selected again, so continuations and transient failures are
// retried fairly.
func (s *Store) ListReposDueForGraphIndex(ctx context.Context, staleAfter time.Duration, limit int) ([]RepoIndexTarget, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, r.installation_id, i.installation_id, r.full_name, r.default_branch, r.graph_index_cursor
		FROM repos r
		JOIN installations i ON i.id = r.installation_id
		WHERE r.enabled
		  AND i.suspended_at IS NULL
		  AND (EXISTS (
		        SELECT 1 FROM graph_index_generations g
		        WHERE g.repo_id = r.id AND g.status = 'building'
		      ) OR r.graph_indexed_at IS NULL OR r.graph_indexed_at < NOW() - $1::interval)
		ORDER BY r.graph_index_attempted_at NULLS FIRST,
		         r.graph_indexed_at NULLS FIRST, r.id
		LIMIT $2`,
		fmt.Sprintf("%d seconds", int64(staleAfter.Seconds())), limit)
	if err != nil {
		return nil, fmt.Errorf("listing repos due for graph index: %w", err)
	}
	defer rows.Close()

	var out []RepoIndexTarget
	for rows.Next() {
		var t RepoIndexTarget
		var fullName string
		if err := rows.Scan(&t.RepoID, &t.DBInstallationID, &t.GitHubInstallationID,
			&fullName, &t.DefaultBranch, &t.IndexCursor); err != nil {
			return nil, fmt.Errorf("scanning repo index target: %w", err)
		}
		owner, repo, ok := strings.Cut(fullName, "/")
		if !ok {
			// Skip rather than fail the batch. One malformed row must not stop
			// every other repo from ever being indexed.
			continue
		}
		t.Owner, t.Repo = owner, repo
		if t.DefaultBranch == "" {
			t.DefaultBranch = "main"
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkRepoGraphAttempted records that an index was tried, whatever the outcome.
//
// Written BEFORE the attempt, not after, so a crash or a deadline kill still
// moves the repo to the back of the queue. Recording it only on the way out
// would leave a repo that reliably kills the worker selected forever.
func (s *Store) MarkRepoGraphAttempted(ctx context.Context, repoID int64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE repos SET graph_index_attempted_at = NOW() WHERE id = $1`, repoID)
	if err != nil {
		return fmt.Errorf("marking repo %d graph-index attempt: %w", repoID, err)
	}
	return nil
}

// MarkRepoGraphIndexed records a completed full index.
//
// Only called on success. A failed index deliberately leaves graph_indexed_at
// alone so the repo is retried rather than waiting out a full staleness window
// — the attempt stamp above is what keeps that retry from monopolising the
// queue.
func (s *Store) MarkRepoGraphIndexed(ctx context.Context, repoID int64, nextCursor int) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE repos SET graph_indexed_at = NOW(), graph_index_cursor = $2 WHERE id = $1`,
		repoID, nextCursor)
	if err != nil {
		return fmt.Errorf("marking repo %d graph-indexed: %w", repoID, err)
	}
	return nil
}

// GraphSnapshot is the externally visible state of the newest full-index generation.
type GraphSnapshot struct {
	GenerationID     int64      `json:"generation_id"`
	CommitSHA        string     `json:"commit_sha"`
	Status           string     `json:"status"`
	TreeTruncated    bool       `json:"tree_truncated"`
	ExpectedFiles    int        `json:"expected_files"`
	VisitedFiles     int        `json:"visited_files"`
	FailedFiles      int        `json:"failed_files"`
	UnavailableFiles int        `json:"unavailable_files"`
	SkippedFiles     int        `json:"skipped_files"`
	Complete         bool       `json:"complete"`
	StartedAt        time.Time  `json:"started_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
}

// GraphGenerationFile is one staged deterministic parser snapshot.
type GraphGenerationFile struct {
	FilePath  string
	Symbols   json.RawMessage
	Edges     json.RawMessage
	Endpoints json.RawMessage
}

// BeginGraphGeneration resumes the same immutable snapshot or supersedes it
// when the default branch moved. A truncated tree is recorded as failed and is
// never eligible for publication.
func (s *Store) BeginGraphGeneration(ctx context.Context, repoID int64, commitSHA string, expectedFiles, skippedFiles int, treeTruncated bool) (GraphSnapshot, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("begin graph generation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE graph_index_generations SET status = 'superseded', updated_at = NOW()
		WHERE repo_id = $1 AND status = 'building' AND commit_sha <> $2`, repoID, commitSHA); err != nil {
		return GraphSnapshot{}, fmt.Errorf("supersede graph generation: %w", err)
	}

	status, message := "building", ""
	if treeTruncated {
		status, message = "failed", "github tree response was truncated"
		if _, err := tx.Exec(ctx, `UPDATE graph_index_generations SET status = 'superseded', updated_at = NOW() WHERE repo_id = $1 AND status = 'building'`, repoID); err != nil {
			return GraphSnapshot{}, fmt.Errorf("supersede truncated graph generation: %w", err)
		}
	}
	var snap GraphSnapshot
	err = tx.QueryRow(ctx, `
		INSERT INTO graph_index_generations (repo_id, commit_sha, status, tree_truncated, expected_files, skipped_files, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (repo_id) WHERE status = 'building'
		DO UPDATE SET expected_files = EXCLUDED.expected_files,
		              skipped_files = EXCLUDED.skipped_files,
		              tree_truncated = EXCLUDED.tree_truncated,
		              updated_at = NOW()
		RETURNING id, commit_sha, status, tree_truncated, expected_files,
		          visited_files, failed_files, skipped_files, started_at, published_at`,
		repoID, commitSHA, status, treeTruncated, expectedFiles, skippedFiles, message).Scan(
		&snap.GenerationID, &snap.CommitSHA, &snap.Status, &snap.TreeTruncated,
		&snap.ExpectedFiles, &snap.VisitedFiles, &snap.FailedFiles, &snap.SkippedFiles,
		&snap.StartedAt, &snap.PublishedAt)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("insert graph generation: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'unavailable'`, snap.GenerationID).Scan(&snap.UnavailableFiles); err != nil {
		return GraphSnapshot{}, fmt.Errorf("count unavailable graph files: %w", err)
	}
	snap.Complete = snap.Status == "published"
	if _, err := tx.Exec(ctx, `
		UPDATE repos SET graph_index_commit_sha = $2, graph_index_expected_files = $3,
		  graph_index_visited_files = $4, graph_index_failed_files = $5,
		  graph_index_skipped_files = $6, graph_index_tree_truncated = $7
		WHERE id = $1`, repoID, snap.CommitSHA, snap.ExpectedFiles, snap.VisitedFiles,
		snap.FailedFiles, snap.SkippedFiles, snap.TreeTruncated); err != nil {
		return GraphSnapshot{}, fmt.Errorf("update repo graph generation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphSnapshot{}, fmt.Errorf("commit graph generation: %w", err)
	}
	return snap, nil
}

// StageGraphGenerationFile upserts one file result and refreshes generation
// counts. Retrying a transiently failed file replaces its failure with a ready
// snapshot. permanentlyUnavailable is valid only with a non-nil stageErr and
// records a terminal omission at this immutable commit.
func (s *Store) StageGraphGenerationFile(ctx context.Context, repoID, generationID int64, filePath string, symbols, edges, endpoints []byte, stageErr error, permanentlyUnavailable bool) (GraphSnapshot, error) {
	if permanentlyUnavailable && stageErr == nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file %s: permanent-unavailable result requires an error", filePath)
	}
	status, message := "ready", ""
	if stageErr != nil {
		status, message = "failed", stageErr.Error()
		if permanentlyUnavailable {
			status = "unavailable"
		}
		symbols, edges, endpoints = []byte("[]"), []byte("[]"), []byte("[]")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_index_generation_files (generation_id, file_path, status, symbols, edges, endpoints, error)
		SELECT $1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7
		WHERE EXISTS (SELECT 1 FROM graph_index_generations WHERE id = $1 AND repo_id = $8 AND status = 'building')
		ON CONFLICT (generation_id, file_path) DO UPDATE
		SET status = EXCLUDED.status, symbols = EXCLUDED.symbols, edges = EXCLUDED.edges,
		    endpoints = EXCLUDED.endpoints, error = EXCLUDED.error, updated_at = NOW()`,
		generationID, filePath, status, symbols, edges, endpoints, message, repoID); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: upsert: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_index_generations g SET
		  visited_files = x.visited, failed_files = x.failed, updated_at = NOW()
		FROM (SELECT count(*)::int visited, count(*) FILTER (WHERE status IN ('failed', 'unavailable'))::int failed
		      FROM graph_index_generation_files WHERE generation_id = $1) x
		WHERE g.id = $1 AND g.repo_id = $2 AND g.status = 'building'`, generationID, repoID); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: counts: %w", err)
	}
	if permanentlyUnavailable {
		if _, err := tx.Exec(ctx, `
			UPDATE graph_index_generations SET status = 'failed', error = $3, updated_at = NOW()
			WHERE id = $1 AND repo_id = $2 AND status = 'building'`, generationID, repoID, message); err != nil {
			return GraphSnapshot{}, fmt.Errorf("stage graph file: fail generation: %w", err)
		}
	}
	var snap GraphSnapshot
	if err := tx.QueryRow(ctx, `
		SELECT id, commit_sha, status, tree_truncated, expected_files, visited_files,
		       failed_files, skipped_files, started_at, published_at
		FROM graph_index_generations WHERE id = $1 AND repo_id = $2`, generationID, repoID).Scan(
		&snap.GenerationID, &snap.CommitSHA, &snap.Status, &snap.TreeTruncated,
		&snap.ExpectedFiles, &snap.VisitedFiles, &snap.FailedFiles, &snap.SkippedFiles,
		&snap.StartedAt, &snap.PublishedAt); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: snapshot: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'unavailable'`, generationID).Scan(&snap.UnavailableFiles); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: count unavailable: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE repos SET graph_index_visited_files = $2, graph_index_failed_files = $3 WHERE id = $1`, repoID, snap.VisitedFiles, snap.FailedFiles); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: repo counts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphSnapshot{}, fmt.Errorf("stage graph file: commit: %w", err)
	}
	return snap, nil
}

// FailGraphGeneration makes a deterministic immutable-snapshot failure
// terminal without changing the currently published projection.
func (s *Store) FailGraphGeneration(ctx context.Context, repoID, generationID int64, generationErr error) error {
	message := ""
	if generationErr != nil {
		message = generationErr.Error()
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE graph_index_generations SET status = 'failed', error = $3, updated_at = NOW()
		WHERE id = $1 AND repo_id = $2 AND status = 'building'`, generationID, repoID, message)
	if err != nil {
		return fmt.Errorf("fail graph generation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("fail graph generation: generation %d is not building for repo %d", generationID, repoID)
	}
	return nil
}

// ListReadyGraphGenerationPaths returns successfully parsed files that do not
// need to be fetched on a continuation. Transient failures remain retryable.
func (s *Store) ListReadyGraphGenerationPaths(ctx context.Context, generationID int64) (map[string]struct{}, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT file_path FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'ready'`, generationID)
	if err != nil {
		return nil, fmt.Errorf("list ready graph paths: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out[path] = struct{}{}
	}
	return out, rows.Err()
}

func (s *Store) ListGraphGenerationFiles(ctx context.Context, generationID int64) ([]GraphGenerationFile, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT file_path, symbols, edges, endpoints
		FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'ready' ORDER BY file_path`, generationID)
	if err != nil {
		return nil, fmt.Errorf("list graph generation files: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (GraphGenerationFile, error) {
		var f GraphGenerationFile
		err := row.Scan(&f.FilePath, &f.Symbols, &f.Edges, &f.Endpoints)
		return f, err
	})
}

// GetGraphSnapshot returns the building generation when one exists, otherwise
// the newest published/failed generation. A repo without a generation returns
// the zero snapshot without error.
func (s *Store) GetGraphSnapshot(ctx context.Context, repoID int64) (GraphSnapshot, error) {
	var snap GraphSnapshot
	err := s.Pool.QueryRow(ctx, `
		SELECT g.id, g.commit_sha, g.status, g.tree_truncated, g.expected_files, g.visited_files,
		       g.failed_files, (SELECT count(*)::int FROM graph_index_generation_files f
		         WHERE f.generation_id = g.id AND f.status = 'unavailable'),
		       g.skipped_files, g.started_at, g.published_at
		FROM graph_index_generations g WHERE g.repo_id = $1
		ORDER BY (g.status = 'building') DESC, g.started_at DESC LIMIT 1`, repoID).Scan(
		&snap.GenerationID, &snap.CommitSHA, &snap.Status, &snap.TreeTruncated,
		&snap.ExpectedFiles, &snap.VisitedFiles, &snap.FailedFiles, &snap.UnavailableFiles,
		&snap.SkippedFiles, &snap.StartedAt, &snap.PublishedAt)
	if err == pgx.ErrNoRows {
		return GraphSnapshot{}, nil
	}
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("get graph snapshot: %w", err)
	}
	snap.Complete = snap.Status == "published" && !snap.TreeTruncated && snap.FailedFiles == 0 && snap.VisitedFiles == snap.ExpectedFiles
	return snap, nil
}

// HasBuildingGraphGeneration reports whether a capped or failed window needs a prompt continuation.
func (s *Store) HasBuildingGraphGeneration(ctx context.Context) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM graph_index_generations WHERE status = 'building')`).Scan(&exists)
	return exists, err
}

// TryGraphIndexLock takes a session-level advisory lock for the backfill.
//
// Both Fly machines run this loop, and their ticks are offset by boot time, so
// without a lock machine B can select the same repo machine A is minutes into
// indexing — A has not marked it yet. The duplicated writes are harmless
// (idempotent upserts), but the GitHub cost is not: two concurrent full indexes
// are up to 3000 fetches against the installation's 5000/hour budget, and those
// are the same calls in-flight reviews are waiting on.
//
// try_ rather than a blocking acquire: a machine that loses the race should skip
// this tick and come back in an hour, not queue up behind the winner and then
// run a redundant index the moment it finishes.
func (s *Store) TryGraphIndexLock(ctx context.Context) (acquired bool, release func(), err error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("acquiring conn for graph index lock: %w", err)
	}
	// The lock is tied to this SESSION, so the connection must be held for as
	// long as the lock is. Returning it to the pool would let another caller
	// unlock it, or hold the lock under an unrelated query.
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1)`, graphIndexLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("taking graph index lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return false, nil, nil
	}
	return true, func() {
		// Best-effort unlock on a fresh context: the caller's is usually already
		// cancelled by the time this runs. Even if it fails, the lock dies with
		// the session when the connection closes.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, graphIndexLockKey)
		conn.Release()
	}, nil
}

// graphIndexLockKey is an arbitrary but FIXED key. Advisory locks share one
// global namespace per database, so this must not collide with another
// subsystem's key.
const graphIndexLockKey int64 = 0x4152475553475831 // "ARGUSGX1"

// RebuildCodeGraphProjection refreshes the pgGraph projection.
//
// pgGraph serves traversals from an in-memory CSR projection built by
// graph.build(). It is registered with sync_mode = 'manual', so rows written
// since the last build are invisible to it: a traversal touching a new node
// fails with "Node not found … may need a rebuild", and the caller silently
// falls back to the recursive CTE. A full index writes thousands of such nodes,
// so it must be followed by a rebuild or the work is invisible to the fast path.
//
// Best-effort by design. pgGraph is an optional extension — a self-hosted
// deployment on stock Postgres has no graph schema at all, and there the CTE
// path is the only path and is correct. A missing extension is therefore not an
// error worth failing an otherwise successful index over.
func (s *Store) RebuildCodeGraphProjection(ctx context.Context) error {
	var exists bool
	if err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'graph')`).Scan(&exists); err != nil {
		return fmt.Errorf("probing for pgGraph: %w", err)
	}
	if !exists {
		return nil
	}
	if _, err := s.Pool.Exec(ctx, `SELECT graph.build()`); err != nil {
		return fmt.Errorf("rebuilding pgGraph projection: %w", err)
	}
	return nil
}
