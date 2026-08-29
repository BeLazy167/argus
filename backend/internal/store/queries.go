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

func (s *Store) CreateInstallation(ctx context.Context, installationID int64, orgLogin string) (storeResult0 *Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateInstallation",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.CreateInstallation(ctx, db.CreateInstallationParams{InstallationID: installationID, OrgLogin: orgLogin})
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) ListInstallations(ctx context.Context) (storeResult0 []Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListInstallations")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0,
			)

			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) LinkUserInstallation(ctx context.Context, clerkUserID string, installationID int64, role string) (storeResult0 *UserInstallation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "LinkUserInstallation",

			"clerk_user_id",

			storeLogValue(clerkUserID), "installation_id",
			storeLogValue(installationID), "role", storeLogValue(role))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) IsUserLinkedToInstallation(ctx context.Context, clerkUserID string, installationID int64) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "IsUserLinkedToInstallation",

			"clerk_user_id", storeLogValue(clerkUserID), "installation_id",
			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) CountInstallationUsers(ctx context.Context, installationID int64) (storeResult0 int, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CountInstallationUsers",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {

			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM user_installations WHERE installation_id = $1`, installationID).Scan(&count)
	return count, err
}

func (s *Store) ListUserInstallations(ctx context.Context, clerkUserID string) (storeResult0 []Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListUserInstallations",

			"clerk_user_id",

			storeLogValue(clerkUserID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) GetUserInstallationIDs(ctx context.Context, clerkUserID string) (storeResult0 []int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetUserInstallationIDs",

			"clerk_user_id",

			storeLogValue(clerkUserID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	rows, err := s.q.GetUserInstallationIDs(ctx, clerkUserID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	copy(ids, rows)
	return ids, nil
}

func (s *Store) GetInstallation(ctx context.Context, id int64) (storeResult0 *Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetInstallation",

			"id",
			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered,

				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetInstallation(ctx, id)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) GetInstallationByGitHubID(ctx context.Context, ghInstallationID int64) (storeResult0 *Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetInstallationByGitHubID",

			"gh_installation_id", storeLogValue(ghInstallationID))
	defer func() {
		if recovered := recover(); recovered !=

			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetInstallationByGitHubID(ctx, ghInstallationID)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) GetInstallationByClerkOrgID(ctx context.Context, clerkOrgID string) (storeResult0 *Installation, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetInstallationByClerkOrgID",

			"clerk_org_id", storeLogValue(clerkOrgID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetInstallationByClerkOrgID(ctx, &clerkOrgID)
	if err != nil {
		return nil, err
	}
	installation := installationFromSQLC(row.ID, row.InstallationID, row.OrgLogin, row.ClerkOrgID, row.CreatedAt, row.SuspendedAt)
	return &installation, nil
}

func (s *Store) CountReviewsThisMonth(ctx context.Context, installationID int64) (storeResult0 int, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CountReviewsThisMonth",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {

			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.CountReviewsThisMonth(ctx, installationID)
}

func (s *Store) CountEnabledRepos(ctx context.Context, installationID int64) (storeResult0 int, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CountEnabledRepos",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.CountEnabledRepos(ctx, installationID)
}

func (s *Store) SuspendInstallation(ctx context.Context, id int64) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SuspendInstallation",

			"id",

			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.SuspendInstallation(ctx, id)
}

func (s *Store) SetInstallationClerkOrgID(ctx context.Context, installationID int64, clerkOrgID string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetInstallationClerkOrgID",

			"installation_id", storeLogValue(installationID), "clerk_org_id",
			storeLogValue(
				clerkOrgID))
	defer func() {

		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.SetInstallationClerkOrgID(ctx, db.SetInstallationClerkOrgIDParams{ClerkOrgID: &clerkOrgID, ID: installationID})
}

// --- Org Default Settings ---

func (s *Store) GetOrgDefaults(ctx context.Context, installationID int64) (storeResult0 json.RawMessage, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetOrgDefaults",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.GetOrgDefaults(ctx, installationID)
}

func (s *Store) SetOrgDefaults(ctx context.Context, installationID int64, settings json.RawMessage) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetOrgDefaults",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.SetOrgDefaults(ctx, db.SetOrgDefaultsParams{DefaultSettings: settings, ID: installationID})
}

// GetMergedSettings returns org defaults merged with repo overrides (repo wins).
func (s *Store) GetMergedSettings(ctx context.Context, installationID int64, repoID int64) (storeResult0 json.RawMessage, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetMergedSettings",

			"installation_id",

			storeLogValue(installationID), "repo_id", storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) ListRepos(ctx context.Context) (storeResult0 []Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListRepos")
	defer func() {

		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(
				recovered,
			)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) ListReposByOwner(ctx context.Context, ownerPrefix string) (storeResult0 []Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListReposByOwner")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)

			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) GetRepo(ctx context.Context, id int64) (storeResult0 *Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetRepo",

			"id", storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered,

				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetRepo(ctx, id)
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) GetRepoByFullName(ctx context.Context, fullName string) (storeResult0 *Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetRepoByFullName",

			"full_name",

			storeLogValue(fullName))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(
				storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetRepoByFullName(ctx, fullName)
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) UpdateRepo(ctx context.Context, id int64, enabled *bool, defaultBranch *string, settingsJSON []byte) (storeResult0 *Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateRepo",

			"id",
			storeLogValue(id), "enabled", storeLogValue(enabled))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.UpdateRepo(ctx, db.UpdateRepoParams{ID: id, Enabled: enabled, DefaultBranch: defaultBranch, SettingsJSON: settingsJSON})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) UpsertRepo(ctx context.Context, installationID, githubID int64, fullName, defaultBranch string) (storeResult0 *Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpsertRepo",

			"installation_id",

			storeLogValue(installationID), "github_id", storeLogValue(githubID), "full_name",
			storeLogValue(fullName),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	row, err := s.q.UpsertRepo(ctx, db.UpsertRepoParams{InstallationID: installationID, GithubID: githubID, FullName: fullName, DefaultBranch: defaultBranch})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

func (s *Store) ListReposScoped(ctx context.Context, installationIDs []int64) (storeResult0 []Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListReposScoped",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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

func (s *Store) GetRepoScoped(ctx context.Context, id int64, installationIDs []int64) (storeResult0 *Repo, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetRepoScoped",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: id, Column2: installationIDs})
	if err != nil {
		return nil, err
	}
	repo := repoFromSQLC(row.ID, row.InstallationID, row.GithubID, row.FullName, row.DefaultBranch, row.Enabled, row.SettingsJSON, row.CreatedAt, row.UpdatedAt)
	return &repo, nil
}

// --- Reviews ---

func (s *Store) GetReview(ctx context.Context, id uuid.UUID) (storeResult0 *Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetReview",

			"id", storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered,

				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetReview(ctx, id)
	if err != nil {
		return nil, err
	}
	review := fullReviewFromSQLC(row)
	return &review, nil
}

func (s *Store) GetReviewComments(ctx context.Context, reviewID uuid.UUID) (storeResult0 []ReviewComment, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetReviewComments",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(
				storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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
func (s *Store) GetPRCompletedReviewComments(ctx context.Context, repoID int64, prNumber int) (storeResult0 []ReviewComment, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPRCompletedReviewComments",

			"repo_id", storeLogValue(repoID), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) ListPRReviewSummaries(ctx context.Context, repoID int64, prNumber int) (storeResult0 []PRReviewSummary, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPRReviewSummaries",

			"repo_id",

			storeLogValue(repoID), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) ListPRAutoResolveEvents(ctx context.Context, repoID int64, prNumber int) (storeResult0 []AutoResolveSummary, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPRAutoResolveEvents",

			"repo_id",

			storeLogValue(repoID), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) SetFindingResolvedSHA(ctx context.Context, commentID uuid.UUID, sha string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetFindingResolvedSHA",

			"comment_id",

			storeLogValue(commentID), "sha", storeLogValue(sha))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	if sha == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE review_comments rc SET resolved_sha = $2
		FROM reviews r
		WHERE rc.id = $1 AND rc.review_id = r.id
		  AND rc.attempt_generation = r.attempt_generation
		  AND rc.resolved_sha IS NULL
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
func (s *Store) SetStartedCommentID(ctx context.Context, reviewID uuid.UUID, commentID int64) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetStartedCommentID",

			"review_id",

			storeLogValue(reviewID), "comment_id", storeLogValue(commentID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	_, err := s.Pool.Exec(ctx,
		`UPDATE reviews SET started_comment_id = $2 WHERE id = $1`, reviewID, commentID)
	return err
}

// GetStartedCommentRef loads the progress-comment reference for a review.
// Returns (nil, nil) when the review never posted one — an ordinary case
// (auto-run off, or the create call failed), not an error.
func (s *Store) GetStartedCommentRef(ctx context.Context, reviewID uuid.UUID) (storeResult0 *StartedCommentRef, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetStartedCommentRef",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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

func (s *Store) UpdateReviewStatus(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateReviewStatus",

			"id",
			storeLogValue(id), "status", storeLogValue(status))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	err := s.q.UpdateReviewStatus(ctx, db.UpdateReviewStatusParams{
		ID:                   id,
		Status:               status,
		Error:                nilIfEmpty(errMsg),
		TokenUsage:           tokenUsage,
		ProtectedErrorPrefix: ErrReviewPostPersistenceAmbiguous.Error(),
	})
	if err != nil {
		return fmt.Errorf("updating review status: %w", err)
	}
	return nil
}

// UpdateReviewStatusForAttempt applies a review-owned write only while the
// caller's generation is still current.
func (s *Store) UpdateReviewStatusForAttempt(ctx context.Context, id uuid.UUID, generation int, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateReviewStatusForAttempt",

			"id", storeLogValue(id), "generation", storeLogValue(
				generation), "status", storeLogValue(status), "allowed_current_count",

			len(allowedCurrent))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	query := `UPDATE reviews SET status=$3,
		error=CASE WHEN error LIKE $6 || '%' AND COALESCE($4,'') NOT LIKE $6 || '%' THEN error ELSE $4 END,
		token_usage=COALESCE($5,token_usage), completed_at=CASE WHEN $3 IN ('completed','failed') THEN NOW() ELSE completed_at END
		WHERE id=$1 AND attempt_generation=$2`
	args := []any{id, generation, status, nilIfEmpty(errMsg), tokenUsage, ErrReviewPostPersistenceAmbiguous.Error()}
	if len(allowedCurrent) > 0 {
		query += ` AND status = ANY($7)`
		args = append(args, allowedCurrent)
	}
	tag, err := s.Pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("updating review attempt status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) GetReviewAttemptGeneration(ctx context.Context, id uuid.UUID) (storeResult0 int, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetReviewAttemptGeneration",

			"id", storeLogValue(id))
	defer func() {
		if recovered :=
			recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var generation int
	if err := s.Pool.QueryRow(ctx, `SELECT attempt_generation FROM reviews WHERE id=$1`, id).Scan(&generation); err != nil {
		return 0, err
	}
	return generation, nil
}

func (s *Store) IsReviewAttemptCurrent(ctx context.Context, id uuid.UUID, generation int) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "IsReviewAttemptCurrent",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var current bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reviews WHERE id=$1 AND attempt_generation=$2)`, id, generation).Scan(&current); err != nil {
		return false, err
	}
	return current, nil
}

// ReviewPostOutcome describes whether PostReviewForAttempt invoked the external
// mutation and durably recorded its id. Rejected and already-recorded outcomes
// never call GitHub. Attempted accompanies an error after the callback began;
// the remote result may be ambiguous even when no id was returned.
type ReviewPostOutcome string

const (
	ReviewPostRejected             ReviewPostOutcome = "rejected"
	ReviewPostAttempted            ReviewPostOutcome = "attempted"
	ReviewPostDefinitelyNotCreated ReviewPostOutcome = "definitely_not_created"
	ReviewPostRecorded             ReviewPostOutcome = "recorded"
	ReviewPostAlreadyRecorded      ReviewPostOutcome = "already_recorded"

	postedReviewOperationTimeout = 3 * time.Second
	postedReviewRepairTimeout    = 3 * time.Second
	postedReviewRepairTries      = 3
	postedReviewRepairDelay      = 50 * time.Millisecond
	// ReviewPostReconciliationMinAge gives GitHub's eventually consistent review
	// listing time to expose a successful mutation before absence can authorize a
	// retry. A positive marker match never waits.
	ReviewPostReconciliationMinAge = 5 * time.Minute
	// hashtextextended keeps the complete UUID in PostgreSQL's stable 64-bit
	// advisory-lock namespace. The seed separates review posting from other
	// application advisory locks.
	postedReviewLockSeed int64 = 0x4152475553504f53
)

var (
	ErrReviewPostRepairConflict       = errors.New("posted review repair conflicts with durable review identity")
	ErrReviewPostPersistenceAmbiguous = errors.New("GitHub review posted but durable identity is ambiguous; automatic retry disabled")
	ErrReviewPostClaimTooRecent       = errors.New("review post reconciliation is not yet safe")
)

func reviewPostClaimMarker() string {
	return ErrReviewPostPersistenceAmbiguous.Error() + ": posting authority claimed; reconciliation required"
}

func reviewPostClaimMarkerFor(id uuid.UUID, generation int, claimedAt time.Time) string {
	return fmt.Sprintf("%s: posting authority claimed; review=%s; generation=%d; claimed_at=%s; reconciliation required",
		ErrReviewPostPersistenceAmbiguous, id, generation, claimedAt.UTC().Format(time.RFC3339Nano))
}

// ReviewPostClaimedAt extracts the marker's observability timestamp. It must
// never authorize reconciliation: only reviews.review_post_claimed_at compared
// with PostgreSQL NOW() is authoritative across application machines.
func ReviewPostClaimedAt(marker string) (time.Time, bool) {
	const key = "claimed_at="
	start := strings.Index(marker, key)
	if start < 0 {
		return time.Time{}, false
	}
	value := marker[start+len(key):]
	if end := strings.IndexByte(value, ';'); end >= 0 {
		value = value[:end]
	}
	claimedAt, err := time.Parse(time.RFC3339Nano, value)
	return claimedAt, err == nil
}

func reviewDefinitelyNotCreated(err error) bool {
	var certain interface{ DefinitelyNotCreated() bool }
	return errors.As(err, &certain) && certain.DefinitelyNotCreated()
}

// clearReviewPostClaim removes only the exact claim created by this call. The
// detached deadline keeps cleanup bounded even when the request was cancelled.
func (s *Store) clearReviewPostClaim(ctx context.Context, id uuid.UUID, generation int, claim string) error {
	clearCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	_, err := s.Pool.Exec(clearCtx, `UPDATE reviews SET error=NULL,review_post_claimed_at=NULL WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NULL AND error=$3`, id, generation, claim)
	if err != nil {
		return fmt.Errorf("clearing exact review post claim: %w", err)
	}
	return nil
}

func detachedReviewPostContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), postedReviewOperationTimeout)
}

type reviewPostQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// repairPostedReviewID establishes or confirms positive remote evidence using
// the supplied session. ctx must be deadline bounded by the caller.
func repairPostedReviewID(ctx context.Context, q reviewPostQuerier, id uuid.UUID, generation int, githubReviewID int64) error {
	var lastErr error
	for attempt := 0; attempt < postedReviewRepairTries; attempt++ {
		var storedID int64
		err := q.QueryRow(ctx, `
			UPDATE reviews
			SET github_review_id=$1,
			    error=CASE WHEN error LIKE $4 || '%' THEN NULL ELSE error END,
			    review_post_claimed_at=CASE WHEN error LIKE $4 || '%' THEN NULL ELSE review_post_claimed_at END
			WHERE id=$2 AND attempt_generation=$3
			  AND (github_review_id IS NULL OR github_review_id=$1)
			RETURNING github_review_id
		`, githubReviewID, id, generation, ErrReviewPostPersistenceAmbiguous.Error()).Scan(&storedID)
		if err == nil {
			if storedID != githubReviewID {
				return fmt.Errorf("%w: repair returned GitHub review id %d, want %d", ErrReviewPostRepairConflict, storedID, githubReviewID)
			}
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			var currentGeneration int
			var currentID *int64
			readErr := q.QueryRow(ctx, `SELECT attempt_generation,github_review_id FROM reviews WHERE id=$1`, id).Scan(&currentGeneration, &currentID)
			if errors.Is(readErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: review %s no longer exists", ErrReviewPostRepairConflict, id)
			}
			if readErr == nil {
				if currentGeneration != generation {
					return fmt.Errorf("%w: review %s generation is %d, posted generation was %d", ErrReviewPostRepairConflict, id, currentGeneration, generation)
				}
				if currentID != nil && *currentID != githubReviewID {
					return fmt.Errorf("%w: review %s has GitHub review id %d, posted id was %d", ErrReviewPostRepairConflict, id, *currentID, githubReviewID)
				}
				lastErr = fmt.Errorf("repair compare-and-set returned no row for review %s", id)
			} else {
				lastErr = fmt.Errorf("re-reading review after repair compare-and-set: %w", readErr)
			}
		} else {
			lastErr = fmt.Errorf("repairing GitHub review id %d: %w", githubReviewID, err)
		}

		if attempt+1 == postedReviewRepairTries {
			break
		}
		timer := time.NewTimer(postedReviewRepairDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("repairing GitHub review id %d: %w", githubReviewID, errors.Join(lastErr, ctx.Err()))
		case <-timer.C:
		}
	}
	return fmt.Errorf("repairing GitHub review id %d after %d attempts: %w", githubReviewID, postedReviewRepairTries, lastErr)
}

// RepairPostedReviewID is the bounded detached recovery entry point for a
// known positive GitHub id. The normal post path first repairs on its locked
// session and falls back here only after quarantining that session.
func (s *Store) RepairPostedReviewID(ctx context.Context, id uuid.UUID, generation int, githubReviewID int64) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "RepairPostedReviewID",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation), "github_review_id",
			storeLogValue(githubReviewID),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	if githubReviewID <= 0 {
		return fmt.Errorf("repairing posted review: invalid GitHub review id %d", githubReviewID)
	}
	repairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postedReviewRepairTimeout)
	defer cancel()
	return repairPostedReviewID(repairCtx, s.Pool, id, generation, githubReviewID)
}

// AttachReconciledReviewID records marker-verified positive evidence without
// completing the review. It intentionally preserves the ambiguous-post claim,
// status, and claim clock so any binding failure remains recoverable without a
// duplicate post. Repeating the same exact attachment is idempotent.
func (s *Store) AttachReconciledReviewID(ctx context.Context, id uuid.UUID, generation int, exactClaim string, githubReviewID int64) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "AttachReconciledReviewID",

			"id",
			storeLogValue(id), "generation", storeLogValue(generation), "github_review_id",
			storeLogValue(githubReviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	if githubReviewID <= 0 {
		return false, fmt.Errorf("attaching reconciled review: invalid GitHub review id %d", githubReviewID)
	}
	attachCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	tag, err := s.Pool.Exec(attachCtx, `
		UPDATE reviews SET github_review_id=$1
		WHERE id=$2 AND attempt_generation=$3 AND github_review_id IS NULL
		  AND error=$4 AND status IN ('failed','cancelled','in_progress')
	`, githubReviewID, id, generation, exactClaim)
	if err != nil {
		return false, fmt.Errorf("attaching reconciled GitHub review id: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var currentGeneration int
	var status string
	var currentID *int64
	var currentClaim *string
	if err := s.Pool.QueryRow(attachCtx, `SELECT attempt_generation,status,github_review_id,error FROM reviews WHERE id=$1`, id).Scan(&currentGeneration, &status, &currentID, &currentClaim); err != nil {
		return false, fmt.Errorf("checking reconciled GitHub review attachment: %w", err)
	}
	if currentGeneration == generation && currentID != nil && *currentID == githubReviewID &&
		((currentClaim != nil && *currentClaim == exactClaim) || status == "completed") {
		return true, nil
	}
	if currentGeneration == generation && currentID != nil && *currentID != githubReviewID {
		return false, fmt.Errorf("%w: review %s has GitHub review id %d, reconciled id was %d", ErrReviewPostRepairConflict, id, *currentID, githubReviewID)
	}
	return false, nil
}

const recoveredEventEnvelopeMarker = "argus.review-event.v1:3f985e31-2ad4-4d78-b5c4-83f3663e12e0"

type ReconciledCompletionMetadata struct {
	RepoID         int64
	PRNumber       int
	InstallationID int64
}

// CompleteReconciledReviewWithEvents atomically commits terminal status and
// the three durable recovered lifecycle events. Semantic keys make repair and
// concurrent retries idempotent; NOTIFY is deferred by PostgreSQL until commit.
func (s *Store) CompleteReconciledReviewWithEvents(ctx context.Context, id uuid.UUID, generation int, exactClaim string, githubReviewID int64, metadata ReconciledCompletionMetadata) (storeResult0 ReviewCompletionOutcome, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CompleteReconciledReviewWithEvents",

			"id", storeLogValue(id), "generation", storeLogValue(generation), "github_review_id",
			storeLogValue(githubReviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	if githubReviewID <= 0 {
		return ReviewCompletionRejected, fmt.Errorf("completing reconciled review: invalid GitHub review id %d", githubReviewID)
	}
	completeCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	tx, err := s.Pool.Begin(completeCtx)
	if err != nil {
		return ReviewCompletionRejected, fmt.Errorf("starting reconciled completion: %w", err)
	}
	txFinish := beginStoreTransaction(completeCtx, "CompleteReconciledReviewWithEvents", "review_id", id, "generation", generation, "github_review_id", githubReviewID)
	committed := false
	defer func() { txFinish(storeErr, committed) }()
	defer func() { _ = tx.Rollback(completeCtx) }()

	var currentGeneration int
	var status string
	var currentID *int64
	var currentClaim *string
	if err := tx.QueryRow(completeCtx, `SELECT attempt_generation,status,github_review_id,error FROM reviews WHERE id=$1 FOR UPDATE`, id).Scan(&currentGeneration, &status, &currentID, &currentClaim); err != nil {
		return ReviewCompletionRejected, fmt.Errorf("locking reconciled review: %w", err)
	}
	if currentGeneration != generation || currentID == nil || *currentID != githubReviewID {
		return ReviewCompletionRejected, nil
	}
	outcome := ReviewCompletionAlreadyCompleted
	if status != "completed" {
		if currentClaim == nil || *currentClaim != exactClaim || (status != "failed" && status != "cancelled" && status != "in_progress") {
			return ReviewCompletionRejected, nil
		}
		tag, updateErr := tx.Exec(completeCtx, `UPDATE reviews SET status='completed',completed_at=COALESCE(completed_at,NOW()),error=NULL,review_post_claimed_at=NULL WHERE id=$1 AND attempt_generation=$2 AND github_review_id=$3 AND error=$4`, id, generation, githubReviewID, exactClaim)
		if updateErr != nil {
			return ReviewCompletionRejected, fmt.Errorf("completing reconciled GitHub review: %w", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return ReviewCompletionRejected, nil
		}
		outcome = ReviewCompletionWon
	}

	type recoveredEvent struct {
		key, eventType string
		payload        any
	}
	events := []recoveredEvent{
		{"recovered.review_completed", "review_completed", map[string]any{"review_id": id, "repo_id": metadata.RepoID, "pr_number": metadata.PRNumber, "installation_id": metadata.InstallationID}},
		{"recovered.posted_to_github", "posted_to_github", map[string]any{"github_review_id": githubReviewID, "recovered": true}},
		{"recovered.completed", "completed", map[string]any{"status": "completed", "recovered": true}},
	}
	for _, event := range events {
		payload, marshalErr := json.Marshal(event.payload)
		if marshalErr != nil {
			return ReviewCompletionRejected, fmt.Errorf("encoding recovered event %s: %w", event.key, marshalErr)
		}
		deliveryID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("argus:%s:%d:%s", id, generation, event.key))).String()
		envelope, marshalErr := json.Marshal(map[string]any{"_argus_event_envelope": recoveredEventEnvelopeMarker, "delivery_id": deliveryID, "data": json.RawMessage(payload)})
		if marshalErr != nil {
			return ReviewCompletionRejected, fmt.Errorf("encoding recovered event envelope %s: %w", event.key, marshalErr)
		}
		if _, insertErr := tx.Exec(completeCtx, `
			WITH inserted AS (
			  INSERT INTO review_events(review_id,attempt_generation,event_type,data,semantic_key)
			  VALUES($1,$2,$3,$4,$5)
			  ON CONFLICT (review_id,attempt_generation,semantic_key) WHERE semantic_key IS NOT NULL DO NOTHING
			  RETURNING id
			)
			SELECT pg_notify('argus_review_events','recovered:'||id::text) FROM inserted
		`, id, generation, event.eventType, envelope, event.key); insertErr != nil {
			return ReviewCompletionRejected, fmt.Errorf("persisting recovered event %s: %w", event.key, insertErr)
		}
	}
	if err := tx.Commit(completeCtx); err != nil {
		return ReviewCompletionRejected, fmt.Errorf("committing reconciled completion: %w", err)
	}
	return outcome, nil
}

// CompleteReconciledReview completes an exact, already-attached delivery. The
// returned winner is the only caller allowed to emit recovered completion
// actions. Binding recovery must succeed before this method is called.
func (s *Store) CompleteReconciledReview(ctx context.Context, id uuid.UUID, generation int, exactClaim string, githubReviewID int64) (storeResult0 ReviewCompletionOutcome, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CompleteReconciledReview",

			"id",
			storeLogValue(id), "generation", storeLogValue(generation), "github_review_id",
			storeLogValue(githubReviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	if githubReviewID <= 0 {
		return ReviewCompletionRejected, fmt.Errorf("completing reconciled review: invalid GitHub review id %d", githubReviewID)
	}
	completeCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	tag, err := s.Pool.Exec(completeCtx, `
		UPDATE reviews
		SET status='completed', completed_at=COALESCE(completed_at,NOW()),
		    error=NULL, review_post_claimed_at=NULL
		WHERE id=$2 AND attempt_generation=$3 AND github_review_id=$1 AND error=$4
		  AND status IN ('failed','cancelled','in_progress')
	`, githubReviewID, id, generation, exactClaim)
	if err != nil {
		return ReviewCompletionRejected, fmt.Errorf("completing reconciled GitHub review: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ReviewCompletionWon, nil
	}

	var currentGeneration int
	var status string
	var currentID *int64
	if err := s.Pool.QueryRow(completeCtx, `SELECT attempt_generation,status,github_review_id FROM reviews WHERE id=$1`, id).Scan(&currentGeneration, &status, &currentID); err != nil {
		return ReviewCompletionRejected, fmt.Errorf("checking reconciled GitHub review loser: %w", err)
	}
	if currentGeneration == generation && status == "completed" && currentID != nil && *currentID == githubReviewID {
		return ReviewCompletionAlreadyCompleted, nil
	}
	if currentGeneration == generation && currentID != nil && *currentID != githubReviewID {
		return ReviewCompletionRejected, fmt.Errorf("%w: review %s has GitHub review id %d, reconciled id was %d", ErrReviewPostRepairConflict, id, *currentID, githubReviewID)
	}
	return ReviewCompletionRejected, nil
}

// ClearReconciledReviewClaim authorizes a retry only for an exact, aged claim.
// PostgreSQL's clock and the DB-derived claim column are the age authority;
// marker timestamps are observability only and may come from a skewed host.
func (s *Store) ClearReconciledReviewClaim(ctx context.Context, id uuid.UUID, generation int, exactClaim string) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ClearReconciledReviewClaim",

			"id", storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	clearCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	tag, err := s.Pool.Exec(clearCtx, `
		UPDATE reviews SET error=NULL,review_post_claimed_at=NULL
		WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NULL AND error=$3
		  AND review_post_claimed_at IS NOT NULL
		  AND review_post_claimed_at <= NOW()-make_interval(secs => $4)
	`, id, generation, exactClaim, ReviewPostReconciliationMinAge.Seconds())
	if err != nil {
		return false, fmt.Errorf("clearing reconciled review post claim: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	// Distinguish an exact claim that is still inside the consistency window
	// from an exact-CAS loser. Both stay blocked, but callers surface the former
	// as an intentional retry delay rather than a changed claim.
	var recent bool
	if err := s.Pool.QueryRow(clearCtx, `
		SELECT EXISTS(
			SELECT 1 FROM reviews
			WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NULL AND error=$3
			  AND review_post_claimed_at IS NOT NULL
			  AND review_post_claimed_at > NOW()-make_interval(secs => $4)
		)
	`, id, generation, exactClaim, ReviewPostReconciliationMinAge.Seconds()).Scan(&recent); err != nil {
		return false, fmt.Errorf("checking reconciled review post claim age: %w", err)
	}
	if recent {
		return false, ErrReviewPostClaimTooRecent
	}
	return false, nil
}

// GetRecordedReviewID is the early, read-only crash-recovery check used before
// pre-post learning. It deliberately does not change review status: callers
// with an id must still join CompletePostedReview's winner election.
func (s *Store) GetRecordedReviewID(ctx context.Context, id uuid.UUID, generation int) (storeResult0 int64, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetRecordedReviewID",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0, storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0, storeResult1)
	}()

	var githubReviewID int64
	err := s.Pool.QueryRow(ctx, `SELECT github_review_id FROM reviews WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NOT NULL`, id, generation).Scan(&githubReviewID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("checking recorded GitHub review id: %w", err)
	}
	return githubReviewID, true, nil
}

// rollbackReviewPostTx bounds cleanup after the non-idempotent callback. false
// means the transaction state is unconfirmed and its connection must never be
// returned to the pool.
func rollbackReviewPostTx(ctx context.Context, tx pgx.Tx) bool {
	rollbackCtx, cancel := detachedReviewPostContext(ctx)
	defer cancel()
	err := tx.Rollback(rollbackCtx)
	return err == nil || errors.Is(err, pgx.ErrTxClosed)
}

// PostReviewForAttempt is the authority boundary for the non-idempotent GitHub
// mutation. A per-review PostgreSQL session advisory lock spans the durable
// pre-post claim, external call, evidence transaction, and repair. Retry takes
// the matching transaction lock, so another machine cannot advance generation
// while positive evidence is being persisted.
//
// The claim is committed before the external call. It is intentionally
// conservative: if the process or connection disappears at any later point,
// automatic retry remains blocked until reconciliation. This also makes an
// unconfirmed rollback/unlock safe to quarantine without reopening a duplicate
// delivery window. Cancel/failure writers preserve this marker.
func (s *Store) PostReviewForAttempt(ctx context.Context, id uuid.UUID, generation int, post func(context.Context) (int64, error)) (githubReviewID int64, outcome ReviewPostOutcome, err error) {
	storeFinish :=
		beginStoreOperation(ctx, "PostReviewForAttempt",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, githubReviewID, outcome)
			panic(recovered)
		}
		storeFinish(err, githubReviewID, outcome)
	}()

	if post == nil {
		return 0, ReviewPostRejected, errors.New("posting review: nil callback")
	}

	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return 0, ReviewPostRejected, fmt.Errorf("acquiring review post authority session: %w", err)
	}
	locked := false
	quarantined := false
	quarantine := func(reason error) error {
		if quarantined {
			return reason
		}
		quarantined = true
		locked = false
		raw := conn.Hijack()
		closeCtx, cancel := detachedReviewPostContext(ctx)
		defer cancel()
		if closeErr := raw.Close(closeCtx); closeErr != nil {
			return errors.Join(reason, fmt.Errorf("closing quarantined review post session: %w", closeErr))
		}
		return reason
	}
	defer func() {
		if quarantined {
			return
		}
		if locked {
			unlockCtx, cancel := detachedReviewPostContext(ctx)
			var unlocked bool
			var unlockErr error
			if s.unlockReviewPostSession == nil {
				unlockErr = conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1,$2))`, id.String(), postedReviewLockSeed).Scan(&unlocked)
			} else {
				unlocked, unlockErr = s.unlockReviewPostSession(unlockCtx, conn, id.String())
			}
			cancel()
			if unlockErr != nil || !unlocked {
				if unlockErr == nil {
					unlockErr = errors.New("PostgreSQL reported review post advisory lock was not held")
				}
				cleanupErr := quarantine(fmt.Errorf("releasing review post authority: %w", unlockErr))
				if err == nil {
					err = cleanupErr
				} else {
					err = errors.Join(err, cleanupErr)
				}
				return
			}
		}
		conn.Release()
	}()

	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,$2))`, id.String(), postedReviewLockSeed); err != nil {
		return 0, ReviewPostRejected, quarantine(fmt.Errorf("acquiring review post authority (outcome unconfirmed): %w", err))
	}
	locked = true

	// Commit a conservative claim before the request can escape this process.
	// Same-generation workers serialize on the session lock and treat an old
	// claim as an ambiguity, never as permission to call GitHub again.
	beginClaim := s.beginReviewPostClaimTx
	var claimTx pgx.Tx
	var beginErr error
	if beginClaim == nil {
		claimTx, beginErr = conn.Begin(ctx)
	} else {
		claimTx, beginErr = beginClaim(ctx, conn)
	}
	if beginErr != nil {
		return 0, ReviewPostRejected, fmt.Errorf("beginning review post claim: %w", beginErr)
	}
	claimClosed := false
	defer func() {
		if !claimClosed && !rollbackReviewPostTx(ctx, claimTx) {
			cleanupErr := quarantine(errors.New("review post claim rollback outcome unconfirmed"))
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()

	var currentGeneration int
	var status string
	var recordedID *int64
	var existingError *string
	if err = claimTx.QueryRow(ctx, `SELECT attempt_generation,status,github_review_id,error FROM reviews WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&currentGeneration, &status, &recordedID, &existingError); err != nil {
		return 0, ReviewPostRejected, fmt.Errorf("locking review for post claim: %w", err)
	}
	if currentGeneration != generation {
		return 0, ReviewPostRejected, nil
	}
	if recordedID != nil {
		return *recordedID, ReviewPostAlreadyRecorded, nil
	}
	if existingError != nil && strings.HasPrefix(*existingError, ErrReviewPostPersistenceAmbiguous.Error()) {
		return 0, ReviewPostRejected, fmt.Errorf("%w: review %s already has an unresolved posting claim", ErrReviewPostPersistenceAmbiguous, id)
	}
	if status != "in_progress" {
		return 0, ReviewPostRejected, nil
	}
	var claimedAt time.Time
	if claimErr := claimTx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&claimedAt); claimErr != nil {
		return 0, ReviewPostRejected, fmt.Errorf("reading authoritative review post claim clock: %w", claimErr)
	}
	claimMarker := reviewPostClaimMarkerFor(id, generation, claimedAt)
	tag, claimErr := claimTx.Exec(ctx, `UPDATE reviews SET error=$1,review_post_claimed_at=$2 WHERE id=$3 AND attempt_generation=$4 AND status='in_progress' AND github_review_id IS NULL`, claimMarker, claimedAt, id, generation)
	if claimErr != nil || tag.RowsAffected() != 1 {
		if claimErr == nil {
			claimErr = errors.New("guarded review row disappeared")
		}
		return 0, ReviewPostRejected, fmt.Errorf("recording durable review post claim: %w", claimErr)
	}
	claimCommitCtx, cancelClaimCommit := context.WithTimeout(ctx, postedReviewOperationTimeout)
	claimErr = claimTx.Commit(claimCommitCtx)
	cancelClaimCommit()
	claimClosed = true
	if claimErr != nil {
		// COMMIT may have succeeded, but no external call happened. Quarantine the
		// uncertain session, then safely clear only this exact claim on a fresh,
		// bounded connection so retry cannot be stranded by a local commit loss.
		commitErr := quarantine(fmt.Errorf("committing durable review post claim: %w", claimErr))
		if clearErr := s.clearReviewPostClaim(ctx, id, generation, claimMarker); clearErr != nil {
			commitErr = errors.Join(commitErr, clearErr)
		}
		return 0, ReviewPostRejected, commitErr
	}

	begin := s.beginReviewPostTx
	var tx pgx.Tx
	if begin == nil {
		tx, err = conn.Begin(ctx)
	} else {
		tx, err = begin(ctx, conn)
	}
	if err != nil {
		guardErr := fmt.Errorf("beginning review post guard: %w", err)
		if clearErr := s.clearReviewPostClaim(ctx, id, generation, claimMarker); clearErr != nil {
			guardErr = errors.Join(guardErr, clearErr)
		}
		return 0, ReviewPostRejected, guardErr
	}
	txClosed := false
	defer func() {
		if !txClosed && !rollbackReviewPostTx(ctx, tx) {
			cleanupErr := quarantine(errors.New("review post rollback outcome unconfirmed"))
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()

	if err = tx.QueryRow(ctx, `SELECT attempt_generation,status,github_review_id FROM reviews WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&currentGeneration, &status, &recordedID); err != nil {
		guardErr := fmt.Errorf("locking review for post: %w", err)
		if !rollbackReviewPostTx(ctx, tx) {
			guardErr = errors.Join(guardErr, quarantine(errors.New("review post guard rollback outcome unconfirmed")))
		}
		txClosed = true
		if clearErr := s.clearReviewPostClaim(ctx, id, generation, claimMarker); clearErr != nil {
			guardErr = errors.Join(guardErr, clearErr)
		}
		return 0, ReviewPostRejected, guardErr
	}
	clearUnusedClaim := func() error {
		clearCtx, cancelClear := detachedReviewPostContext(ctx)
		tag, clearErr := tx.Exec(clearCtx, `UPDATE reviews SET error=NULL,review_post_claimed_at=NULL WHERE id=$1 AND attempt_generation=$2 AND github_review_id IS NULL AND error=$3`, id, generation, claimMarker)
		cancelClear()
		if clearErr == nil && tag.RowsAffected() != 1 {
			clearErr = errors.New("exact review post claim disappeared")
		}
		if clearErr != nil {
			return fmt.Errorf("clearing unused review post claim: %w", clearErr)
		}
		commitCtx, cancelCommit := detachedReviewPostContext(ctx)
		commitErr := tx.Commit(commitCtx)
		cancelCommit()
		if commitErr != nil {
			return fmt.Errorf("committing unused review post claim cleanup: %w", commitErr)
		}
		txClosed = true
		return nil
	}
	if currentGeneration != generation {
		return 0, ReviewPostRejected, clearUnusedClaim()
	}
	if recordedID != nil {
		if clearErr := clearUnusedClaim(); clearErr != nil {
			return *recordedID, ReviewPostAlreadyRecorded, clearErr
		}
		return *recordedID, ReviewPostAlreadyRecorded, nil
	}
	if status != "in_progress" {
		// Cancellation won the small gap between claim commit and this guard. The
		// callback has not run, so remove only our exact claim before allowing retry.
		return 0, ReviewPostRejected, clearUnusedClaim()
	}

	githubReviewID, callbackErr := post(ctx)
	if githubReviewID <= 0 {
		if callbackErr != nil && reviewDefinitelyNotCreated(callbackErr) {
			// GitHub conclusively did not create a review. Clear only our exact
			// durable claim in the already-locked transaction before exposing the
			// retryable failure.
			if clearErr := clearUnusedClaim(); clearErr != nil {
				// The remote mutation is still known absent, so a fresh exact CAS is
				// safe even if the cleanup transaction response was lost.
				if !txClosed {
					_ = rollbackReviewPostTx(ctx, tx)
					txClosed = true
				}
				if fallbackErr := s.clearReviewPostClaim(ctx, id, generation, claimMarker); fallbackErr != nil {
					callbackErr = errors.Join(callbackErr, clearErr, fallbackErr)
				}
			}
			return githubReviewID, ReviewPostDefinitelyNotCreated, callbackErr
		}
		if callbackErr != nil {
			return githubReviewID, ReviewPostAttempted, callbackErr
		}
		return githubReviewID, ReviewPostAttempted, fmt.Errorf("posting review returned invalid id %d", githubReviewID)
	}

	// A positive id is authoritative evidence even if the client also returned
	// an error. Every detached operation after this point has its own deadline.
	persistCtx, cancelPersist := detachedReviewPostContext(ctx)
	tag, persistErr := tx.Exec(persistCtx, `UPDATE reviews SET github_review_id=$1,error=NULL,review_post_claimed_at=NULL WHERE id=$2 AND attempt_generation=$3 AND status='in_progress' AND github_review_id IS NULL`, githubReviewID, id, generation)
	cancelPersist()
	if persistErr == nil && tag.RowsAffected() != 1 {
		persistErr = fmt.Errorf("persisting GitHub review id %d after post succeeded: guarded row disappeared", githubReviewID)
	}
	if persistErr == nil {
		commitCtx, cancelCommit := detachedReviewPostContext(ctx)
		persistErr = tx.Commit(commitCtx)
		cancelCommit()
		if persistErr == nil {
			txClosed = true
		} else {
			persistErr = fmt.Errorf("committing GitHub review id %d after post succeeded (commit outcome may be ambiguous): %w", githubReviewID, persistErr)
		}
	} else {
		persistErr = fmt.Errorf("persisting GitHub review id %d after post succeeded: %w", githubReviewID, persistErr)
	}
	if persistErr == nil {
		return githubReviewID, ReviewPostRecorded, nil
	}

	rollbackConfirmed := txClosed || rollbackReviewPostTx(ctx, tx)
	txClosed = true
	if !rollbackConfirmed {
		persistErr = errors.Join(persistErr, quarantine(errors.New("review post rollback outcome unconfirmed")))
	}

	var repairErr error
	if !quarantined {
		repairCtx, cancelRepair := context.WithTimeout(context.WithoutCancel(ctx), postedReviewRepairTimeout)
		repairErr = repairPostedReviewID(repairCtx, conn, id, generation, githubReviewID)
		cancelRepair()
		if repairErr != nil {
			repairErr = errors.Join(repairErr, quarantine(errors.New("review post repair session became untrustworthy")))
		}
	}
	if quarantined {
		// The durable claim prevents BeginReviewRetry from winning this race after
		// the session lock is released, so a fresh-session repair remains safe.
		if fallbackErr := s.RepairPostedReviewID(ctx, id, generation, githubReviewID); fallbackErr == nil {
			return githubReviewID, ReviewPostRecorded, nil
		} else if repairErr == nil {
			repairErr = fallbackErr
		} else {
			repairErr = errors.Join(repairErr, fallbackErr)
		}
	}
	if repairErr == nil {
		return githubReviewID, ReviewPostRecorded, nil
	}

	ambiguityErr := fmt.Errorf("%w: GitHub review %d; persistence error: %v; detached repair error: %v", ErrReviewPostPersistenceAmbiguous, githubReviewID, persistErr, repairErr)
	// Upgrade the pre-call marker with the known id and failure evidence. Failure
	// here cannot reopen retry: the conservative claim is already committed.
	blockCtx, cancelBlock := detachedReviewPostContext(ctx)
	_, blockErr := s.Pool.Exec(blockCtx, `UPDATE reviews SET error=$1 WHERE id=$2 AND attempt_generation=$3 AND github_review_id IS NULL AND error LIKE $4 || '%'`, ambiguityErr.Error(), id, generation, ErrReviewPostPersistenceAmbiguous.Error())
	cancelBlock()
	if blockErr != nil {
		ambiguityErr = errors.Join(ambiguityErr, fmt.Errorf("updating durable review post ambiguity evidence: %w", blockErr))
	}
	return githubReviewID, ReviewPostAttempted, ambiguityErr
}

// RunIfReviewAttemptCurrent linearizes an attempt-owned external side effect
// against BeginReviewRetry. The callback runs while holding a NO KEY UPDATE
// lock on the review row: a retry's generation bump waits, while writes that
// reference reviews through a foreign key can still take KEY SHARE and avoid
// self-deadlocking on their separate connection.
//
// The callback should contain only the mutation, never the potentially slow
// work that prepares it. current=false means the generation already lost
// ownership. An authority lookup/transaction failure is returned as an error;
// callers must not mistake storage failure for a stale attempt.
func (s *Store) RunIfReviewAttemptCurrent(ctx context.Context, id uuid.UUID, generation int, write func(context.Context) error) (current bool, err error) {
	storeFinish :=
		beginStoreOperation(ctx, "RunIfReviewAttemptCurrent",

			"id", storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, current)
			panic(recovered)
		}
		storeFinish(err, current)
	}()

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning review attempt guard: %w", err)
	}
	txFinish := beginStoreTransaction(ctx, "RunIfReviewAttemptCurrent", "review_id", id, "generation", generation)
	committed := false
	defer func() { txFinish(err, committed) }()
	defer func() { _ = tx.Rollback(context.Background()) }()

	var currentGeneration int
	if err = tx.QueryRow(ctx, `SELECT attempt_generation FROM reviews WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&currentGeneration); err != nil {
		return false, fmt.Errorf("locking review attempt: %w", err)
	}
	if currentGeneration != generation {
		return false, nil
	}

	if err = write(ctx); err != nil {
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("committing review attempt guard: %w", err)
	}
	committed = true
	return true, nil
}

// ReviewCompletionOutcome is the winner election result for follow-up work
// after a GitHub review id is durable.
type ReviewCompletionOutcome string

const (
	ReviewCompletionWon              ReviewCompletionOutcome = "won"
	ReviewCompletionAlreadyCompleted ReviewCompletionOutcome = "already_completed"
	ReviewCompletionRejected         ReviewCompletionOutcome = "rejected"
)

// CompletePostedReview elects exactly one same-generation worker to transition
// the review to completed. Only ReviewCompletionWon may run linked-reference,
// event, backfill, hydration, and post-review sink side effects. A concurrent
// worker that observes the identical durable id already completed is a clean
// loser, not a cancellation. Different ids are integrity conflicts.
func (s *Store) CompletePostedReview(ctx context.Context, id uuid.UUID, generation int, githubReviewID int64) (storeResult0 ReviewCompletionOutcome, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CompletePostedReview",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation), "github_review_id",
			storeLogValue(githubReviewID),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	if githubReviewID <= 0 {
		return ReviewCompletionRejected, fmt.Errorf("completing posted review: invalid GitHub review id %d", githubReviewID)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews
		SET status='completed', github_review_id=$1,
		    completed_at=COALESCE(completed_at,NOW()), error=NULL,
		    review_post_claimed_at=NULL
		WHERE id=$2 AND attempt_generation=$3 AND status='in_progress'
		  AND (github_review_id IS NULL OR github_review_id=$1)
	`, githubReviewID, id, generation)
	if err != nil {
		return ReviewCompletionRejected, fmt.Errorf("completing posted review: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ReviewCompletionWon, nil
	}

	var currentGeneration int
	var status string
	var currentID *int64
	if err := s.Pool.QueryRow(ctx, `SELECT attempt_generation,status,github_review_id FROM reviews WHERE id=$1`, id).Scan(&currentGeneration, &status, &currentID); err != nil {
		return ReviewCompletionRejected, fmt.Errorf("checking posted review completion loser: %w", err)
	}
	if currentGeneration == generation && status == "completed" && currentID != nil && *currentID == githubReviewID {
		return ReviewCompletionAlreadyCompleted, nil
	}
	if currentGeneration == generation && currentID != nil && *currentID != githubReviewID {
		return ReviewCompletionRejected, fmt.Errorf("%w: review %s has GitHub review id %d, completion id was %d", ErrReviewPostRepairConflict, id, *currentID, githubReviewID)
	}
	return ReviewCompletionRejected, nil
}

// GetReviewStatus returns just the status column for a review — a cheap PK
// lookup used by the state machine's cooperative-cancellation check, which runs
// at every stage boundary and must stay light.
// ConvergePostedReview repairs a review whose GitHub mutation succeeded but
// whose completion write did not. Cancelled rows remain cancelled.
func (s *Store) ConvergePostedReview(ctx context.Context, id uuid.UUID, generation int) (storeResult0 int64, storeResult1 bool, storeResult2 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ConvergePostedReview",

			"id",

			storeLogValue(id), "generation", storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0, storeResult1, storeResult2)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0, storeResult1,
			storeResult2)
	}()

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
	tag, err := s.Pool.Exec(ctx, `UPDATE reviews SET status='completed',completed_at=COALESCE(completed_at,NOW()),error=NULL,review_post_claimed_at=NULL WHERE id=$1 AND attempt_generation=$2 AND status IN ('pending','in_progress','failed')`, id, generation)
	if err != nil {
		return 0, true, false, fmt.Errorf("converging posted review: %w", err)
	}
	return githubReviewID, true, tag.RowsAffected() > 0, nil
}

// BeginReviewRetry atomically starts one new generation. Concurrent callers
// cannot both advance a failed/cancelled review to pending.
func (s *Store) BeginReviewRetry(ctx context.Context, id uuid.UUID) (storeResult0 int, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "BeginReviewRetry",

			"id", storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered,

				storeResult0, storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0,
			storeResult1)
	}()

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("beginning review retry transaction: %w", err)
	}
	txFinish := beginStoreTransaction(ctx, "BeginReviewRetry", "review_id", id)
	committed := false
	defer func() { txFinish(storeErr, committed) }()
	closed := false
	defer func() {
		if !closed {
			_ = rollbackReviewPostTx(ctx, tx)
		}
	}()

	// Match PostReviewForAttempt's per-review session lock before touching the
	// generation. Lock-before-row ordering avoids a deadlock with the posting
	// transaction; cancellation may wait on the row but never holds this lock.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,$2))`, id.String(), postedReviewLockSeed); err != nil {
		return 0, false, fmt.Errorf("acquiring review retry authority: %w", err)
	}

	var generation int
	err = tx.QueryRow(ctx, `
		UPDATE reviews
		SET status = 'pending', error = NULL, completed_at = NULL,
		    review_post_claimed_at = NULL, expected_github_inline_count = NULL,
		    attempt_generation = attempt_generation + 1
		WHERE id = $1 AND status IN ('failed', 'cancelled')
		  AND (error IS NULL OR error NOT LIKE $2 || '%')
		RETURNING attempt_generation
	`, id, ErrReviewPostPersistenceAmbiguous.Error()).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return 0, false, fmt.Errorf("committing review retry loser: %w", err)
		}
		committed = true
		closed = true
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("beginning review retry: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("committing review retry: %w", err)
	}
	committed = true
	closed = true
	return generation, true, nil
}

func (s *Store) GetReviewStatus(ctx context.Context, id uuid.UUID) (storeResult0 string, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetReviewStatus",

			"id",
			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered,

				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) UpdateReviewStatusIf(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateReviewStatusIf",

			"id",

			storeLogValue(id), "status", storeLogValue(status), "allowed_current_count",
			len(
				allowedCurrent))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = $2,
		       error = CASE WHEN error LIKE $6 || '%' AND COALESCE($3,'') NOT LIKE $6 || '%' THEN error ELSE $3 END,
		       token_usage = COALESCE($4, token_usage),
		       completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE completed_at END
		WHERE id = $1 AND status = ANY($5)
	`, id, status, nilIfEmpty(errMsg), tokenUsage, allowedCurrent, ErrReviewPostPersistenceAmbiguous.Error())
	if err != nil {
		return false, fmt.Errorf("updating review status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// List queries drop the heavy fields (token_usage, diagram*, diagrams,
// truncated_files, brief) to keep response size manageable. 1 row of those
// columns averages ~5 KB of JSONB; at limit=200 the list payload was 1.22 MB
// (measured via Fly logs on the dashboard route) — ~95% of which was data the
// list view never renders. The detail endpoint (GET /reviews/{id}) still
// returns the full Review struct via GetReview. The generated row type owns
// the list query's scan order, so adding a field to Review cannot recreate the
// production 500 caused by a positional destination-count mismatch.
func (s *Store) ListReviewsScoped(ctx context.Context, repoID int64, installationIDs []int64, limit, offset int) (storeResult0 []Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListReviewsScoped",

			"repo_id",

			storeLogValue(repoID), "installation_ids_count", len(installationIDs), "limit",
			storeLogValue(limit), "offset",

			storeLogValue(offset))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) ListAllReviewsScoped(ctx context.Context, installationIDs []int64, limit, offset int) (storeResult0 []Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListAllReviewsScoped",

			"installation_ids_count",

			len(installationIDs), "limit", storeLogValue(limit), "offset",
			storeLogValue(offset))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) ReplaceReviewMinorNotes(ctx context.Context, reviewID uuid.UUID, attemptGeneration int, notes []ReviewMinorNote) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ReplaceReviewMinorNotes",

			"review_id",

			storeLogValue(reviewID), "attempt_generation",
			storeLogValue(attemptGeneration), "notes_count", len(
				notes))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning minor notes replace: %w", err)
	}
	txFinish := beginStoreTransaction(ctx, "ReplaceReviewMinorNotes", "review_id", reviewID, "generation", attemptGeneration, "note_count", len(notes))
	committed := false
	defer func() { txFinish(storeErr, committed) }()
	defer func() { _ = tx.Rollback(context.Background()) }()
	var currentGeneration int
	if err = tx.QueryRow(ctx, `SELECT attempt_generation FROM reviews WHERE id = $1 FOR UPDATE`, reviewID).Scan(&currentGeneration); err != nil {
		return fmt.Errorf("locking review for minor notes: %w", err)
	}
	if currentGeneration != attemptGeneration {
		return ErrReviewAttemptStale
	}
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
	committed = true
	return nil
}

// GetReviewMinorNotes returns only the review's current attempt.
func (s *Store) GetReviewMinorNotes(ctx context.Context, reviewID uuid.UUID) (storeResult0 []ReviewMinorNote, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetReviewMinorNotes",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	rows, err := s.q.GetReviewMinorNotes(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	notes := make([]ReviewMinorNote, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, ReviewMinorNote{
			ID: row.ID, ReviewID: row.ReviewID, AttemptGeneration: row.AttemptGeneration,
			FilePath: row.FilePath, Line: row.Line, Severity: row.Severity,
			Title: row.Title, CreatedAt: row.CreatedAt,
		})
	}
	return notes, nil
}

// ClaimReviewSignal atomically elects one machine to deliver a CTA. An
// abandoned claim becomes retryable after the lease expires.
func (s *Store) ClaimReviewSignal(ctx context.Context, repoID int64, prNumber int, kind string, staleAfter time.Duration) (storeResult0 uuid.UUID, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ClaimReviewSignal",

			"repo_id",

			storeLogValue(repoID), "pr_number", storeLogValue(prNumber), "kind", storeLogValue(kind), "stale_after",
			storeLogValue(staleAfter))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0, storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0,
			storeResult1,
		)
	}()

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

func (s *Store) CompleteReviewSignal(ctx context.Context, id uuid.UUID) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CompleteReviewSignal",

			"id",

			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	tag, err := s.Pool.Exec(ctx, `UPDATE review_signals SET delivered_at=NOW() WHERE id=$1 AND delivered_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
func (s *Store) ReleaseReviewSignal(ctx context.Context, id uuid.UUID) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ReleaseReviewSignal",

			"id",

			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	tag, err := s.Pool.Exec(ctx, `DELETE FROM review_signals WHERE id=$1 AND delivered_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// --- Rules ---

func (s *Store) ListRules(ctx context.Context, installationIDs []int64) (storeResult0 []Rule, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListRules",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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

func (s *Store) CreateRule(ctx context.Context, installationID int64, category, content string, priority int, enabled bool) (storeResult0 *Rule, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateRule",

			"installation_id",

			storeLogValue(installationID), "category", storeLogValue(category), "priority",
			storeLogValue(priority),
			"enabled",

			storeLogValue(enabled))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var rule Rule
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		row, err := db.New(tx).CreateRule(ctx, db.CreateRuleParams{
			InstallationID: &installationID, Category: category, Content: content,
			Priority: priority, Enabled: enabled,
		})
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		rule = Rule{ID: row.ID, InstallationID: row.InstallationID, Category: row.Category, Content: row.Content, Priority: row.Priority, Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		return newRuleMirrorEvent(rule, enabled)
	})
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Store) UpdateRule(ctx context.Context, id int64, installationIDs []int64, category, content *string, priority *int, enabled *bool) (storeResult0 *Rule, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateRule",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs), "category", storeLogValue(category), "priority", storeLogValue(priority), "enabled", storeLogValue(enabled))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var rule Rule
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		row, err := db.New(tx).UpdateRule(ctx, db.UpdateRuleParams{
			ID: id, InstallationIds: installationIDs, Category: category,
			Content: content, Priority: priority, Enabled: enabled,
		})
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		rule = Rule{ID: row.ID, InstallationID: row.InstallationID, Category: row.Category, Content: row.Content, Priority: row.Priority, Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		return newRuleMirrorEvent(rule, rule.Enabled)
	})
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *Store) DeleteRule(ctx context.Context, id int64, installationIDs []int64) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeleteRule",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		installationID, err := db.New(tx).DeleteRule(ctx, db.DeleteRuleParams{ID: id, InstallationIds: installationIDs})
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryMirrorEvent{}, fmt.Errorf("rule %d not found", id)
		}
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		if installationID == nil {
			return MemoryMirrorEvent{}, fmt.Errorf("rule %d has no installation", id)
		}
		payload, err := json.Marshal(map[string]string{"custom_id": fmt.Sprintf("rule--%d", id)})
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return MemoryMirrorEvent{InstallationID: *installationID, AggregateType: MemoryMirrorRule, AggregateID: id, Operation: MemoryMirrorDelete, Payload: payload}, nil
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

func (s *Store) ListModelConfigs(ctx context.Context, repoID int64) (storeResult0 []ModelConfig, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListModelConfigs",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) UpsertModelConfig(ctx context.Context, repoID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (storeResult0 *ModelConfig, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpsertModelConfig",

			"repo_id",

			storeLogValue(repoID), "stage", storeLogValue(stage),
			"provider", storeLogValue(provider), "model", storeLogValue(model))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.UpsertModelConfig(ctx, db.UpsertModelConfigParams{RepoID: &repoID, Stage: stage, Provider: provider, Model: model, BaseURL: baseURL, MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return nil, err
	}
	config := modelConfigFromValues(row.ID, row.RepoID, nil, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt)
	return &config, nil
}

func (s *Store) DeleteModelConfig(ctx context.Context, repoID int64, stage string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeleteModelConfig",

			"repo_id",

			storeLogValue(repoID), "stage", storeLogValue(stage),
		)
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

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
func (s *Store) ListOrgModelConfigs(ctx context.Context, installationID int64) (storeResult0 []ModelConfig, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListOrgModelConfigs",

			"installation_id",

			storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) UpsertOrgModelConfig(ctx context.Context, installationID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (storeResult0 *ModelConfig, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpsertOrgModelConfig",

			"installation_id",

			storeLogValue(installationID), "stage", storeLogValue(stage), "provider",
			storeLogValue(provider),
			"model", storeLogValue(model))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.UpsertOrgModelConfig(ctx, db.UpsertOrgModelConfigParams{InstallationID: &installationID, Stage: stage, Provider: provider, Model: model, BaseURL: baseURL, MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return nil, err
	}
	config := modelConfigFromValues(row.ID, row.RepoID, row.InstallationID, row.Stage, row.Provider, row.Model, row.BaseURL, row.MaxTokens, row.Temperature, row.CreatedAt, row.UpdatedAt)
	return &config, nil
}

// DeleteOrgModelConfig removes an installation-level config.
func (s *Store) DeleteOrgModelConfig(ctx context.Context, installationID int64, stage string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeleteOrgModelConfig",

			"installation_id",

			storeLogValue(installationID), "stage", storeLogValue(stage))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

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
func (s *Store) ListModelConfigsWithFallback(ctx context.Context, installationID, repoID int64) (storeResult0 []ModelConfig, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListModelConfigsWithFallback",

			"installation_id", storeLogValue(installationID), "repo_id",
			storeLogValue(repoID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

// ErrReviewAttemptStale means an attempt-owned write lost the generation race
// to BeginReviewRetry. Callers must stop that worker instead of publishing it.
var ErrReviewAttemptStale = errors.New("review attempt is no longer current")

func (s *Store) CreateReviewComment(ctx context.Context, reviewID uuid.UUID, attemptGeneration int, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist, codeSnippet *string, confidenceScore *int, githubCommentID *int64, matchedPatternID *int64, matchedPatternScore *float32, enforcedRuleContent *string, isNewFinding bool, suppressedReason *string, state FindingState) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateReviewComment",

			"review_id",

			storeLogValue(reviewID), "attempt_generation", storeLogValue(attemptGeneration), "file_path", storeLogValue(filePath), "severity", storeLogValue(severity), "category", storeLogValue(category), "confidence_score", storeLogValue(confidenceScore), "github_comment_id",
			storeLogValue(githubCommentID), "matched_pattern_id",
			storeLogValue(matchedPatternID))
	defer func() {

		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.CreateReviewCommentWithInlineManifest(ctx, reviewID, attemptGeneration, filePath, startLine, endLine, side, body, severity, category, specialist, codeSnippet, confidenceScore, githubCommentID, matchedPatternID, matchedPatternScore, enforcedRuleContent, isNewFinding, suppressedReason, state, false)
}

// CreateReviewCommentWithInlineManifest persists whether this exact row was selected by Compose for the external submission.
func (s *Store) CreateReviewCommentWithInlineManifest(ctx context.Context, reviewID uuid.UUID, attemptGeneration int, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist, codeSnippet *string, confidenceScore *int, githubCommentID *int64, matchedPatternID *int64, matchedPatternScore *float32, enforcedRuleContent *string, isNewFinding bool, suppressedReason *string, state FindingState, wasPostedInline bool) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateReviewCommentWithInlineManifest",

			"review_id", storeLogValue(reviewID), "attempt_generation",
			storeLogValue(attemptGeneration), "file_path",

			storeLogValue(filePath), "severity", storeLogValue(severity), "category", storeLogValue(category), "confidence_score", storeLogValue(confidenceScore), "github_comment_id",
			storeLogValue(githubCommentID), "matched_pattern_id",
			storeLogValue(matchedPatternID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	if state == "" {
		state = FindingStatePosted
	}
	count, err := s.q.CreateReviewComment(ctx, db.CreateReviewCommentParams{
		ReviewID: reviewID, AttemptGeneration: attemptGeneration, FilePath: filePath, StartLine: startLine, EndLine: endLine, Side: side,
		Body: body, Severity: severity, Category: category, Specialist: specialist,
		ConfidenceScore: confidenceScore, CodeSnippet: codeSnippet, GithubCommentID: githubCommentID,
		MatchedPatternID: matchedPatternID, MatchedPatternScore: matchedPatternScore,
		EnforcedRuleContent: enforcedRuleContent, IsNewFinding: &isNewFinding,
		SuppressedReason: suppressedReason, State: string(state), WasPostedInline: wasPostedInline,
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrReviewAttemptStale
	}
	return nil
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
func (s *Store) ListPRGithubCommentIDs(ctx context.Context, repoFullName string, prNumber int) (storeResult0 []int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPRGithubCommentIDs",

			"repo_full_name",

			storeLogValue(repoFullName), "pr_number",
			storeLogValue(prNumber))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) GetCommentByGithubID(ctx context.Context, githubCommentID int64) (storeResult0 *ReviewComment, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetCommentByGithubID",

			"github_comment_id",

			storeLogValue(githubCommentID))
	defer func() {
		if recovered := recover(); recovered != nil {

			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

// SaveExpectedReviewInlineCount finalizes the attempt-owned delivery manifest.
// It succeeds only when exactly expected rows were stamped by Compose.
func (s *Store) SaveExpectedReviewInlineCount(ctx context.Context, reviewID uuid.UUID, generation, expected int) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SaveExpectedReviewInlineCount",

			"review_id", storeLogValue(reviewID), "generation",
			storeLogValue(generation),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	if expected < 0 {
		return fmt.Errorf("saving inline manifest: invalid expected count %d", expected)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews r SET expected_github_inline_count=$3
		WHERE r.id=$1 AND r.attempt_generation=$2
		  AND (r.expected_github_inline_count IS NULL OR r.expected_github_inline_count=$3)
		  AND (SELECT count(*) FROM review_comments rc
		       WHERE rc.review_id=$1 AND rc.attempt_generation=$2 AND rc.was_posted_inline)=$3
	`, reviewID, generation, expected)
	if err != nil {
		return fmt.Errorf("saving inline manifest: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("saving inline manifest: attempt changed or stamped row count differs from %d", expected)
	}
	return nil
}

// ExpectedReviewInlineCount returns false for legacy attempts that predate the
// exact manifest. Such attempts are intentionally not auto-reconcilable.
func (s *Store) ExpectedReviewInlineCount(ctx context.Context, reviewID uuid.UUID, generation int) (storeResult0 int, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ExpectedReviewInlineCount",

			"review_id", storeLogValue(reviewID), "generation", storeLogValue(generation))
	defer func() {
		if recovered :=

			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0,
				storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0, storeResult1)
	}()

	var count *int
	err := s.Pool.QueryRow(ctx, `SELECT expected_github_inline_count FROM reviews WHERE id=$1 AND attempt_generation=$2`, reviewID, generation).Scan(&count)
	if err != nil {
		return 0, false, fmt.Errorf("reading inline manifest: %w", err)
	}
	if count == nil {
		return 0, false, nil
	}
	return *count, true, nil
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
func (s *Store) ListUnboundReviewComments(ctx context.Context, reviewID uuid.UUID) (storeResult0 []UnboundComment, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListUnboundReviewComments",

			"review_id", storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	rows, err := s.Pool.Query(ctx, `
		SELECT id, file_path, end_line, body
		FROM review_comments
		WHERE review_id = $1 AND github_comment_id IS NULL AND end_line IS NOT NULL
		  AND attempt_generation = (SELECT attempt_generation FROM reviews WHERE id=$1)
		  AND was_posted_inline
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

// PostedReviewBindingState describes the durable inline-comment bindings for
// one exact delivery. Current is false when the generation or GitHub review ID
// no longer matches the review row.
type PostedReviewBindingState struct {
	Current               bool
	BoundGitHubCommentIDs int
	MissingThreadNodeIDs  int
}

// GetPostedReviewBindingState counts current-generation rows that are already
// bound to delivered inline comments. Suppressed and folded summary rows have
// no GitHub comment ID and do not require a GraphQL thread.
func (s *Store) GetPostedReviewBindingState(ctx context.Context, reviewID uuid.UUID, generation int, githubReviewID int64) (storeResult0 PostedReviewBindingState, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPostedReviewBindingState",

			"review_id", storeLogValue(reviewID), "generation", storeLogValue(generation),
			"github_review_id", storeLogValue(githubReviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var state PostedReviewBindingState
	err := s.Pool.QueryRow(ctx, `
		SELECT r.attempt_generation=$2 AND r.github_review_id=$3,
		       count(rc.id) FILTER (WHERE rc.github_comment_id IS NOT NULL)::int,
		       count(rc.id) FILTER (
		           WHERE rc.github_comment_id IS NOT NULL AND rc.graphql_thread_node_id IS NULL
		       )::int
		FROM reviews r
		LEFT JOIN review_comments rc
		  ON rc.review_id=r.id
		 AND rc.attempt_generation=$2
		 AND rc.was_posted_inline
		WHERE r.id=$1
		GROUP BY r.attempt_generation,r.github_review_id
	`, reviewID, generation, githubReviewID).Scan(
		&state.Current,
		&state.BoundGitHubCommentIDs,
		&state.MissingThreadNodeIDs,
	)
	if err != nil {
		return PostedReviewBindingState{}, fmt.Errorf("reading posted review binding state: %w", err)
	}
	return state, nil
}

// ListPostedReviewGitHubCommentIDs returns the exact REST IDs already bound
// to posted findings in one current generation. Summary-only and folded rows
// remain unbound and are intentionally absent.
func (s *Store) ListPostedReviewGitHubCommentIDs(ctx context.Context, reviewID uuid.UUID, generation int) (storeResult0 []int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPostedReviewGitHubCommentIDs",

			"review_id", storeLogValue(reviewID), "generation",
			storeLogValue(generation))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	rows, err := s.Pool.Query(ctx, `
		SELECT rc.github_comment_id
		FROM review_comments rc
		JOIN reviews r ON r.id=rc.review_id AND r.attempt_generation=rc.attempt_generation
		WHERE rc.review_id=$1 AND rc.attempt_generation=$2
		  AND rc.github_comment_id IS NOT NULL
		  AND rc.was_posted_inline
		ORDER BY rc.github_comment_id
	`, reviewID, generation)
	if err != nil {
		return nil, fmt.Errorf("listing posted review GitHub comment ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning posted review GitHub comment id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing posted review GitHub comment ids: %w", err)
	}
	return ids, nil
}

// BindGitHubCommentID binds exactly one review_comments row to its GitHub REST
// comment id. Scoped to id (the finding's PK) and guarded by
// github_comment_id IS NULL so a replayed backfill can't re-point an already
// bound row. Returns whether the row was updated. This replaces the old fuzzy
// (review_id, file_path, end_line) UPDATE that collapsed two same-line findings
// onto a single id (see FindingLifecycle #165 same-line binding fix).
func (s *Store) BindGitHubCommentID(ctx context.Context, commentID uuid.UUID, githubCommentID int64) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "BindGitHubCommentID",

			"comment_id",

			storeLogValue(commentID), "github_comment_id",
			storeLogValue(githubCommentID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	tag, err := s.Pool.Exec(ctx, `
		UPDATE review_comments rc SET github_comment_id = $2
		FROM reviews r
		WHERE rc.id = $1 AND rc.review_id = r.id
		  AND rc.attempt_generation = r.attempt_generation
		  AND rc.github_comment_id IS NULL AND rc.was_posted_inline
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
func (s *Store) GetRepoReviewStats(ctx context.Context, repoID int64, limit int) (storeResult0 RepoReviewStats, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetRepoReviewStats",

			"repo_id",

			storeLogValue(repoID), "limit", storeLogValue(limit))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetRepoReviewStats(ctx, db.GetRepoReviewStatsParams{RepoID: repoID, RowLimit: int64(limit)})
	return RepoReviewStats{SampleSize: row.SampleSize, AvgTokens: row.AvgTokens, AvgCost: row.AvgCost, CostAvailable: row.CostAvailable}, err
}

// GetLastCompletedReview returns the most recent completed review for a repo+PR.
func (s *Store) GetLastCompletedReview(ctx context.Context, repoID int64, prNumber int) (storeResult0 *Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLastCompletedReview",

			"repo_id",

			storeLogValue(repoID), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetLastCompletedReview(ctx, db.GetLastCompletedReviewParams{RepoID: repoID, PRNumber: prNumber})
	if err != nil {
		return nil, err
	}
	review := lastCompletedReviewFromSQLC(row)
	return &review, nil
}

func (s *Store) GetLatestReviewBySHA(ctx context.Context, repoFullName string, prNumber int, headSHA string) (storeResult0 *Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLatestReviewBySHA",

			"repo_full_name",

			storeLogValue(repoFullName), "pr_number", storeLogValue(prNumber), "head_sha",
			storeLogValue(headSHA))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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
func (s *Store) HasFailedReviewWithError(ctx context.Context, repoID int64, prNumber int, errorCode string) (storeResult0 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "HasFailedReviewWithError",

			"repo_id",
			storeLogValue(repoID), "pr_number", storeLogValue(prNumber), "error_code",
			storeLogValue(errorCode))

	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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
func (s *Store) GetLatestReviewByPR(ctx context.Context, repoFullName string, prNumber int) (storeResult0 *Review, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLatestReviewByPR",

			"repo_full_name",

			storeLogValue(repoFullName), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered :=

			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
// review coverage that was never delivered (#239). The generated query owns
// the predicate so the Store wrapper cannot drift from it.
func (s *Store) GetStats(ctx context.Context) (storeResult0 *Stats, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetStats")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetStats(ctx)
	if err != nil {
		return nil, err
	}
	stats := Stats{TotalReviews: row.TotalReviews, CompletedToday: row.CompletedToday, AvgScore: row.AvgScore, ActiveRepos: row.ActiveRepos, CriticalFinds: row.CriticalFinds, PendingReviews: row.PendingReviews, CatchRate: row.CatchRate, PRsThisWeek: row.PrsThisWeek, HighRiskCount: row.HighRiskCount, AvgReviewTimeMs: row.AvgReviewTimeMs, DeepReviewCount: row.DeepReviewCount}
	return &stats, nil
}

func (s *Store) GetStatsScoped(ctx context.Context, installationIDs []int64) (storeResult0 *Stats, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetStatsScoped",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	row, err := s.q.GetStatsScoped(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	stats := Stats{TotalReviews: row.TotalReviews, CompletedToday: row.CompletedToday, AvgScore: row.AvgScore, ActiveRepos: row.ActiveRepos, CriticalFinds: row.CriticalFinds, PendingReviews: row.PendingReviews, CatchRate: row.CatchRate, PRsThisWeek: row.PrsThisWeek, HighRiskCount: row.HighRiskCount, AvgReviewTimeMs: row.AvgReviewTimeMs, DeepReviewCount: row.DeepReviewCount}
	return &stats, nil
}

// --- Activity ---

func (s *Store) ListActivity(ctx context.Context, installationIDs []int64, limit int) (storeResult0 []ActivityLog, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListActivity",

			"installation_ids_count",

			len(installationIDs), "limit", storeLogValue(limit))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) LogActivity(ctx context.Context, installationID *int64, action, actor, resource string, metadata []byte) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "LogActivity",

			"installation_id",

			storeLogValue(installationID), "action", storeLogValue(action), "actor", storeLogValue(actor), "resource",

			storeLogValue(resource))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

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
func (s *Store) InsertAutoResolveEvent(ctx context.Context, p InsertAutoResolveEventParams) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "InsertAutoResolveEvent")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	keys := p.ResolvedThreadKeys
	if keys == nil {
		keys = []string{}
	}
	err := s.q.InsertAutoResolveEvent(ctx, db.InsertAutoResolveEventParams{InstallationID: p.InstallationID, RepoID: p.RepoID, PRNumber: p.PRNumber, SourceSHA: p.SourceSHA, ResolvedCount: p.ResolvedCount, AttemptedCount: p.AttemptedCount, GitHubAPICalls: p.GitHubAPICalls, ResolvedThreadKeys: keys})
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
func (s *Store) GetAutoResolveStats(ctx context.Context, installationIDs []int64, period string) (storeResult0 GetAutoResolveStatsRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetAutoResolveStats",

			"installation_ids_count",

			len(installationIDs), "period", storeLogValue(period))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetAutoResolveStats(ctx, db.GetAutoResolveStatsParams{InstallationIds: installationIDs, Period: period})
	result := GetAutoResolveStatsRow{EventCount: row.EventCount, ResolvedTotal: row.ResolvedTotal, AttemptedTotal: row.AttemptedTotal, APICallsTotal: row.APICallsTotal}
	if err != nil {
		return result, fmt.Errorf("get auto_resolve_events stats: %w", err)
	}
	return result, nil
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
func (s *Store) GetLearnLayerCounts(ctx context.Context, installationIDs []int64, period string) (storeResult0 GetLearnLayerCountsRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLearnLayerCounts",

			"installation_ids_count",

			len(installationIDs), "period", storeLogValue(period))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetLearnLayerCounts(ctx, db.GetLearnLayerCountsParams{InstallationIds: installationIDs, Period: period})
	result := GetLearnLayerCountsRow{PatternsLearned: row.PatternsLearned, ScenariosStored: row.ScenariosStored, DecisionTraces: row.DecisionTraces, FeedbackIndexed: row.FeedbackIndexed}
	if err != nil {
		return result, fmt.Errorf("get learn-layer counts: %w", err)
	}
	return result, nil
}

// --- Comment Outcomes ---

// RecordCommentOutcome records a (comment, outcome) signal idempotently and
// reports whether this call actually inserted a new row. inserted=false means
// the outcome was already recorded — the reaction sweep replays on every PR
// event, so callers must gate side effects (e.g. bumping pattern quality) on
// inserted to avoid double-counting a single 👍/👎.
func (s *Store) RecordCommentOutcome(ctx context.Context, reviewCommentID uuid.UUID, outcome string) (inserted bool, err error) {
	storeFinish :=
		beginStoreOperation(ctx, "RecordCommentOutcome",

			"review_comment_id",

			storeLogValue(reviewCommentID), "outcome",
			storeLogValue(outcome),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, inserted)
			panic(recovered)
		}
		storeFinish(err, inserted)
	}()

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
func (s *Store) SetScenarioMemoryDocID(ctx context.Context, id int64, memoryDocID string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetScenarioMemoryDocID",

			"id",

			storeLogValue(id), "memory_doc_id", storeLogValue(memoryDocID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.UpdateScenarioMemoryDocID(ctx, db.UpdateScenarioMemoryDocIDParams{
		MemoryDocID: &memoryDocID,
		ID:          id,
	})
}

func (s *Store) GetCommentOutcomes(ctx context.Context, reviewCommentID uuid.UUID) (storeResult0 []CommentOutcome, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetCommentOutcomes",

			"review_comment_id",

			storeLogValue(reviewCommentID))
	defer func() {
		if recovered := recover(); recovered != nil {

			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) ListPostedFindings(ctx context.Context, repoID int64, prNumber int) (storeResult0 []PostedFinding, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPostedFindings",

			"repo_id",

			storeLogValue(repoID), "pr_number", storeLogValue(prNumber))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) RecordFindingOutcome(ctx context.Context, reviewCommentID uuid.UUID, outcome string, addressedAt *time.Time) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "RecordFindingOutcome",

			"review_comment_id",

			storeLogValue(reviewCommentID), "outcome",
			storeLogValue(outcome),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	_, err := s.Pool.Exec(ctx, `
		INSERT INTO comment_outcomes (review_comment_id, outcome, addressed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (review_comment_id, outcome) DO NOTHING
	`, reviewCommentID, outcome, addressedAt)
	return err
}

// ListReviewGauge reads vw_review_gauge scoped to the given installations.
func (s *Store) ListReviewGauge(ctx context.Context, installationIDs []int64) (storeResult0 []GaugeRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListReviewGauge",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

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

func (s *Store) ListPromptTemplates(ctx context.Context, repoID int64) (storeResult0 []PromptTemplate, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPromptTemplates",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) UpsertPromptTemplate(ctx context.Context, repoID int64, stage, promptText string) (storeResult0 *PromptTemplate, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpsertPromptTemplate",

			"repo_id",

			storeLogValue(repoID), "stage", storeLogValue(stage))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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

func (s *Store) DeletePromptTemplate(ctx context.Context, repoID int64, stage string) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeletePromptTemplate",

			"repo_id",

			storeLogValue(repoID), "stage", storeLogValue(stage))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

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
func (s *Store) RecoverStaleReviews(ctx context.Context, maxAge time.Duration) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "RecoverStaleReviews",

			"max_age",

			storeLogValue(maxAge))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(
				storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	tag, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = 'failed',
		       error = CASE WHEN error LIKE $2 || '%' THEN error ELSE 'review timed out — server restarted' END,
		       completed_at = NOW()
		WHERE status IN ('pending', 'in_progress')
		  AND created_at < NOW() - make_interval(secs => $1)
	`, float64(maxAge.Seconds()), ErrReviewPostPersistenceAmbiguous.Error())
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
func (s *Store) GetLatestRunForReview(ctx context.Context, reviewID uuid.UUID) (storeResult0 uuid.UUID, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLatestRunForReview",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.GetLatestRunForReview(ctx, reviewID)
}

func (s *Store) FindReviewsLinkingToPR(ctx context.Context, arg db.FindReviewsLinkingToPRParams) (storeResult0 []db.FindReviewsLinkingToPRRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "FindReviewsLinkingToPR")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0,
			)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.FindReviewsLinkingToPR(ctx, arg)
}

func (s *Store) SetReviewLinkedPRRefs(ctx context.Context, arg db.SetReviewLinkedPRRefsParams) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetReviewLinkedPRRefs")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(
				recovered,
			)
		}
		storeFinish(storeErr)
	}()

	return s.q.SetReviewLinkedPRRefs(ctx, arg)
}

func (s *Store) SetReviewLinkedIssueRefs(ctx context.Context, arg db.SetReviewLinkedIssueRefsParams) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "SetReviewLinkedIssueRefs")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(
				recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.SetReviewLinkedIssueRefs(ctx, arg)
}

func (s *Store) UpdateReviewCrossPRHash(ctx context.Context, arg db.UpdateReviewCrossPRHashParams) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "UpdateReviewCrossPRHash")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(
				recovered,
			)
		}
		storeFinish(storeErr)
	}()

	return s.q.UpdateReviewCrossPRHash(ctx, arg)
}

func (s *Store) GetLatestCompletedReviewByPR(ctx context.Context, arg db.GetLatestCompletedReviewByPRParams) (storeResult0 db.GetLatestCompletedReviewByPRRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetLatestCompletedReviewByPR")
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.GetLatestCompletedReviewByPR(ctx, arg)
}

func (s *Store) FindSharedLinkedIssues(ctx context.Context, reviewID uuid.UUID) (storeResult0 []db.FindSharedLinkedIssuesRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "FindSharedLinkedIssues",

			"review_id",

			storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.FindSharedLinkedIssues(ctx, reviewID)
}

func (s *Store) MergeStageTokenEntry(ctx context.Context, arg db.MergeStageTokenEntryParams) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "MergeStageTokenEntry")
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0,
			)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

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
func (s *Store) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (storeResult0 json.RawMessage, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetInstallationFeatureFlags",

			"installation_id", storeLogValue(installationID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.GetInstallationFeatureFlags(ctx, installationID)
}

// MergeInstallationFeatureFlags merges the given keys into the feature_flags
// JSONB, leaving every other key intact. Merging in SQL rather than
// read-modify-writing in Go keeps it atomic: the settings form owns three
// keys while operators set others by direct UPDATE, and a
// lost update between those two writers reverts a backend flip silently.
func (s *Store) MergeInstallationFeatureFlags(ctx context.Context, installationID int64, patch json.RawMessage) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "MergeInstallationFeatureFlags",

			"installation_id", storeLogValue(installationID), "patch_count",
			len(patch))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered)
			panic(
				recovered)
		}
		storeFinish(storeErr)
	}()

	return s.q.MergeInstallationFeatureFlags(ctx, db.MergeInstallationFeatureFlagsParams{
		ID:    installationID,
		Patch: patch,
	})
}

// GetAllFileReviewsForReview returns the unfiltered per-file review payload
// (pre dedup/scoring) recorded for a review's latest run, as raw JSONB. The
// export path uses it to surface dropped findings.
func (s *Store) GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (storeResult0 json.RawMessage, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetAllFileReviewsForReview",

			"review_id", storeLogValue(reviewID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return s.q.GetAllFileReviewsForReview(ctx, reviewID)
}

// GetTopChokePoints returns the highest fan-in files for a repo (up to limit) —
// the architecture-summary input.
func (s *Store) GetTopChokePoints(ctx context.Context, repoID int64, limit int32) (storeResult0 []db.GetTopChokePointsRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetTopChokePoints",

			"repo_id",

			storeLogValue(repoID), "limit", storeLogValue(limit),
		)
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.GetTopChokePoints(ctx, db.GetTopChokePointsParams{RepoID: repoID, Limit: limit})
}

// ListArchNodes returns the per-symbol architecture rows (file, name, language,
// line span) for a repo.
func (s *Store) ListArchNodes(ctx context.Context, repoID int64) (storeResult0 []db.ListArchNodesRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListArchNodes",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.ListArchNodes(ctx, repoID)
}

// ListArchFileEdges returns the file→file dependency edges for a repo.
func (s *Store) ListArchFileEdges(ctx context.Context, repoID int64) (storeResult0 []db.ListArchFileEdgesRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListArchFileEdges",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.ListArchFileEdges(ctx, repoID)
}

// ListArchBugDensity returns per-file bug counts and PR-change frequency for a repo.
func (s *Store) ListArchBugDensity(ctx context.Context, repoID int64) (storeResult0 []db.ListArchBugDensityRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListArchBugDensity",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(
				storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.ListArchBugDensity(ctx, repoID)
}

// ListArchCoupling returns per-PR file sets used to derive temporal coupling.
func (s *Store) ListArchCoupling(ctx context.Context, repoID int64) (storeResult0 []db.ListArchCouplingRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListArchCoupling",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	return s.q.ListArchCoupling(ctx, repoID)
}

// ListGraphNodes returns the code-graph nodes for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphNodes(ctx context.Context, repoID int64) (storeResult0 []db.ListGraphNodesRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListGraphNodes",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	rows, err := s.q.ListGraphNodes(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphNodesRow{}
	}
	return rows, err
}

// ListGraphEdges returns the code-graph edges for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphEdges(ctx context.Context, repoID int64) (storeResult0 []db.ListGraphEdgesRow, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListGraphEdges",

			"repo_id",

			storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish,

				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	rows, err := s.q.ListGraphEdges(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphEdgesRow{}
	}
	return rows, err
}
