package store

import (
	"context"
	"fmt"
	"strings"
	"time"
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
// The ATTEMPT time is the second sort key, and it is what stops one broken
// repository from starving every other. A failure leaves graph_indexed_at NULL
// on purpose, so ordering on that column alone would re-select the same failing
// repo every tick, forever, while the queue behind it never moved. Ordering on
// the attempt after it means a failure goes to the back and is retried only
// once everything else has had a turn.
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
		  AND (r.graph_indexed_at IS NULL OR r.graph_indexed_at < NOW() - $1::interval)
		ORDER BY r.graph_indexed_at NULLS FIRST, r.graph_index_attempted_at NULLS FIRST, r.id
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
