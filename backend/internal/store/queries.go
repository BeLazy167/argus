package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// collectOrEmpty wraps pgx.CollectRows and returns an empty slice instead of nil.
func collectOrEmpty[T any](rows pgx.Rows, fn pgx.RowToFunc[T]) ([]T, error) {
	result, err := pgx.CollectRows(rows, fn)
	if result == nil {
		result = []T{}
	}
	return result, err
}

// --- Installations ---

func (s *Store) CreateInstallation(ctx context.Context, installationID int64, orgLogin string) (*Installation, error) {
	row, err := s.q.CreateInstallation(ctx, db.CreateInstallationParams{InstallationID: installationID, OrgLogin: orgLogin})
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) ListInstallations(ctx context.Context) ([]Installation, error) {
	rows, err := s.q.ListInstallations(ctx)
	if err != nil {
		return nil, err
	}
	installations := make([]Installation, 0, len(rows))
	for _, row := range rows {
		installations = append(installations, installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt))
	}
	return installations, nil
}

// --- User Installations ---

func (s *Store) LinkUserInstallation(ctx context.Context, clerkUserID string, installationID int64, role string) (*UserInstallation, error) {
	row, err := s.q.LinkUserInstallation(ctx, db.LinkUserInstallationParams{ClerkUserID: clerkUserID, InstallationID: installationID, Role: role})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = s.q.GetUserInstallationByUserAndInstallation(ctx, db.GetUserInstallationByUserAndInstallationParams{ClerkUserID: clerkUserID, InstallationID: installationID})
	}
	if err != nil {
		return nil, err
	}
	installation := UserInstallation{ID: row.ID, ClerkUserID: row.ClerkUserID, InstallationID: row.InstallationID, Role: row.Role, CreatedAt: row.CreatedAt}
	return &installation, nil
}

// IsUserLinkedToInstallation checks if a user is already linked to an installation.
// Returns (linked, error). DB errors are surfaced to the caller so they cannot
// silently degrade authorization decisions (a query failure used to
// return false and fall through as a first-owner claim).
// A pgx.ErrNoRows result is treated as "not linked" with a nil error.
func (s *Store) IsUserLinkedToInstallation(ctx context.Context, clerkUserID string, installationID int64) (bool, error) {
	var exists int
	err := s.Pool.QueryRow(ctx, `
		SELECT 1 FROM user_installations WHERE installation_id = $1 AND clerk_user_id = $2
	`, installationID, clerkUserID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return exists == 1, nil
}

// CountInstallationUsers returns the number of users linked to an installation.
func (s *Store) CountInstallationUsers(ctx context.Context, installationID int64) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM user_installations WHERE installation_id = $1`, installationID).Scan(&count)
	return count, err
}

func (s *Store) ListUserInstallations(ctx context.Context, clerkUserID string) ([]Installation, error) {
	rows, err := s.q.ListUserInstallations(ctx, clerkUserID)
	if err != nil {
		return nil, err
	}
	installations := make([]Installation, 0, len(rows))
	for _, row := range rows {
		installations = append(installations, installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt))
	}
	return installations, nil
}

func (s *Store) GetUserInstallationIDs(ctx context.Context, clerkUserID string) ([]int64, error) {
	rows, err := s.q.GetUserInstallationIDs(ctx, clerkUserID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	copy(ids, rows)
	return ids, nil
}

func (s *Store) GetInstallation(ctx context.Context, id int64) (*Installation, error) {
	row, err := s.q.GetInstallation(ctx, id)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) GetInstallationByGitHubID(ctx context.Context, ghInstallationID int64) (*Installation, error) {
	row, err := s.q.GetInstallationByGitHubID(ctx, ghInstallationID)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) GetInstallationByClerkOrgID(ctx context.Context, clerkOrgID string) (*Installation, error) {
	row, err := s.q.GetInstallationByClerkOrgID(ctx, &clerkOrgID)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) CountReviewsThisMonth(ctx context.Context, installationID int64) (int, error) {
	return s.q.CountReviewsThisMonth(ctx, installationID)
}

func (s *Store) CountEnabledRepos(ctx context.Context, installationID int64) (int, error) {
	return s.q.CountEnabledRepos(ctx, installationID)
}

func (s *Store) SuspendInstallation(ctx context.Context, id int64) error {
	return s.q.SuspendInstallation(ctx, id)
}

func (s *Store) SetInstallationClerkOrgID(ctx context.Context, installationID int64, clerkOrgID string) error {
	return s.q.SetInstallationClerkOrgID(ctx, db.SetInstallationClerkOrgIDParams{ClerkOrgID: &clerkOrgID, ID: installationID})
}

// --- Org Default Settings ---

func (s *Store) GetOrgDefaults(ctx context.Context, installationID int64) (json.RawMessage, error) {
	return s.q.GetOrgDefaults(ctx, installationID)
}

func (s *Store) SetOrgDefaults(ctx context.Context, installationID int64, settings json.RawMessage) error {
	return s.q.SetOrgDefaults(ctx, db.SetOrgDefaultsParams{DefaultSettings: settings, ID: installationID})
}

// GetMergedSettings returns org defaults merged with repo overrides (repo wins).
func (s *Store) GetMergedSettings(ctx context.Context, installationID int64, repoID int64) (json.RawMessage, error) {
	var orgDefaults, repoSettings json.RawMessage
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(default_settings, '{}') FROM installations WHERE id = $1`, installationID).Scan(&orgDefaults); err != nil {
		return nil, fmt.Errorf("fetching org defaults: %w", err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(settings_json, '{}') FROM repos WHERE id = $1`, repoID).Scan(&repoSettings); err != nil {
		return nil, fmt.Errorf("fetching repo settings: %w", err)
	}
	return mergeJSON(orgDefaults, repoSettings), nil
}

// mergeJSON does a shallow merge where override keys replace base keys.
func mergeJSON(base, override json.RawMessage) json.RawMessage {
	var baseMap map[string]interface{}
	if err := json.Unmarshal(base, &baseMap); err != nil || baseMap == nil {
		baseMap = make(map[string]interface{})
	}
	var overrideMap map[string]interface{}
	if err := json.Unmarshal(override, &overrideMap); err == nil {
		for k, v := range overrideMap {
			baseMap[k] = v
		}
	}
	result, _ := json.Marshal(baseMap)
	return result
}

// --- Repos ---

func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.q.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	repos := make([]Repo, 0, len(rows))
	for _, row := range rows {
		repos = append(repos, repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt))
	}
	return repos, nil
}

func (s *Store) ListReposByOwner(ctx context.Context, ownerPrefix string) ([]Repo, error) {
	// Escape LIKE wildcards so an LLM-controlled owner stays a literal prefix.
	escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(ownerPrefix)
	rows, err := s.q.ListReposByOwner(ctx, escaped+"/%")
	if err != nil {
		return nil, err
	}
	repos := make([]Repo, 0, len(rows))
	for _, row := range rows {
		repos = append(repos, repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt))
	}
	return repos, nil
}

func (s *Store) GetRepo(ctx context.Context, id int64) (*Repo, error) {
	row, err := s.q.GetRepo(ctx, id)
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) GetRepoByFullName(ctx context.Context, fullName string) (*Repo, error) {
	row, err := s.q.GetRepoByFullName(ctx, fullName)
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) UpdateRepo(ctx context.Context, id int64, enabled *bool, defaultBranch *string, settingsJSON []byte) (*Repo, error) {
	row, err := s.q.UpdateRepo(ctx, db.UpdateRepoParams{ID: id, Enabled: enabled, DefaultBranch: defaultBranch, SettingsJSON: settingsJSON})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) UpsertRepo(ctx context.Context, installationID, githubID int64, fullName, defaultBranch string) (*Repo, error) {
	row, err := s.q.UpsertRepo(ctx, db.UpsertRepoParams{InstallationID: installationID, GithubID: githubID, FullName: fullName, DefaultBranch: defaultBranch})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) ListReposScoped(ctx context.Context, installationIDs []int64) ([]Repo, error) {
	rows, err := s.q.ListReposScoped(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	repos := make([]Repo, 0, len(rows))
	for _, row := range rows {
		repos = append(repos, repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt))
	}
	return repos, nil
}

func (s *Store) GetRepoScoped(ctx context.Context, id int64, installationIDs []int64) (*Repo, error) {
	row, err := s.q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: id, Column2: installationIDs})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

// --- Reviews ---

func (s *Store) GetReview(ctx context.Context, id uuid.UUID) (*Review, error) {
	row, err := s.q.GetReview(ctx, id)
	if err != nil {
		return nil, err
	}
	review := fullReviewFromSQLC(row)
	return &review, nil
}

func (s *Store) GetReviewComments(ctx context.Context, reviewID uuid.UUID) ([]ReviewComment, error) {
	rows, err := s.q.GetReviewComments(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	comments := make([]ReviewComment, 0, len(rows))
	for _, row := range rows {
		comment, err := reviewCommentFromSQLC(row)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

// GetPRCompletedReviewComments returns review comments across ALL completed
// reviews for a repo+PR, not just the single most-recent one. Incremental
// re-reviews dedup against every prior finding on the PR, so a comment posted
// two pushes ago is still visible to the current run. Ordered by file+line to
// keep buildPriorComments' per-file grouping deterministic; caller dedupes.
func (s *Store) GetPRCompletedReviewComments(ctx context.Context, repoID int64, prNumber int) ([]ReviewComment, error) {
	rows, err := s.q.GetPRCompletedReviewComments(ctx, db.GetPRCompletedReviewCommentsParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	comments := make([]ReviewComment, 0, len(rows))
	for _, row := range rows {
		comment, err := completedReviewCommentFromSQLC(row)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

// ListPRReviewSummaries returns every review pass for a repo+PR (one row per
// push, since reviews are per-SHA), oldest first, with each pass's non-suppressed
// finding counts. Powers the review-detail viewer's incremental history. Marker
// reviews (auto_run_disabled / no_api_key stubs) are excluded so they don't
// render as failed passes — same predicate the list endpoints use.
func (s *Store) ListPRReviewSummaries(ctx context.Context, repoID int64, prNumber int) ([]PRReviewSummary, error) {
	rows, err := s.q.ListPRReviewSummaries(ctx, db.ListPRReviewSummariesParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	summaries := make([]PRReviewSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, PRReviewSummary{
			ID: row.ID, HeadSHA: row.HeadSHA, Status: row.Status, Score: row.Score,
			IsIncremental: row.IsIncremental, DeepReview: row.DeepReview,
			CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt,
			CommentCount: row.CommentCount, NewCount: row.NewCount,
		})
	}
	return summaries, nil
}

// ListPRAutoResolveEvents returns the auto-resolve events for a repo+PR, oldest
// first — one row per synchronize push that ACTUALLY closed a stale thread. The
// `resolved_count > 0` filter is load-bearing: auto_resolve_events persists a row
// on every synchronize that touched GitHub, INCLUDING list-only calls that
// resolved nothing (resolved_count = 0). Without the filter those surface as
// "Auto-resolved 0 threads" noise and defeat the viewer's single-pass hide gate.
// The counts feed the incremental-history timeline.
func (s *Store) ListPRAutoResolveEvents(ctx context.Context, repoID int64, prNumber int) ([]AutoResolveSummary, error) {
	rows, err := s.q.ListPRAutoResolveEvents(ctx, db.ListPRAutoResolveEventsParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	events := make([]AutoResolveSummary, 0, len(rows))
	for _, row := range rows {
		events = append(events, AutoResolveSummary{SourceSHA: row.SourceSHA, ResolvedCount: row.ResolvedCount, AttemptedCount: row.AttemptedCount, CreatedAt: row.CreatedAt})
	}
	return events, nil
}

// SetFindingResolvedSHA stamps the resolving push commit onto a finding
// (migration 056), stamp-once: the `resolved_sha IS NULL` guard means the FIRST
// commit that closed the finding wins and later re-resolves are silent no-ops.
// Called best-effort by FindingLifecycle when a finding is marked
// addressed/resolved on a known SHA; a missing row or empty SHA is a no-op.
func (s *Store) SetFindingResolvedSHA(ctx context.Context, commentID uuid.UUID, sha string) error {
	if sha == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE review_comments SET resolved_sha = $2
		WHERE id = $1 AND resolved_sha IS NULL
	`, commentID, sha)
	if err != nil {
		return fmt.Errorf("setting finding resolved sha: %w", err)
	}
	return nil
}

// StartedCommentRef is everything needed to rewrite a review's "watch live"
// comment from any process: which installation to authenticate as, which repo
// and comment to edit, and who asked for the review.
type StartedCommentRef struct {
	CommentID      int64
	InstallationID int64 // GitHub installation id (not the DB serial)
	RepoFullName   string
	PRNumber       int
	TriggeredBy    string
}

// SetStartedCommentID records the id of the progress comment so a later
// failure or cancel — possibly on another machine — can rewrite it.
func (s *Store) SetStartedCommentID(ctx context.Context, reviewID uuid.UUID, commentID int64) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE reviews SET started_comment_id = $2 WHERE id = $1`, reviewID, commentID)
	return err
}

// GetStartedCommentRef loads the progress-comment reference for a review.
// Returns (nil, nil) when the review never posted one — an ordinary case
// (auto-run off, or the create call failed), not an error.
func (s *Store) GetStartedCommentRef(ctx context.Context, reviewID uuid.UUID) (*StartedCommentRef, error) {
	var ref StartedCommentRef
	var commentID *int64
	var triggeredBy *string
	err := s.Pool.QueryRow(ctx, `
		SELECT rv.started_comment_id, i.installation_id, r.full_name, rv.pr_number, rv.triggered_by
		FROM reviews rv
		JOIN repos r ON rv.repo_id = r.id
		JOIN installations i ON r.installation_id = i.id
		WHERE rv.id = $1`, reviewID).
		Scan(&commentID, &ref.InstallationID, &ref.RepoFullName, &ref.PRNumber, &triggeredBy)
	if err != nil {
		return nil, err
	}
	if commentID == nil || *commentID == 0 {
		return nil, nil
	}
	ref.CommentID = *commentID
	if triggeredBy != nil {
		ref.TriggeredBy = *triggeredBy
	}
	return &ref, nil
}

func (s *Store) UpdateReviewStatus(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte) error {
	err := s.q.UpdateReviewStatus(ctx, db.UpdateReviewStatusParams{ID: id, Status: status, Error: nilIfEmpty(errMsg), TokenUsage: tokenUsage})
	if err != nil {
		return fmt.Errorf("updating review status: %w", err)
	}
	return nil
}

// UpdateReviewStatusForAttempt applies a review-owned write only while the
// caller's generation is still current.
func (s *Store) UpdateReviewStatusForAttempt(ctx context.Context, id uuid.UUID, generation int, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error) {
	query := `UPDATE reviews SET status=$3, error=$4, token_usage=COALESCE($5,token_usage), completed_at=CASE WHEN $3 IN ('completed','failed') THEN NOW() ELSE completed_at END WHERE id=$1 AND attempt_generation=$2`
	args := []any{id, generation, status, nilIfEmpty(errMsg), tokenUsage}
	if len(allowedCurrent) > 0 {
		query += ` AND status = ANY($6)`
		args = append(args, allowedCurrent)
	}
	tag, err := s.Pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("updating review attempt status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) GetReviewAttemptGeneration(ctx context.Context, id uuid.UUID) (int, error) {
	var generation int
	if err := s.Pool.QueryRow(ctx, `SELECT attempt_generation FROM reviews WHERE id=$1`, id).Scan(&generation); err != nil {
		return 0, err
	}
	return generation, nil
}

func (s *Store) IsReviewAttemptCurrent(ctx context.Context, id uuid.UUID, generation int) (bool, error) {
	var current bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reviews WHERE id=$1 AND attempt_generation=$2)`, id, generation).Scan(&current); err != nil {
		return false, err
	}
	return current, nil
}

// GetReviewStatus returns just the status column for a review — a cheap PK
// lookup used by the state machine's cooperative-cancellation check, which runs
// at every stage boundary and must stay light.
// ConvergePostedReview repairs a review whose GitHub mutation succeeded but
// whose completion write did not. Cancelled rows remain cancelled.
func (s *Store) ConvergePostedReview(ctx context.Context, id uuid.UUID, generation int) (int64, bool, bool, error) {
	var githubReviewID int64
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT github_review_id,status FROM reviews WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NOT NULL`, id, generation).Scan(&githubReviewID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, fmt.Errorf("checking posted review: %w", err)
	}
	if status == "cancelled" || status == "completed" {
		return githubReviewID, true, false, nil
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE reviews SET status='completed',completed_at=COALESCE(completed_at,NOW()),error=NULL WHERE id=$1 AND attempt_generation=$2 AND status IN ('pending','in_progress','failed')`, id, generation)
	if err != nil {
		return 0, true, false, fmt.Errorf("converging posted review: %w", err)
	}
	return githubReviewID, true, tag.RowsAffected() > 0, nil
}

// BeginReviewRetry atomically starts one new generation. Concurrent callers
// cannot both advance a failed/cancelled review to pending.
func (s *Store) BeginReviewRetry(ctx context.Context, id uuid.UUID) (int, bool, error) {
	var generation int
	err := s.Pool.QueryRow(ctx, `
		UPDATE reviews
		SET status = 'pending', error = NULL, completed_at = NULL,
		    attempt_generation = attempt_generation + 1
		WHERE id = $1 AND status IN ('failed', 'cancelled')
		RETURNING attempt_generation
	`, id).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("beginning review retry: %w", err)
	}
	return generation, true, nil
}

func (s *Store) GetReviewStatus(ctx context.Context, id uuid.UUID) (string, error) {
	var status string
	if err := s.Pool.QueryRow(ctx, `SELECT status FROM reviews WHERE id = $1`, id).Scan(&status); err != nil {
		return "", fmt.Errorf("querying review status: %w", err)
	}
	return status, nil
}

// UpdateReviewStatusIf writes a review's status only when its current status is
// one of allowedCurrent — a compare-and-set that keeps terminal writes from
// racing. A completion write (allowed: in_progress) must not clobber a cancel,
// and a cancel write (allowed: pending/in_progress) must not flip an already
// completed/failed review. Returns whether a row was actually updated.
func (s *Store) UpdateReviewStatusIf(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = $2, error = $3, token_usage = COALESCE($4, token_usage),
		       completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE completed_at END
		WHERE id = $1 AND status = ANY($5)
	`, id, status, nilIfEmpty(errMsg), tokenUsage, allowedCurrent)
	if err != nil {
		return false, fmt.Errorf("updating review status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// markerReviewFilter is the SQL predicate (bare, unambiguous columns) matching
// synthetic "marker" review rows — idempotency-key inserts that were never real
// review attempts. signalAutoRunDisabled writes auto_run_disabled (push-signal
// dedup) and the readiness gate writes no_api_key (onboarding dedup); both carry
// status='failed' with github_review_id IS NULL. The rows stay in the table so
// HasFailedReviewWithError dedup keeps working, but dashboard list/stats reads
// exclude them via `NOT (`+markerReviewFilter+`)` so they don't render as failed
// reviews or inflate TotalReviews.
const markerReviewFilter = `github_review_id IS NULL AND status = 'failed' AND error IN ('auto_run_disabled', 'no_api_key')`

// List queries drop the heavy fields (token_usage, diagram*, diagrams,
// truncated_files, brief) to keep response size manageable. 1 row of those
// columns averages ~5 KB of JSONB; at limit=200 the list payload was 1.22 MB
// (measured via Fly logs on the dashboard route) — ~95% of which was data the
// list view never renders. The detail endpoint (GET /reviews/{id}) still
// returns the full Review struct via GetReview. The generated row type owns
// the list query's scan order, so adding a field to Review cannot recreate the
// production 500 caused by a positional destination-count mismatch.
func (s *Store) ListReviewsScoped(ctx context.Context, repoID int64, installationIDs []int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListReviewsScoped(ctx, db.ListReviewsScopedParams{RepoID: repoID, Column2: installationIDs, RowLimit: int64(limit), RowOffset: int64(offset)})
	if err != nil {
		return nil, err
	}
	reviews := make([]Review, 0, len(rows))
	for _, row := range rows {
		reviews = append(reviews, scopedReviewFromSQLC(row))
	}
	return reviews, nil
}

func (s *Store) ListAllReviewsScoped(ctx context.Context, installationIDs []int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.ListAllReviewsScoped(ctx, db.ListAllReviewsScopedParams{Column1: installationIDs, RowLimit: int64(limit), RowOffset: int64(offset)})
	if err != nil {
		return nil, err
	}
	reviews := make([]Review, 0, len(rows))
	for _, row := range rows {
		reviews = append(reviews, allScopedReviewFromSQLC(row))
	}
	return reviews, nil
}

// ReplaceReviewMinorNotes replaces only one attempt's structured notes.
func (s *Store) ReplaceReviewMinorNotes(ctx context.Context, reviewID uuid.UUID, attemptGeneration int, notes []ReviewMinorNote) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning minor notes replace: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `DELETE FROM review_minor_notes WHERE review_id = $1 AND attempt_generation = $2`, reviewID, attemptGeneration); err != nil {
		return fmt.Errorf("clearing minor notes: %w", err)
	}
	for _, note := range notes {
		if _, err = tx.Exec(ctx, `INSERT INTO review_minor_notes (review_id, attempt_generation, file_path, line, severity, title) VALUES ($1,$2,$3,$4,$5,$6)`, reviewID, attemptGeneration, note.FilePath, note.Line, note.Severity, note.Title); err != nil {
			return fmt.Errorf("inserting minor note: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing minor notes: %w", err)
	}
	return nil
}

// GetReviewMinorNotes returns only the review's current attempt.
func (s *Store) GetReviewMinorNotes(ctx context.Context, reviewID uuid.UUID) ([]ReviewMinorNote, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.review_id, n.attempt_generation, n.file_path, n.line, n.severity, n.title, n.created_at
		FROM review_minor_notes n JOIN reviews r ON r.id = n.review_id
		WHERE n.review_id = $1 AND n.attempt_generation = r.attempt_generation
		ORDER BY n.file_path, n.line, n.id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ReviewMinorNote])
}

// ClaimReviewSignal atomically elects one machine to deliver a CTA. An
// abandoned claim becomes retryable after the lease expires.
func (s *Store) ClaimReviewSignal(ctx context.Context, repoID int64, prNumber int, kind string, staleAfter time.Duration) (uuid.UUID, bool, error) {
	id := uuid.New()
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO review_signals (id, repo_id, pr_number, kind)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (repo_id, pr_number, kind) DO UPDATE
		SET id = EXCLUDED.id, claimed_at = NOW()
		WHERE review_signals.delivered_at IS NULL
		  AND review_signals.claimed_at < NOW() - make_interval(secs => $5)
		RETURNING id`, id, repoID, prNumber, kind, staleAfter.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("claiming review signal: %w", err)
	}
	return id, true, nil
}

func (s *Store) CompleteReviewSignal(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE review_signals SET delivered_at=NOW() WHERE id=$1 AND delivered_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
func (s *Store) ReleaseReviewSignal(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM review_signals WHERE id=$1 AND delivered_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// --- Rules ---

func (s *Store) ListRules(ctx context.Context, installationIDs []int64) ([]Rule, error) {
	rows, err := s.q.ListRules(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, Rule{ID: row.ID, InstallationID: row.InstallationID, Category: row.Category, Content: row.Content, Priority: row.Priority, Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	return rules, nil
}

func (s *Store) CreateRule(ctx context.Context, installationID int64, category, content string, priority int, enabled bool) (*Rule, error) {
	var rule Rule
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		err := tx.QueryRow(ctx, `
			INSERT INTO rules (installation_id, category, content, priority, enabled)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at`,
			installationID, category, content, priority, enabled).
			Scan(&rule.ID, &rule.InstallationID, &rule.Category, &rule.Content, &rule.Priority, &rule.Enabled, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return newRuleMirrorEvent(rule, enabled)
	})
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Store) UpdateRule(ctx context.Context, id int64, installationIDs []int64, category, content *string, priority *int, enabled *bool) (*Rule, error) {
	var rule Rule
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		err := tx.QueryRow(ctx, `
			UPDATE rules SET
				category = COALESCE($3, category), content = COALESCE($4, content),
				priority = COALESCE($5, priority), enabled = COALESCE($6, enabled), updated_at = NOW()
			WHERE id = $1 AND installation_id = ANY($2::bigint[])
			RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at`,
			id, installationIDs, category, content, priority, enabled).
			Scan(&rule.ID, &rule.InstallationID, &rule.Category, &rule.Content, &rule.Priority, &rule.Enabled, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return newRuleMirrorEvent(rule, rule.Enabled)
	})
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Store) DeleteRule(ctx context.Context, id int64, installationIDs []int64) error {
	return s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		var installationID int64
		err := tx.QueryRow(ctx, `
			DELETE FROM rules WHERE id = $1 AND installation_id = ANY($2::bigint[])
			RETURNING installation_id`, id, installationIDs).Scan(&installationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryMirrorEvent{}, fmt.Errorf("rule %d not found", id)
		}
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		payload, err := json.Marshal(map[string]string{"custom_id": fmt.Sprintf("rule--%d", id)})
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return MemoryMirrorEvent{InstallationID: installationID, AggregateType: MemoryMirrorRule, AggregateID: id, Operation: MemoryMirrorDelete, Payload: payload}, nil
	})
}

func newRuleMirrorEvent(rule Rule, enabled bool) (MemoryMirrorEvent, error) {
	customID := fmt.Sprintf("rule--%d", rule.ID)
	operation := MemoryMirrorDelete
	var payload any = map[string]string{"custom_id": customID}
	if enabled {
		type ruleBody struct {
			RuleID   int64
			Category string
			Priority int
			Content  string
		}
		payload = struct {
			CustomID string   `json:"custom_id"`
			Enabled  bool     `json:"enabled"`
			Rule     ruleBody `json:"rule"`
		}{
			CustomID: customID,
			Enabled:  true,
			Rule: ruleBody{
				RuleID: rule.ID, Category: rule.Category,
				Priority: rule.Priority, Content: rule.Content,
			},
		}
		operation = MemoryMirrorUpsert
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return MemoryMirrorEvent{}, fmt.Errorf("marshal rule mirror payload: %w", err)
	}
	return MemoryMirrorEvent{InstallationID: *rule.InstallationID, AggregateType: MemoryMirrorRule, AggregateID: rule.ID, Operation: operation, Payload: raw}, nil
}

// --- Model Configs ---

func (s *Store) ListModelConfigs(ctx context.Context, repoID int64) ([]ModelConfig, error) {
	rows, err := s.q.ListModelConfigs(ctx, &repoID)
	if err != nil {
		return nil, err
	}
	configs := make([]ModelConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, modelConfigFromValues(row.ID, row.RepoID, nil, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt))
	}
	return configs, nil
}

func (s *Store) UpsertModelConfig(ctx context.Context, repoID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (*ModelConfig, error) {
	row, err := s.q.UpsertModelConfig(ctx, db.UpsertModelConfigParams{RepoID: &repoID, Stage: stage, Provider: provider, Model: model, BaseURL: baseURL, MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return nil, err
	}
	config := modelConfigFromValues(row.ID, row.RepoID, nil, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt)
	return &config, nil
}

func (s *Store) DeleteModelConfig(ctx context.Context, repoID int64, stage string) error {
	count, err := s.q.DeleteModelConfig(ctx, db.DeleteModelConfigParams{RepoID: &repoID, Stage: stage})
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("config not found for repo %d stage %s", repoID, stage)
	}
	return nil
}

// ListOrgModelConfigs returns installation-level model configs (repo_id IS NULL).
func (s *Store) ListOrgModelConfigs(ctx context.Context, installationID int64) ([]ModelConfig, error) {
	rows, err := s.q.ListOrgModelConfigs(ctx, &installationID)
	if err != nil {
		return nil, err
	}
	configs := make([]ModelConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, modelConfigFromValues(row.ID, row.RepoID, row.InstallationID, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt))
	}
	return configs, nil
}

// UpsertOrgModelConfig saves an installation-level model config.
func (s *Store) UpsertOrgModelConfig(ctx context.Context, installationID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (*ModelConfig, error) {
	row, err := s.q.UpsertOrgModelConfig(ctx, db.UpsertOrgModelConfigParams{InstallationID: &installationID, Stage: stage, Provider: provider, Model: model, BaseURL: baseURL, MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return nil, err
	}
	config := modelConfigFromValues(row.ID, row.RepoID, row.InstallationID, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt)
	return &config, nil
}

// DeleteOrgModelConfig removes an installation-level config.
func (s *Store) DeleteOrgModelConfig(ctx context.Context, installationID int64, stage string) error {
	count, err := s.q.DeleteOrgModelConfig(ctx, db.DeleteOrgModelConfigParams{InstallationID: &installationID, Stage: stage})
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("org config not found for installation %d stage %s", installationID, stage)
	}
	return nil
}

// ListModelConfigsWithFallback returns repo configs, falling back to org configs for missing stages.
func (s *Store) ListModelConfigsWithFallback(ctx context.Context, installationID, repoID int64) ([]ModelConfig, error) {
	rows, err := s.q.ListModelConfigsWithFallback(ctx, db.ListModelConfigsWithFallbackParams{InstallationID: &installationID, RepoID: &repoID})
	if err != nil {
		return nil, err
	}
	configs := make([]ModelConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, modelConfigFromValues(row.ID, row.RepoID, row.InstallationID, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt))
	}
	return configs, nil
}

// --- Review Comments ---

func (s *Store) CreateReviewComment(ctx context.Context, reviewID uuid.UUID, attemptGeneration int, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist, codeSnippet *string, confidenceScore *int, githubCommentID *int64, matchedPatternID *int64, matchedPatternScore *float32, enforcedRuleContent *string, isNewFinding bool, suppressedReason *string, state FindingState) error {
	if state == "" {
		state = FindingStatePosted
	}
	return s.q.CreateReviewComment(ctx, db.CreateReviewCommentParams{
		ReviewID: reviewID, AttemptGeneration: attemptGeneration, FilePath: filePath, StartLine: startLine, EndLine: endLine, Side: side,
		Body: body, Severity: severity, Category: category, Specialist: specialist,
		ConfidenceScore: confidenceScore, CodeSnippet: codeSnippet, GithubCommentID: githubCommentID,
		MatchedPatternID: matchedPatternID, MatchedPatternScore: matchedPatternScore,
		EnforcedRuleContent: enforcedRuleContent, IsNewFinding: &isNewFinding,
		SuppressedReason: suppressedReason, State: string(state),
	})
}

// GetCommentByGithubID looks up a review comment by its GitHub comment ID.
// ListPRGithubCommentIDs returns the GitHub comment IDs of every Argus-posted
// review comment on the given PR, across all completed reviews. Used by the
// reaction sweep to know which comments to fetch reactions for. Deduped by
// github_comment_id in case we somehow posted the same comment twice.
//
// Filters to completed reviews only — a pending or failed review may have
// written comment rows but not yet posted them to GitHub, so their IDs would
// be stale or absent. Belt-and-suspenders since we also require the ID to
// be non-NULL.
func (s *Store) ListPRGithubCommentIDs(ctx context.Context, repoFullName string, prNumber int) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT rc.github_comment_id
		FROM review_comments rc
		JOIN reviews rv ON rc.review_id = rv.id
		JOIN repos r ON rv.repo_id = r.id
		WHERE r.full_name = $1 AND rv.pr_number = $2
		  AND rv.status = 'completed'
		  AND rc.attempt_generation = rv.attempt_generation
		  AND rc.github_comment_id IS NOT NULL
	`, repoFullName, prNumber)
	if err != nil {
		return nil, fmt.Errorf("listing PR comment ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id *int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning comment id: %w", err)
		}
		if id != nil {
			ids = append(ids, *id)
		}
	}
	return ids, rows.Err()
}

func (s *Store) GetCommentByGithubID(ctx context.Context, githubCommentID int64) (*ReviewComment, error) {
	row, err := s.q.GetCommentByGithubID(ctx, &githubCommentID)
	if err != nil {
		return nil, err
	}
	comment, err := githubReviewCommentFromSQLC(row)
	if err != nil {
		return nil, err
	}
	return &comment, nil
}

// UnboundComment is one posted review_comments row still awaiting its GitHub
// REST comment id (github_comment_id backfill). Line is end_line (the posted
// anchor); Body is the exact stored comment body used to disambiguate two
// findings that share a (path, line).
type UnboundComment struct {
	ID       uuid.UUID
	FilePath string
	Line     int
	Body     string
}

// ListUnboundReviewComments returns the review's posted, line-anchored comments
// that don't yet have a github_comment_id. File-level rows (NULL end_line) are
// excluded — they carry no line to bind on, matching the historical backfill
// key (review_id, file_path, end_line). Suppressed rows (suppressed_reason set)
// are excluded too: they were never posted to GitHub, so binding a posted
// comment's id to one via the order-fallback would be wrong. Ordered by
// created_at so same-line ties bind in insertion order (the submission order).
func (s *Store) ListUnboundReviewComments(ctx context.Context, reviewID uuid.UUID) ([]UnboundComment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, file_path, end_line, body
		FROM review_comments
		WHERE review_id = $1 AND github_comment_id IS NULL AND end_line IS NOT NULL
		  AND attempt_generation = (SELECT attempt_generation FROM reviews WHERE id=$1)
		  AND suppressed_reason IS NULL
		ORDER BY created_at, id
	`, reviewID)
	if err != nil {
		return nil, fmt.Errorf("listing unbound review comments: %w", err)
	}
	defer rows.Close()
	var out []UnboundComment
	for rows.Next() {
		var c UnboundComment
		if err := rows.Scan(&c.ID, &c.FilePath, &c.Line, &c.Body); err != nil {
			return nil, fmt.Errorf("scanning unbound review comment: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// BindGitHubCommentID binds exactly one review_comments row to its GitHub REST
// comment id. Scoped to id (the finding's PK) and guarded by
// github_comment_id IS NULL so a replayed backfill can't re-point an already
// bound row. Returns whether the row was updated. This replaces the old fuzzy
// (review_id, file_path, end_line) UPDATE that collapsed two same-line findings
// onto a single id (see FindingLifecycle #165 same-line binding fix).
func (s *Store) BindGitHubCommentID(ctx context.Context, commentID uuid.UUID, githubCommentID int64) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE review_comments SET github_comment_id = $2
		WHERE id = $1 AND github_comment_id IS NULL
	`, commentID, githubCommentID)
	if err != nil {
		return false, fmt.Errorf("binding github comment id: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RepoReviewStats holds averaged token / cost figures used to estimate the
// cost of a pending review when rendering the "Trigger review" checkbox.
type RepoReviewStats struct {
	SampleSize    int
	AvgTokens     int64
	AvgCost       float64
	CostAvailable bool
}

// GetRepoReviewStats averages tokens + cost across the last `limit` completed
// reviews for a repo. CostAvailable is false when no sampled review recorded a
// positive USD cost (e.g., OSS provider, missing pricing metadata) — callers
// should render tokens only in that case.
//
// Non-fatal: returns a zero-value RepoReviewStats on error with no sample_size
// so the caller can fall through to generic messaging.
func (s *Store) GetRepoReviewStats(ctx context.Context, repoID int64, limit int) (RepoReviewStats, error) {
	row, err := s.q.GetRepoReviewStats(ctx, db.GetRepoReviewStatsParams{RepoID: repoID, RowLimit: int64(limit)})
	return RepoReviewStats{SampleSize: row.SampleSize, AvgTokens: row.AvgTokens, AvgCost: row.AvgCost, CostAvailable: row.CostAvailable}, err
}

// GetLastCompletedReview returns the most recent completed review for a repo+PR.
func (s *Store) GetLastCompletedReview(ctx context.Context, repoID int64, prNumber int) (*Review, error) {
	row, err := s.q.GetLastCompletedReview(ctx, db.GetLastCompletedReviewParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	review := lastCompletedReviewFromSQLC(row)
	return &review, nil
}

func (s *Store) GetLatestReviewBySHA(ctx context.Context, repoFullName string, prNumber int, headSHA string) (*Review, error) {
	row, err := s.q.GetLatestReviewBySHA(ctx, db.GetLatestReviewBySHAParams{FullName: repoFullName, PRNumber: prNumber, HeadSHA: headSHA})
	if err != nil {
		return nil, err
	}
	review := latestReviewBySHAFromSQLC(row)
	return &review, nil
}

// HasFailedReviewWithError returns true if a review with status='failed' and the
// given error code already exists for this PR. Used by the readiness gate to
// suppress duplicate "welcome to Argus" comments when users retry
// `@argus-eye review` while the API key is still missing.
func (s *Store) HasFailedReviewWithError(ctx context.Context, repoID int64, prNumber int, errorCode string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM reviews
			WHERE repo_id = $1 AND pr_number = $2 AND status = 'failed' AND error = $3
		)
	`, repoID, prNumber, errorCode).Scan(&exists)
	return exists, err
}

// GetLatestReviewByPR returns the most recent completed review for a repo+PR by full name.
func (s *Store) GetLatestReviewByPR(ctx context.Context, repoFullName string, prNumber int) (*Review, error) {
	row, err := s.q.GetLatestReviewByPR(ctx, db.GetLatestReviewByPRParams{FullName: repoFullName, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	review := latestReviewByPRFromSQLC(row)
	return &review, nil
}

// --- Stats ---

// CriticalFinds in both GetStats and GetStatsScoped excludes state='suppressed'.
// Those findings were generated and then withheld by the suppression pass, so no
// PR author ever received them; counting them made the dashboard advertise
// review coverage that was never delivered (#239). The same predicate must stay
// on the sqlc mirrors in sqlc/query/stats.sql — the sqlc migration swaps one
// implementation for the other, and a fix on only one half is the exact failure
// this issue is a follow-up to.
func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	row, err := s.q.GetStats(ctx)
	if err != nil {
		return nil, err
	}
	stats := Stats{TotalReviews: row.TotalReviews, CompletedToday: row.CompletedToday, AvgScore: row.AvgScore, ActiveRepos: row.ActiveRepos, CriticalFinds: row.CriticalFinds, PendingReviews: row.PendingReviews, CatchRate: row.CatchRate, PRsThisWeek: row.PrsThisWeek, HighRiskCount: row.HighRiskCount, AvgReviewTimeMs: row.AvgReviewTimeMs, DeepReviewCount: row.DeepReviewCount}
	return &stats, nil
}

func (s *Store) GetStatsScoped(ctx context.Context, installationIDs []int64) (*Stats, error) {
	row, err := s.q.GetStatsScoped(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	stats := Stats{TotalReviews: row.TotalReviews, CompletedToday: row.CompletedToday, AvgScore: row.AvgScore, ActiveRepos: row.ActiveRepos, CriticalFinds: row.CriticalFinds, PendingReviews: row.PendingReviews, CatchRate: row.CatchRate, PRsThisWeek: row.PrsThisWeek, HighRiskCount: row.HighRiskCount, AvgReviewTimeMs: row.AvgReviewTimeMs, DeepReviewCount: row.DeepReviewCount}
	return &stats, nil
}

// --- Activity ---

func (s *Store) ListActivity(ctx context.Context, installationIDs []int64, limit int) ([]ActivityLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.ListActivity(ctx, db.ListActivityParams{Column1: installationIDs, RowLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	activity := make([]ActivityLog, 0, len(rows))
	for _, row := range rows {
		activity = append(activity, ActivityLog{ID: row.ID, InstallationID: row.InstallationID, Action: row.Action, Actor: row.Actor, Resource: row.Resource, Metadata: row.Metadata, CreatedAt: row.CreatedAt})
	}
	return activity, nil
}

func (s *Store) LogActivity(ctx context.Context, installationID *int64, action, actor, resource string, metadata []byte) error {
	return s.q.LogActivity(ctx, db.LogActivityParams{InstallationID: installationID, Action: action, Actor: nilIfEmpty(actor), Resource: nilIfEmpty(resource), Metadata: metadata})
}

// --- Auto-Resolve Events ---

// InsertAutoResolveEventParams mirrors the sqlc-generated params for the
// underlying INSERT but lives in the store package so higher layers
// (pipeline, api handlers) don't import internal/store/db directly.
//
// ResolvedThreadKeys is the flat list of "<path>:<line>" keys for each
// thread the goroutine actually resolved on this push. Added in migration
// 041 so the async cross-PR stage can filter prior findings on the same
// key shape (Finding.Path + Finding.Line). Empty slice is legal — means
// "no thread-level detail captured", indistinguishable from the pre-041
// aggregate-only rows.
type InsertAutoResolveEventParams struct {
	InstallationID     int64
	RepoID             int64
	PRNumber           int
	SourceSHA          string
	ResolvedCount      int
	AttemptedCount     int
	GitHubAPICalls     int
	ResolvedThreadKeys []string
}

// InsertAutoResolveEvent records one fire of the auto-resolve goroutine
// against a synchronize push. Called from the pipeline orchestrator
// after it has finished resolving (or attempting) threads on a PR.
//
// Writes are best-effort: callers already use a short DB-only context
// so that a slow GitHub path doesn't leak into this insert, and a lost
// row here is a dropped stats datapoint — not a correctness issue.
func (s *Store) InsertAutoResolveEvent(ctx context.Context, p InsertAutoResolveEventParams) error {
	// ON CONFLICT DO NOTHING guards against GitHub's webhook-retry
	// behavior — a retried synchronize delivery would otherwise double-
	// count the same resolve activity against the unique (installation,
	// repo, pr, sha) key.
	// Coerce nil slice to empty: pgx serializes nil []string as SQL NULL,
	// but migration 041 declared resolved_thread_keys NOT NULL DEFAULT '{}'.
	// The DEFAULT only applies when the column is omitted from the INSERT;
	// an explicit NULL (from a nil slice) violates the constraint.
	keys := p.ResolvedThreadKeys
	if keys == nil {
		keys = []string{}
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO auto_resolve_events
			(installation_id, repo_id, pr_number, source_sha,
			 resolved_count, attempted_count, github_api_calls,
			 resolved_thread_keys)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (installation_id, repo_id, pr_number, source_sha)
		DO NOTHING
	`, p.InstallationID, p.RepoID, p.PRNumber, p.SourceSHA,
		p.ResolvedCount, p.AttemptedCount, p.GitHubAPICalls,
		keys)
	if err != nil {
		return fmt.Errorf("insert auto_resolve_events: %w", err)
	}
	return nil
}

// GetAutoResolveStatsRow returns aggregated auto-resolve activity over
// a period for one or more installations (scoped via the API layer).
type GetAutoResolveStatsRow struct {
	EventCount     int
	ResolvedTotal  int
	AttemptedTotal int
	APICallsTotal  int
}

// GetAutoResolveStats sums auto_resolve_events for the given installations
// over the given period (e.g. "30 days"). Used by the stats overview
// handler.
func (s *Store) GetAutoResolveStats(ctx context.Context, installationIDs []int64, period string) (GetAutoResolveStatsRow, error) {
	var r GetAutoResolveStatsRow
	err := s.Pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COALESCE(SUM(resolved_count), 0)::int,
		  COALESCE(SUM(attempted_count), 0)::int,
		  COALESCE(SUM(github_api_calls), 0)::int
		FROM auto_resolve_events
		WHERE installation_id = ANY($1::bigint[])
		  AND created_at >= NOW() - $2::interval
	`, installationIDs, period).Scan(&r.EventCount, &r.ResolvedTotal, &r.AttemptedTotal, &r.APICallsTotal)
	if err != nil {
		return r, fmt.Errorf("get auto_resolve_events stats: %w", err)
	}
	return r, nil
}

// GetLearnLayerCountsRow returns counts of new rows in the learn-layer
// tables for the stats "Learn layer" section.
type GetLearnLayerCountsRow struct {
	PatternsLearned int
	ScenariosStored int
	DecisionTraces  int
	FeedbackIndexed int
}

// GetLearnLayerCounts returns new-rows-this-period across the four
// learn-layer tables. Uses four correlated subqueries rather than UNION
// ALL so the caller gets a single flat row and the planner treats each
// count independently.
func (s *Store) GetLearnLayerCounts(ctx context.Context, installationIDs []int64, period string) (GetLearnLayerCountsRow, error) {
	var r GetLearnLayerCountsRow
	err := s.Pool.QueryRow(ctx, `
		SELECT
		  COALESCE((
		    SELECT COUNT(*) FROM patterns p
		    WHERE p.installation_id = ANY($1::bigint[])
		      AND p.created_at >= NOW() - $2::interval
		  ), 0)::int,
		  COALESCE((
		    SELECT COUNT(*) FROM scenarios s
		    WHERE s.installation_id = ANY($1::bigint[])
		      AND s.created_at >= NOW() - $2::interval
		  ), 0)::int,
		  COALESCE((
		    SELECT COUNT(*) FROM decision_traces dt
		    JOIN repos rp ON dt.repo_id = rp.id
		    WHERE rp.installation_id = ANY($1::bigint[])
		      AND dt.created_at >= NOW() - $2::interval
		  ), 0)::int,
		  COALESCE((
		    SELECT COUNT(*) FROM comment_outcomes co
		    JOIN review_comments rc ON co.review_comment_id = rc.id
		    JOIN reviews rv ON rc.review_id = rv.id
		    JOIN repos rp ON rv.repo_id = rp.id
		    WHERE rp.installation_id = ANY($1::bigint[])
		      AND co.created_at >= NOW() - $2::interval
		  ), 0)::int
	`, installationIDs, period).Scan(
		&r.PatternsLearned, &r.ScenariosStored, &r.DecisionTraces, &r.FeedbackIndexed,
	)
	if err != nil {
		return r, fmt.Errorf("get learn-layer counts: %w", err)
	}
	return r, nil
}

// --- Comment Outcomes ---

// RecordCommentOutcome records a (comment, outcome) signal idempotently and
// reports whether this call actually inserted a new row. inserted=false means
// the outcome was already recorded — the reaction sweep replays on every PR
// event, so callers must gate side effects (e.g. bumping pattern quality) on
// inserted to avoid double-counting a single 👍/👎.
func (s *Store) RecordCommentOutcome(ctx context.Context, reviewCommentID uuid.UUID, outcome string) (inserted bool, err error) {
	count, err := s.q.RecordCommentOutcome(ctx, db.RecordCommentOutcomeParams{ReviewCommentID: reviewCommentID, Outcome: outcome})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetScenarioMemoryDocID records the memory customID for a scenario in
// migration 045's mirror column. The pipeline calls this after a successful
// IndexScenario so a NULL memory_doc_id genuinely means "write failed / pending
// reconciliation" instead of "never attempted" — otherwise the reconciler treats
// every freshly-created scenario as drift forever.
func (s *Store) SetScenarioMemoryDocID(ctx context.Context, id int64, memoryDocID string) error {
	return s.q.UpdateScenarioMemoryDocID(ctx, db.UpdateScenarioMemoryDocIDParams{
		MemoryDocID: &memoryDocID,
		ID:          id,
	})
}

func (s *Store) GetCommentOutcomes(ctx context.Context, reviewCommentID uuid.UUID) ([]CommentOutcome, error) {
	rows, err := s.q.GetCommentOutcomes(ctx, reviewCommentID)
	if err != nil {
		return nil, err
	}
	outcomes := make([]CommentOutcome, 0, len(rows))
	for _, row := range rows {
		if row.CreatedAt == nil {
			return nil, fmt.Errorf("comment outcome %d has NULL created_at", row.ID)
		}
		outcomes = append(outcomes, CommentOutcome{ID: int64(row.ID), ReviewCommentID: row.ReviewCommentID, Outcome: row.Outcome, CreatedAt: *row.CreatedAt})
	}
	return outcomes, nil
}

// --- Gauge (address-rate telemetry) ---

// PostedFinding is one GitHub-posted review comment eligible for merge-time
// address detection: its anchor (path + line), when it was posted, and the
// head SHA the review ran against (the compare base for "commits made after
// the comment").
type PostedFinding struct {
	ID       uuid.UUID
	FilePath string
	Line     int
	PostedAt time.Time
	HeadSHA  string
}

// ListPostedFindings returns the posted (never-suppressed, actually-on-GitHub)
// findings for a PR that don't yet have a merge-time outcome. Reaction-driven
// outcomes ('confirmed'/'dismissed') do NOT exclude a finding — a dismissed
// finding can still be addressed; the view weighs both signals.
func (s *Store) ListPostedFindings(ctx context.Context, repoID int64, prNumber int) ([]PostedFinding, error) {
	rows, err := s.q.ListPostedFindings(ctx, db.ListPostedFindingsParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	findings := make([]PostedFinding, 0, len(rows))
	for _, row := range rows {
		findings = append(findings, PostedFinding{ID: row.ID, FilePath: row.FilePath, Line: row.Line, PostedAt: row.PostedAt, HeadSHA: row.HeadSHA})
	}
	return findings, nil
}

// RecordFindingOutcome writes a merge-time outcome for a posted finding,
// idempotently (webhook redeliveries replay the same (comment, outcome)).
// addressedAt is nil for 'ignored'/'deferred'.
func (s *Store) RecordFindingOutcome(ctx context.Context, reviewCommentID uuid.UUID, outcome string, addressedAt *time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO comment_outcomes (review_comment_id, outcome, addressed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (review_comment_id, outcome) DO NOTHING
	`, reviewCommentID, outcome, addressedAt)
	return err
}

// ListReviewGauge reads vw_review_gauge scoped to the given installations.
func (s *Store) ListReviewGauge(ctx context.Context, installationIDs []int64) ([]GaugeRow, error) {
	rows, err := s.q.ListReviewGauge(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	gauge := make([]GaugeRow, 0, len(rows))
	for _, row := range rows {
		gauge = append(gauge, GaugeRow{
			InstallationID: row.InstallationID, Category: row.Category, ChangeClass: row.ChangeClass,
			PostedFindings: row.PostedFindings, AddressedHuman: row.AddressedHuman,
			AddressedAgent: row.AddressedAgent, Dismissed: row.Dismissed, Ignored: row.Ignored,
			Deferred: row.Deferred, AddressRate: row.AddressRate, DismissRate: row.DismissRate,
			MedianSecondsToMerge: row.MedianSecondsToMerge,
		})
	}
	return gauge, nil
}

// --- Prompt Templates ---

func (s *Store) ListPromptTemplates(ctx context.Context, repoID int64) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, repoID)
	if err != nil {
		return nil, err
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		template, err := promptTemplateFromSQLC(row)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, nil
}

func (s *Store) UpsertPromptTemplate(ctx context.Context, repoID int64, stage, promptText string) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, db.UpsertPromptTemplateParams{RepoID: repoID, Stage: stage, PromptText: promptText})
	if err != nil {
		return nil, err
	}
	template, err := promptTemplateFromSQLC(row)
	if err != nil {
		return nil, err
	}
	return &template, nil
}

func (s *Store) DeletePromptTemplate(ctx context.Context, repoID int64, stage string) error {
	count, err := s.q.DeletePromptTemplate(ctx, db.DeletePromptTemplateParams{RepoID: repoID, Stage: stage})
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("prompt template not found for repo %d stage %s", repoID, stage)
	}
	return nil
}

// RecoverStaleReviews marks old in-progress/pending reviews as failed.
func (s *Store) RecoverStaleReviews(ctx context.Context, maxAge time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = 'failed', error = 'review timed out — server restarted',
		       completed_at = NOW()
		WHERE status IN ('pending', 'in_progress')
		  AND created_at < NOW() - make_interval(secs => $1)
	`, float64(maxAge.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("recovering stale reviews: %w", err)
	}
	return tag.RowsAffected(), nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Consumer-facing sqlc pass-throughs (issue #137) ---
//

// Cross-PR generated-query wrappers keep sqlc behind the Store boundary.
func (s *Store) GetLatestRunForReview(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error) {
	return s.q.GetLatestRunForReview(ctx, reviewID)
}

func (s *Store) FindReviewsLinkingToPR(ctx context.Context, arg db.FindReviewsLinkingToPRParams) ([]db.FindReviewsLinkingToPRRow, error) {
	return s.q.FindReviewsLinkingToPR(ctx, arg)
}

func (s *Store) SetReviewLinkedPRRefs(ctx context.Context, arg db.SetReviewLinkedPRRefsParams) error {
	return s.q.SetReviewLinkedPRRefs(ctx, arg)
}

func (s *Store) SetReviewLinkedIssueRefs(ctx context.Context, arg db.SetReviewLinkedIssueRefsParams) error {
	return s.q.SetReviewLinkedIssueRefs(ctx, arg)
}

func (s *Store) UpdateReviewCrossPRHash(ctx context.Context, arg db.UpdateReviewCrossPRHashParams) error {
	return s.q.UpdateReviewCrossPRHash(ctx, arg)
}

func (s *Store) GetLatestCompletedReviewByPR(ctx context.Context, arg db.GetLatestCompletedReviewByPRParams) (db.GetLatestCompletedReviewByPRRow, error) {
	return s.q.GetLatestCompletedReviewByPR(ctx, arg)
}

func (s *Store) FindSharedLinkedIssues(ctx context.Context, reviewID uuid.UUID) ([]db.FindSharedLinkedIssuesRow, error) {
	return s.q.FindSharedLinkedIssues(ctx, reviewID)
}

func (s *Store) MergeStageTokenEntry(ctx context.Context, arg db.MergeStageTokenEntryParams) (int64, error) {
	return s.q.MergeStageTokenEntry(ctx, arg)
}

// These thin wrappers keep generated query types behind Store methods used
// to leak out of the store package into orchestrator stages and API handlers.
// Routing them through *store.Store keeps callers (and the consumer-declared
// narrow interfaces over the store) off the generated query layer. Pure
// delegation — no business logic. SetScenarioMemoryDocID above is the same
// idiom; grouped here so the former leaks are reviewable in one place.

// GetInstallationFeatureFlags returns the raw feature_flags JSONB for an
// installation. Callers parse it (pipeline.loadFeatureFlags, the features
// handler); an empty or "{}" payload means "all defaults".
func (s *Store) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error) {
	return s.q.GetInstallationFeatureFlags(ctx, installationID)
}

// MergeInstallationFeatureFlags merges the given keys into the feature_flags
// JSONB, leaving every other key intact. Merging in SQL rather than
// read-modify-writing in Go keeps it atomic: the settings form owns three
// keys while operators set others by direct UPDATE, and a
// lost update between those two writers reverts a backend flip silently.
func (s *Store) MergeInstallationFeatureFlags(ctx context.Context, installationID int64, patch json.RawMessage) error {
	return s.q.MergeInstallationFeatureFlags(ctx, db.MergeInstallationFeatureFlagsParams{
		ID:    installationID,
		Patch: patch,
	})
}

// GetAllFileReviewsForReview returns the unfiltered per-file review payload
// (pre dedup/scoring) recorded for a review's latest run, as raw JSONB. The
// export path uses it to surface dropped findings.
func (s *Store) GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (json.RawMessage, error) {
	return s.q.GetAllFileReviewsForReview(ctx, reviewID)
}

// GetTopChokePoints returns the highest fan-in files for a repo (up to limit) —
// the architecture-summary input.
func (s *Store) GetTopChokePoints(ctx context.Context, repoID int64, limit int32) ([]db.GetTopChokePointsRow, error) {
	return s.q.GetTopChokePoints(ctx, db.GetTopChokePointsParams{RepoID: repoID, Limit: limit})
}

// ListArchNodes returns the per-symbol architecture rows (file, name, language,
// line span) for a repo.
func (s *Store) ListArchNodes(ctx context.Context, repoID int64) ([]db.ListArchNodesRow, error) {
	return s.q.ListArchNodes(ctx, repoID)
}

// ListArchFileEdges returns the file→file dependency edges for a repo.
func (s *Store) ListArchFileEdges(ctx context.Context, repoID int64) ([]db.ListArchFileEdgesRow, error) {
	return s.q.ListArchFileEdges(ctx, repoID)
}

// ListArchBugDensity returns per-file bug counts and PR-change frequency for a repo.
func (s *Store) ListArchBugDensity(ctx context.Context, repoID int64) ([]db.ListArchBugDensityRow, error) {
	return s.q.ListArchBugDensity(ctx, repoID)
}

// ListArchCoupling returns per-PR file sets used to derive temporal coupling.
func (s *Store) ListArchCoupling(ctx context.Context, repoID int64) ([]db.ListArchCouplingRow, error) {
	return s.q.ListArchCoupling(ctx, repoID)
}

// ListGraphNodes returns the code-graph nodes for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphNodes(ctx context.Context, repoID int64) ([]db.ListGraphNodesRow, error) {
	rows, err := s.q.ListGraphNodes(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphNodesRow{}
	}
	return rows, err
}

// ListGraphEdges returns the code-graph edges for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphEdges(ctx context.Context, repoID int64) ([]db.ListGraphEdgesRow, error) {
	rows, err := s.q.ListGraphEdges(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphEdgesRow{}
	}
	return rows, err
}
