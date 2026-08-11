package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrGraphDefaultBranchMismatch asks the webhook adapter to arbitrate a
// branch-name change against live GitHub repository metadata before retrying.
var ErrGraphDefaultBranchMismatch = errors.New("graph default branch mismatch requires verification")

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

// ListReposDueForPromptGraphIndex returns only event-requested refreshes and
// immutable continuations. It is cheap to poll frequently without accelerating
// the ordinary 14-day backfill queue.
func (s *Store) ListReposDueForPromptGraphIndex(ctx context.Context, limit int) ([]RepoIndexTarget, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, r.installation_id, i.installation_id, r.full_name, r.default_branch, r.graph_index_cursor
		FROM repos r
		JOIN installations i ON i.id = r.installation_id
		WHERE r.enabled AND i.suspended_at IS NULL
		  AND (r.graph_refresh_requested_at IS NOT NULL OR EXISTS (
		    SELECT 1 FROM graph_index_generations g
		    WHERE g.repo_id = r.id AND g.status = 'building'))
		ORDER BY r.graph_index_attempted_at NULLS FIRST,
		         r.graph_refresh_requested_at NULLS LAST, r.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing repos due for prompt graph index: %w", err)
	}
	defer rows.Close()

	var out []RepoIndexTarget
	for rows.Next() {
		var target RepoIndexTarget
		var fullName string
		if err := rows.Scan(&target.RepoID, &target.DBInstallationID,
			&target.GitHubInstallationID, &fullName, &target.DefaultBranch,
			&target.IndexCursor); err != nil {
			return nil, fmt.Errorf("scanning prompt repo index target: %w", err)
		}
		owner, repo, ok := strings.Cut(fullName, "/")
		if !ok {
			continue
		}
		target.Owner, target.Repo = owner, repo
		if target.DefaultBranch == "" {
			target.DefaultBranch = "main"
		}
		out = append(out, target)
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

// ScheduleGraphIndexRefresh records a verified head only when its branch still
// matches the stored default branch. This is the safe seam for merged PRs: a PR
// proves its target branch, but not the repository's authoritative default.
func (s *Store) ScheduleGraphIndexRefresh(
	ctx context.Context,
	githubInstallationID, githubRepoID int64,
	fullName, defaultBranch, commitSHA string,
	observedAt time.Time,
) (bool, error) {
	return s.scheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		fullName, defaultBranch, commitSHA, observedAt, false, "")
}

// ScheduleGraphIndexRefreshFromPush records a signed default-branch push when
// its branch still matches stored authority. A mismatch returns
// ErrGraphDefaultBranchMismatch without mutation so the API adapter can verify
// the current branch through installation-authenticated GitHub metadata.
func (s *Store) ScheduleGraphIndexRefreshFromPush(
	ctx context.Context,
	githubInstallationID, githubRepoID int64,
	fullName, defaultBranch, commitSHA string,
	observedAt time.Time,
) (bool, error) {
	return s.scheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		fullName, defaultBranch, commitSHA, observedAt, true, "")
}

// ScheduleGraphIndexRefreshFromVerifiedPush permits a branch-name change only
// when live repository metadata named the same default branch. Repository
// scope is re-checked and locked inside the retry before mutation.
func (s *Store) ScheduleGraphIndexRefreshFromVerifiedPush(
	ctx context.Context,
	githubInstallationID, githubRepoID int64,
	fullName, defaultBranch, commitSHA string,
	observedAt time.Time,
	verifiedDefaultBranch string,
) (bool, error) {
	return s.scheduleGraphIndexRefresh(ctx, githubInstallationID, githubRepoID,
		fullName, defaultBranch, commitSHA, observedAt, true, verifiedDefaultBranch)
}

// scheduleGraphIndexRefresh scopes by tenant, installation-owned repository
// identity, and full name before locking the row. A branch-authoritative push
// may replace only the branch field; it cannot weaken any other scope.
func (s *Store) scheduleGraphIndexRefresh(
	ctx context.Context,
	githubInstallationID, githubRepoID int64,
	fullName, defaultBranch, commitSHA string,
	observedAt time.Time,
	branchAuthoritative bool,
	verifiedDefaultBranch string,
) (bool, error) {
	if githubInstallationID <= 0 || githubRepoID <= 0 || fullName == "" || defaultBranch == "" || commitSHA == "" {
		return false, nil
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("schedule graph refresh: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var repoID int64
	var storedDefaultBranch string
	var publishedCommit, observedCommit, requestedCommit *string
	var publishedComplete bool
	var lastEventAt, requestedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT r.id, r.default_branch, published.commit_sha,
		       COALESCE(published.status = 'published' AND NOT published.tree_truncated
		         AND published.failed_files = 0 AND published.visited_files = published.expected_files
		         AND NOT EXISTS (SELECT 1 FROM graph_index_generation_files unavailable
		           WHERE unavailable.generation_id = published.id AND unavailable.status = 'unavailable'), false),
		       r.graph_default_head_sha, r.graph_default_head_event_at,
		       r.graph_refresh_requested_at, r.graph_refresh_commit_sha
		FROM repos r
		JOIN installations i ON i.id = r.installation_id
		LEFT JOIN graph_index_generations published
		  ON published.id = r.graph_published_generation_id AND published.repo_id = r.id
		WHERE i.installation_id = $1 AND i.suspended_at IS NULL
		  AND r.github_id = $2 AND r.full_name = $3 AND r.enabled
		FOR UPDATE OF r`, githubInstallationID, githubRepoID, fullName).Scan(
		&repoID, &storedDefaultBranch, &publishedCommit, &publishedComplete,
		&observedCommit, &lastEventAt, &requestedAt, &requestedCommit)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("schedule graph refresh: scope repository: %w", err)
	}

	branchChanged := storedDefaultBranch != defaultBranch
	if branchChanged {
		if !branchAuthoritative {
			return false, nil
		}
		if verifiedDefaultBranch != defaultBranch {
			return false, ErrGraphDefaultBranchMismatch
		}
	}
	// A delayed pre-rename delivery must not restore the old branch merely
	// because its commit happens to equal the currently observed commit.
	if lastEventAt != nil && observedAt.Before(*lastEventAt) &&
		(branchChanged || observedCommit == nil || *observedCommit != commitSHA) {
		return false, nil
	}

	alreadyIndexed := !branchChanged && publishedComplete && publishedCommit != nil && *publishedCommit == commitSHA &&
		observedCommit != nil && *observedCommit == commitSHA && requestedAt == nil
	alreadyRequested := !branchChanged && requestedAt != nil && requestedCommit != nil && *requestedCommit == commitSHA
	if alreadyIndexed || alreadyRequested {
		if _, err := tx.Exec(ctx, `
			UPDATE repos SET graph_default_head_sha = $2,
			  graph_default_head_observed_at = GREATEST(COALESCE(graph_default_head_observed_at, $3), $3),
			  graph_default_head_event_at = GREATEST(COALESCE(graph_default_head_event_at, $3), $3),
			  graph_refresh_requested_at = CASE WHEN $4 THEN NULL ELSE graph_refresh_requested_at END,
			  graph_refresh_commit_sha = CASE WHEN $4 THEN NULL ELSE graph_refresh_commit_sha END
			WHERE id = $1`, repoID, commitSHA, observedAt, alreadyIndexed); err != nil {
			return false, fmt.Errorf("schedule graph refresh: record duplicate head: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("schedule graph refresh: commit duplicate: %w", err)
		}
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE repos SET default_branch = $2, graph_default_head_sha = $3,
		  graph_default_head_observed_at = $4, graph_default_head_event_at = $4,
		  graph_refresh_requested_at = NOW(), graph_refresh_commit_sha = $3,
		  graph_refresh_version = graph_refresh_version + 1,
		  graph_index_attempted_at = CASE WHEN $5 THEN NULL ELSE graph_index_attempted_at END,
		  graph_index_cursor = CASE WHEN $5 THEN 0 ELSE graph_index_cursor END,
		  updated_at = CASE WHEN $5 THEN NOW() ELSE updated_at END
		WHERE id = $1`, repoID, defaultBranch, commitSHA, observedAt, branchChanged); err != nil {
		return false, fmt.Errorf("schedule graph refresh: mark due: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("schedule graph refresh: commit: %w", err)
	}
	return true, nil
}

// ConfirmGraphDefaultHead records the head resolved by the worker only if no
// webhook changed the request while the GitHub call was in flight. It also
// consumes a request when the already-published immutable generation matches.
func (s *Store) ConfirmGraphDefaultHead(ctx context.Context, repoID, refreshVersion int64, commitSHA string) (alreadyCurrent, confirmed bool, err error) {
	if repoID <= 0 || commitSHA == "" {
		return false, false, nil
	}
	err = s.Pool.QueryRow(ctx, `
		WITH authority AS (
		  SELECT r.id, COALESCE(published.status = 'published' AND NOT published.tree_truncated
		    AND published.failed_files = 0 AND published.visited_files = published.expected_files
		    AND published.commit_sha = $3
		    AND NOT EXISTS (SELECT 1 FROM graph_index_generation_files unavailable
		      WHERE unavailable.generation_id = published.id AND unavailable.status = 'unavailable'), false) AS already_current
		  FROM repos r
		  LEFT JOIN graph_index_generations published
		    ON published.id = r.graph_published_generation_id AND published.repo_id = r.id
		  WHERE r.id = $1 AND r.graph_refresh_version = $2
		)
		UPDATE repos r SET graph_default_head_sha = $3, graph_default_head_observed_at = NOW(),
		  graph_refresh_commit_sha = CASE WHEN r.graph_refresh_requested_at IS NULL THEN NULL ELSE $3 END,
		  graph_refresh_requested_at = CASE WHEN authority.already_current THEN NULL ELSE r.graph_refresh_requested_at END,
		  graph_indexed_at = CASE WHEN authority.already_current THEN NOW() ELSE r.graph_indexed_at END
		FROM authority
		WHERE r.id = authority.id AND r.graph_refresh_version = $2
		RETURNING authority.already_current`, repoID, refreshVersion, commitSHA).Scan(&alreadyCurrent)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("confirm graph default head: %w", err)
	}
	return alreadyCurrent, true, nil
}

// GraphSnapshot is the externally visible state of the newest full-index generation.
type GraphSnapshot struct {
	GenerationID             int64      `json:"generation_id"`
	CommitSHA                string     `json:"commit_sha"`
	Status                   string     `json:"status"`
	TreeTruncated            bool       `json:"tree_truncated"`
	ExpectedFiles            int        `json:"expected_files"`
	VisitedFiles             int        `json:"visited_files"`
	FailedFiles              int        `json:"failed_files"`
	UnavailableFiles         int        `json:"unavailable_files"`
	SkippedFiles             int        `json:"skipped_files"`
	Complete                 bool       `json:"complete"`
	Current                  bool       `json:"current"`
	StartedAt                time.Time  `json:"started_at"`
	PublishedAt              *time.Time `json:"published_at,omitempty"`
	TopologyPublishedAt      *time.Time `json:"topology_published_at,omitempty"`
	PublishedCommitSHA       string     `json:"published_commit_sha"`
	DefaultHeadSHA           string     `json:"default_head_sha"`
	RefreshRequestedAt       *time.Time `json:"refresh_requested_at,omitempty"`
	RefreshVersion           int64      `json:"-"`
	GenerationRefreshVersion int64      `json:"-"`
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
func (s *Store) BeginGraphGeneration(ctx context.Context, repoID int64, commitSHA string, expectedFiles, skippedFiles int, treeTruncated bool, refreshVersion int64) (GraphSnapshot, error) {
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
		INSERT INTO graph_index_generations (repo_id, commit_sha, status, tree_truncated, expected_files, skipped_files, error, refresh_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (repo_id) WHERE status = 'building'
		DO UPDATE SET expected_files = EXCLUDED.expected_files,
		              skipped_files = EXCLUDED.skipped_files,
		              tree_truncated = EXCLUDED.tree_truncated,
		              updated_at = NOW()
		RETURNING id, commit_sha, status, tree_truncated, expected_files,
		          visited_files, failed_files, skipped_files, started_at, published_at, refresh_version`,
		repoID, commitSHA, status, treeTruncated, expectedFiles, skippedFiles, message, refreshVersion).Scan(
		&snap.GenerationID, &snap.CommitSHA, &snap.Status, &snap.TreeTruncated,
		&snap.ExpectedFiles, &snap.VisitedFiles, &snap.FailedFiles, &snap.SkippedFiles,
		&snap.StartedAt, &snap.PublishedAt, &snap.GenerationRefreshVersion)
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
		       failed_files, skipped_files, started_at, published_at, refresh_version
		FROM graph_index_generations WHERE id = $1 AND repo_id = $2`, generationID, repoID).Scan(
		&snap.GenerationID, &snap.CommitSHA, &snap.Status, &snap.TreeTruncated,
		&snap.ExpectedFiles, &snap.VisitedFiles, &snap.FailedFiles, &snap.SkippedFiles,
		&snap.StartedAt, &snap.PublishedAt, &snap.GenerationRefreshVersion); err != nil {
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
	var generationID *int64
	var commitSHA, status *string
	var treeTruncated *bool
	var expectedFiles, visitedFiles, failedFiles, unavailableFiles, skippedFiles *int
	var startedAt, generationPublishedAt *time.Time
	var generationRefreshVersion *int64
	var publishedCommitSHA, defaultHeadSHA *string
	var publishedComplete bool

	err := s.Pool.QueryRow(ctx, `
		SELECT g.id, g.commit_sha, g.status, g.tree_truncated, g.expected_files,
		       g.visited_files, g.failed_files,
		       CASE WHEN g.id IS NULL THEN NULL ELSE (
		         SELECT count(*)::int FROM graph_index_generation_files f
		         WHERE f.generation_id = g.id AND f.status = 'unavailable') END,
		       g.skipped_files, g.started_at, g.published_at, g.refresh_version,
		       published.commit_sha, published.published_at,
		       COALESCE(published.status = 'published' AND NOT published.tree_truncated
		         AND published.failed_files = 0 AND published.visited_files = published.expected_files
		         AND NOT EXISTS (SELECT 1 FROM graph_index_generation_files unavailable
		           WHERE unavailable.generation_id = published.id AND unavailable.status = 'unavailable'), false),
		       r.graph_default_head_sha, r.graph_refresh_requested_at, r.graph_refresh_version
		FROM repos r
		LEFT JOIN LATERAL (
		  SELECT candidate.* FROM graph_index_generations candidate
		  WHERE candidate.repo_id = r.id
		  ORDER BY (candidate.status = 'building') DESC, candidate.started_at DESC LIMIT 1
		) g ON true
		LEFT JOIN graph_index_generations published ON published.id = r.graph_published_generation_id
		WHERE r.id = $1`, repoID).Scan(
		&generationID, &commitSHA, &status, &treeTruncated, &expectedFiles,
		&visitedFiles, &failedFiles, &unavailableFiles, &skippedFiles, &startedAt,
		&generationPublishedAt, &generationRefreshVersion, &publishedCommitSHA,
		&snap.TopologyPublishedAt, &publishedComplete, &defaultHeadSHA, &snap.RefreshRequestedAt,
		&snap.RefreshVersion)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("get graph snapshot: %w", err)
	}
	if generationID != nil {
		snap.GenerationID = *generationID
		snap.CommitSHA = *commitSHA
		snap.Status = *status
		snap.TreeTruncated = *treeTruncated
		snap.ExpectedFiles = *expectedFiles
		snap.VisitedFiles = *visitedFiles
		snap.FailedFiles = *failedFiles
		snap.UnavailableFiles = *unavailableFiles
		snap.SkippedFiles = *skippedFiles
		snap.StartedAt = *startedAt
		snap.PublishedAt = generationPublishedAt
		snap.GenerationRefreshVersion = *generationRefreshVersion
		snap.Complete = snap.Status == "published" && !snap.TreeTruncated && snap.FailedFiles == 0 && snap.VisitedFiles == snap.ExpectedFiles
	}
	if publishedCommitSHA != nil {
		snap.PublishedCommitSHA = *publishedCommitSHA
	}
	if defaultHeadSHA != nil {
		snap.DefaultHeadSHA = *defaultHeadSHA
	}
	snap.Current = publishedComplete && snap.PublishedCommitSHA != "" && snap.DefaultHeadSHA == snap.PublishedCommitSHA && snap.RefreshRequestedAt == nil
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
