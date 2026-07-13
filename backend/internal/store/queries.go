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
	var inst Installation
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ($1, $2)
		ON CONFLICT (installation_id) DO UPDATE SET org_login = $2, suspended_at = NULL
		RETURNING id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
	`, installationID, orgLogin).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.ClerkOrgID, &inst.PlanTier, &inst.CreatedAt, &inst.SuspendedAt)
	return &inst, err
}

func (s *Store) ListInstallations(ctx context.Context) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at FROM installations ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Installation])
}

// --- User Installations ---

func (s *Store) LinkUserInstallation(ctx context.Context, clerkUserID string, installationID int64, role string) (*UserInstallation, error) {
	var ui UserInstallation
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO user_installations (clerk_user_id, installation_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (clerk_user_id, installation_id) DO NOTHING
		RETURNING id, clerk_user_id, installation_id, role, created_at
	`, clerkUserID, installationID, role).Scan(&ui.ID, &ui.ClerkUserID, &ui.InstallationID, &ui.Role, &ui.CreatedAt)
	if err == pgx.ErrNoRows {
		err = s.Pool.QueryRow(ctx, `
			SELECT id, clerk_user_id, installation_id, role, created_at
			FROM user_installations WHERE clerk_user_id = $1 AND installation_id = $2
		`, clerkUserID, installationID).Scan(&ui.ID, &ui.ClerkUserID, &ui.InstallationID, &ui.Role, &ui.CreatedAt)
	}
	return &ui, err
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
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id, i.installation_id, i.org_login, i.clerk_org_id, i.plan_tier, i.created_at, i.suspended_at
		FROM installations i
		JOIN user_installations ui ON ui.installation_id = i.id
		WHERE ui.clerk_user_id = $1
		ORDER BY i.created_at DESC
	`, clerkUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Installation])
}

func (s *Store) GetUserInstallationIDs(ctx context.Context, clerkUserID string) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT installation_id FROM user_installations WHERE clerk_user_id = $1
	`, clerkUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, rows.Err()
}

func (s *Store) GetInstallation(ctx context.Context, id int64) (*Installation, error) {
	var inst Installation
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
		FROM installations WHERE id = $1
	`, id).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.ClerkOrgID, &inst.PlanTier, &inst.CreatedAt, &inst.SuspendedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Store) GetInstallationByGitHubID(ctx context.Context, ghInstallationID int64) (*Installation, error) {
	var inst Installation
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
		FROM installations WHERE installation_id = $1
	`, ghInstallationID).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.ClerkOrgID, &inst.PlanTier, &inst.CreatedAt, &inst.SuspendedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Store) GetInstallationByClerkOrgID(ctx context.Context, clerkOrgID string) (*Installation, error) {
	var inst Installation
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
		FROM installations WHERE clerk_org_id = $1
	`, clerkOrgID).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.ClerkOrgID, &inst.PlanTier, &inst.CreatedAt, &inst.SuspendedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Store) CountReviewsThisMonth(ctx context.Context, installationID int64) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM reviews r
		JOIN repos rp ON r.repo_id = rp.id
		WHERE rp.installation_id = $1
		AND r.created_at >= date_trunc('month', NOW())
	`, installationID).Scan(&count)
	return count, err
}

func (s *Store) GetPlanTier(ctx context.Context, installationID int64) (string, error) {
	var tier string
	err := s.Pool.QueryRow(ctx, `SELECT plan_tier FROM installations WHERE id = $1`, installationID).Scan(&tier)
	return tier, err
}

func (s *Store) SetPlanTier(ctx context.Context, installationID int64, tier string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE installations SET plan_tier = $1 WHERE id = $2`, tier, installationID)
	return err
}

func (s *Store) CountEnabledRepos(ctx context.Context, installationID int64) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM repos WHERE installation_id = $1 AND enabled = TRUE`, installationID).Scan(&count)
	return count, err
}

func (s *Store) SuspendInstallation(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE installations SET suspended_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *Store) SetInstallationClerkOrgID(ctx context.Context, installationID int64, clerkOrgID string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE installations SET clerk_org_id = $1 WHERE id = $2
	`, clerkOrgID, installationID)
	return err
}

// --- Org Default Settings ---

func (s *Store) GetOrgDefaults(ctx context.Context, installationID int64) (json.RawMessage, error) {
	var settings json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(default_settings, '{}') FROM installations WHERE id = $1`, installationID).Scan(&settings)
	return settings, err
}

func (s *Store) SetOrgDefaults(ctx context.Context, installationID int64, settings json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `UPDATE installations SET default_settings = $1 WHERE id = $2`, settings, installationID)
	return err
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
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos ORDER BY full_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Repo])
}

func (s *Store) ListReposByOwner(ctx context.Context, ownerPrefix string) ([]Repo, error) {
	// Escape LIKE wildcards to prevent LLM-controlled injection
	escaped := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(ownerPrefix)
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE full_name LIKE $1 ESCAPE '\' ORDER BY full_name
	`, escaped+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Repo])
}

func (s *Store) GetRepo(ctx context.Context, id int64) (*Repo, error) {
	var r Repo
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE id = $1
	`, id).Scan(&r.ID, &r.InstallationID, &r.GithubID, &r.FullName, &r.DefaultBranch, &r.Enabled, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetRepoByFullName(ctx context.Context, fullName string) (*Repo, error) {
	var r Repo
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE full_name = $1
	`, fullName).Scan(&r.ID, &r.InstallationID, &r.GithubID, &r.FullName, &r.DefaultBranch, &r.Enabled, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpdateRepo(ctx context.Context, id int64, enabled *bool, defaultBranch *string, settingsJSON []byte) (*Repo, error) {
	var r Repo
	err := s.Pool.QueryRow(ctx, `
		UPDATE repos SET
			enabled = COALESCE($2, enabled),
			default_branch = COALESCE($3, default_branch),
			settings_json = CASE WHEN $4::jsonb IS NULL THEN settings_json ELSE settings_json || $4::jsonb END,
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
	`, id, enabled, defaultBranch, settingsJSON).Scan(&r.ID, &r.InstallationID, &r.GithubID, &r.FullName, &r.DefaultBranch, &r.Enabled, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpsertRepo(ctx context.Context, installationID, githubID int64, fullName, defaultBranch string) (*Repo, error) {
	var r Repo
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name, default_branch)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (github_id) DO UPDATE SET full_name = $3, default_branch = $4, updated_at = NOW()
		RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
	`, installationID, githubID, fullName, defaultBranch).Scan(&r.ID, &r.InstallationID, &r.GithubID, &r.FullName, &r.DefaultBranch, &r.Enabled, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListReposScoped(ctx context.Context, installationIDs []int64) ([]Repo, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE installation_id = ANY($1) ORDER BY full_name
	`, installationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Repo])
}

func (s *Store) GetRepoScoped(ctx context.Context, id int64, installationIDs []int64) (*Repo, error) {
	var r Repo
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
		FROM repos WHERE id = $1 AND installation_id = ANY($2)
	`, id, installationIDs).Scan(&r.ID, &r.InstallationID, &r.GithubID, &r.FullName, &r.DefaultBranch, &r.Enabled, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// --- Reviews ---

func (s *Store) GetReview(ctx context.Context, id uuid.UUID) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,''), github_review_id,
		       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
		       deep_review, persona, is_incremental, created_at, completed_at,
		       diagram, diagram_title, diagrams, truncated_files, brief, cross_pr_hash, trace_id, review_contract
		FROM reviews WHERE id = $1
	`, id).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.HeadRef, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt,
		&r.Diagram, &r.DiagramTitle, &r.Diagrams, &r.TruncatedFiles, &r.Brief, &r.CrossPRHash, &r.TraceID, &r.ReviewContract)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetReviewComments(ctx context.Context, reviewID uuid.UUID) ([]ReviewComment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
		       specialist, confidence_score, code_snippet, github_comment_id,
		       matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding,
		       created_at, state, suppressed_reason
		FROM review_comments WHERE review_id = $1 ORDER BY file_path, start_line
	`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ReviewComment])
}

func (s *Store) UpdateReviewStatus(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = $2, error = $3, token_usage = COALESCE($4, token_usage),
		       completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE NULL END
		WHERE id = $1
	`, id, status, nilIfEmpty(errMsg), tokenUsage)
	if err != nil {
		return fmt.Errorf("updating review status: %w", err)
	}
	return nil
}

// GetReviewStatus returns just the status column for a review — a cheap PK
// lookup used by the state machine's cooperative-cancellation check, which runs
// at every stage boundary and must stay light.
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
// returns the full Review struct via getReview. Scan shape stays identical
// so pgx.RowToStructByPos[Review] keeps working without a new type.
func (s *Store) ListReviewsScoped(ctx context.Context, repoID int64, installationIDs []int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,''), rv.github_review_id,
		       rv.status, rv.summary, rv.score, NULL::jsonb, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at,
		       NULL::text, NULL::text,
		       '[]'::jsonb, '[]'::jsonb,
		       NULL::text, rv.cross_pr_hash, rv.trace_id, NULL::jsonb
		FROM reviews rv
		JOIN repos r ON rv.repo_id = r.id
		WHERE rv.repo_id = $1 AND r.installation_id = ANY($2)
		  AND NOT (`+markerReviewFilter+`)
		ORDER BY rv.created_at DESC LIMIT $3 OFFSET $4
	`, repoID, installationIDs, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Review])
}

func (s *Store) ListAllReviewsScoped(ctx context.Context, installationIDs []int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,''), rv.github_review_id,
		       rv.status, rv.summary, rv.score, NULL::jsonb, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at,
		       NULL::text, NULL::text,
		       '[]'::jsonb, '[]'::jsonb,
		       NULL::text, rv.cross_pr_hash, rv.trace_id, NULL::jsonb
		FROM reviews rv
		JOIN repos r ON rv.repo_id = r.id
		WHERE r.installation_id = ANY($1)
		  AND NOT (`+markerReviewFilter+`)
		ORDER BY rv.created_at DESC LIMIT $2 OFFSET $3
	`, installationIDs, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Review])
}

// --- Rules ---

func (s *Store) ListRules(ctx context.Context, installationIDs []int64) ([]Rule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, category, content, priority, enabled, created_at, updated_at
		FROM rules WHERE installation_id = ANY($1::bigint[]) ORDER BY priority DESC, category
	`, installationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Rule])
}

func (s *Store) CreateRule(ctx context.Context, installationID int64, category, content string, priority int, enabled bool) (*Rule, error) {
	var r Rule
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO rules (installation_id, category, content, priority, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at
	`, installationID, category, content, priority, enabled).Scan(&r.ID, &r.InstallationID, &r.Category, &r.Content, &r.Priority, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (s *Store) UpdateRule(ctx context.Context, id int64, installationIDs []int64, category, content *string, priority *int, enabled *bool) (*Rule, error) {
	var r Rule
	err := s.Pool.QueryRow(ctx, `
		UPDATE rules SET
			category = COALESCE($3, category),
			content = COALESCE($4, content),
			priority = COALESCE($5, priority),
			enabled = COALESCE($6, enabled),
			updated_at = NOW()
		WHERE id = $1 AND installation_id = ANY($2::bigint[])
		RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at
	`, id, installationIDs, category, content, priority, enabled).Scan(&r.ID, &r.InstallationID, &r.Category, &r.Content, &r.Priority, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) DeleteRule(ctx context.Context, id int64, installationIDs []int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM rules WHERE id = $1 AND installation_id = ANY($2::bigint[])`, id, installationIDs)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("rule %d not found", id)
	}
	return nil
}

// --- Model Configs ---

func (s *Store) ListModelConfigs(ctx context.Context, repoID int64) ([]ModelConfig, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, installation_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
		FROM model_configs WHERE repo_id = $1 ORDER BY stage
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ModelConfig])
}

func (s *Store) UpsertModelConfig(ctx context.Context, repoID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (*ModelConfig, error) {
	var mc ModelConfig
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO model_configs (repo_id, stage, provider, model, base_url, max_tokens, temperature)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (repo_id, stage) DO UPDATE SET
			provider = EXCLUDED.provider,
			model = EXCLUDED.model,
			base_url = EXCLUDED.base_url,
			max_tokens = EXCLUDED.max_tokens,
			temperature = EXCLUDED.temperature,
			updated_at = NOW()
		RETURNING id, repo_id, installation_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
	`, repoID, stage, provider, model, baseURL, maxTokens, temperature).Scan(
		&mc.ID, &mc.RepoID, &mc.InstallationID, &mc.Stage, &mc.Provider, &mc.Model, &mc.BaseURL,
		&mc.MaxTokens, &mc.Temperature, &mc.CreatedAt, &mc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &mc, nil
}

func (s *Store) DeleteModelConfig(ctx context.Context, repoID int64, stage string) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM model_configs WHERE repo_id = $1 AND stage = $2`, repoID, stage)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("config not found for repo %d stage %s", repoID, stage)
	}
	return nil
}

// ListOrgModelConfigs returns installation-level model configs (repo_id IS NULL).
func (s *Store) ListOrgModelConfigs(ctx context.Context, installationID int64) ([]ModelConfig, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, installation_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
		FROM model_configs WHERE installation_id = $1 AND repo_id IS NULL ORDER BY stage
	`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ModelConfig])
}

// UpsertOrgModelConfig saves an installation-level model config.
func (s *Store) UpsertOrgModelConfig(ctx context.Context, installationID int64, stage, provider, model string, baseURL *string, maxTokens int, temperature float32) (*ModelConfig, error) {
	var mc ModelConfig
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO model_configs (installation_id, repo_id, stage, provider, model, base_url, max_tokens, temperature)
		VALUES ($1, NULL, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (installation_id, stage) WHERE repo_id IS NULL AND installation_id IS NOT NULL DO UPDATE SET
			provider = EXCLUDED.provider, model = EXCLUDED.model, base_url = EXCLUDED.base_url,
			max_tokens = EXCLUDED.max_tokens, temperature = EXCLUDED.temperature, updated_at = NOW()
		RETURNING id, repo_id, installation_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
	`, installationID, stage, provider, model, baseURL, maxTokens, temperature).Scan(
		&mc.ID, &mc.RepoID, &mc.InstallationID, &mc.Stage, &mc.Provider, &mc.Model, &mc.BaseURL,
		&mc.MaxTokens, &mc.Temperature, &mc.CreatedAt, &mc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &mc, nil
}

// DeleteOrgModelConfig removes an installation-level config.
func (s *Store) DeleteOrgModelConfig(ctx context.Context, installationID int64, stage string) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM model_configs WHERE installation_id = $1 AND stage = $2 AND repo_id IS NULL`, installationID, stage)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("org config not found for installation %d stage %s", installationID, stage)
	}
	return nil
}

// ListModelConfigsWithFallback returns repo configs, falling back to org configs for missing stages.
func (s *Store) ListModelConfigsWithFallback(ctx context.Context, installationID, repoID int64) ([]ModelConfig, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT ON (stage) id, repo_id, installation_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
		FROM model_configs
		WHERE (repo_id = $2 OR (installation_id = $1 AND repo_id IS NULL))
		ORDER BY stage, repo_id NULLS LAST
	`, installationID, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ModelConfig])
}

// --- Review Comments ---

func (s *Store) CreateReviewComment(ctx context.Context, reviewID uuid.UUID, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist, codeSnippet *string, confidenceScore *int, githubCommentID *int64, matchedPatternID *int64, matchedPatternScore *float32, enforcedRuleContent *string, isNewFinding bool, suppressedReason *string, state FindingState) error {
	if state == "" {
		state = FindingStatePosted
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO review_comments (review_id, file_path, start_line, end_line, side, body, severity, category, specialist, confidence_score, code_snippet, github_comment_id, matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding, suppressed_reason, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`, reviewID, filePath, startLine, endLine, side, body, severity, category, specialist, confidenceScore, codeSnippet, githubCommentID, matchedPatternID, matchedPatternScore, enforcedRuleContent, isNewFinding, suppressedReason, string(state))
	return err
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
	var c ReviewComment
	err := s.Pool.QueryRow(ctx, `
		SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
		       specialist, confidence_score, code_snippet, github_comment_id,
		       matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding,
		       created_at
		FROM review_comments WHERE github_comment_id = $1
	`, githubCommentID).Scan(&c.ID, &c.ReviewID, &c.FilePath, &c.StartLine, &c.EndLine, &c.Side, &c.Body, &c.Severity, &c.Category,
		&c.Specialist, &c.ConfidenceScore, &c.CodeSnippet, &c.GithubCommentID,
		&c.MatchedPatternID, &c.MatchedPatternScore, &c.EnforcedRuleContent, &c.IsNewFinding,
		&c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
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
	var stats RepoReviewStats
	err := s.Pool.QueryRow(ctx, `
		WITH recent AS (
			SELECT token_usage FROM reviews
			WHERE repo_id = $1 AND status = 'completed' AND token_usage IS NOT NULL
			ORDER BY created_at DESC
			LIMIT $2
		)
		SELECT
			COUNT(*)::int,
			COALESCE(AVG((token_usage->'total'->>'total_tokens')::bigint), 0)::bigint,
			COALESCE(AVG(NULLIF((token_usage->'total'->>'cost')::float8, 0)), 0)::float8,
			COALESCE(BOOL_OR((token_usage->'total'->>'cost') IS NOT NULL AND (token_usage->'total'->>'cost')::float8 > 0), false)
		FROM recent
	`, repoID, limit).Scan(&stats.SampleSize, &stats.AvgTokens, &stats.AvgCost, &stats.CostAvailable)
	return stats, err
}

// GetLastCompletedReview returns the most recent completed review for a repo+PR.
func (s *Store) GetLastCompletedReview(ctx context.Context, repoID int64, prNumber int) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,''), github_review_id,
		       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
		       deep_review, persona, is_incremental, created_at, completed_at,
		       diagram, diagram_title
		FROM reviews WHERE repo_id = $1 AND pr_number = $2 AND status = 'completed'
		ORDER BY completed_at DESC LIMIT 1
	`, repoID, prNumber).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.HeadRef, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt,
		&r.Diagram, &r.DiagramTitle)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetLatestReviewBySHA(ctx context.Context, repoFullName string, prNumber int, headSHA string) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,''), rv.github_review_id,
		       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at,
		       rv.diagram, rv.diagram_title
		FROM reviews rv JOIN repos r ON rv.repo_id = r.id
		WHERE r.full_name = $1 AND rv.pr_number = $2 AND rv.head_sha = $3
		  AND rv.status = 'completed'
		ORDER BY rv.created_at DESC LIMIT 1
	`, repoFullName, prNumber, headSHA).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.HeadRef, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt,
		&r.Diagram, &r.DiagramTitle)
	if err != nil {
		return nil, err
	}
	return &r, nil
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
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,''), rv.github_review_id,
		       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at,
		       rv.diagram, rv.diagram_title
		FROM reviews rv JOIN repos r ON rv.repo_id = r.id
		WHERE r.full_name = $1 AND rv.pr_number = $2
		  AND rv.status = 'completed'
		ORDER BY rv.created_at DESC LIMIT 1
	`, repoFullName, prNumber).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.HeadRef, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt,
		&r.Diagram, &r.DiagramTitle)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// --- Stats ---

func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	var st Stats
	err := s.Pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM reviews WHERE NOT (`+markerReviewFilter+`))::int,
			(SELECT COUNT(*) FROM reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int,
			COALESCE((SELECT AVG(score)::int FROM reviews WHERE score IS NOT NULL), 0),
			(SELECT COUNT(*) FROM repos WHERE enabled = true)::int,
			(SELECT COUNT(*) FROM review_comments WHERE severity = 'critical')::int,
			(SELECT COUNT(*) FROM reviews WHERE status IN ('pending','in_progress'))::int,
			COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM reviews), 0),
			(SELECT COUNT(*) FROM reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM reviews WHERE score IS NOT NULL AND score <= 4)::int,
			COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM reviews WHERE completed_at IS NOT NULL), 0),
			(SELECT COUNT(*) FROM reviews WHERE deep_review = true)::int
	`).Scan(&st.TotalReviews, &st.CompletedToday, &st.AvgScore, &st.ActiveRepos, &st.CriticalFinds, &st.PendingReviews,
		&st.CatchRate, &st.PRsThisWeek, &st.HighRiskCount, &st.AvgReviewTimeMs, &st.DeepReviewCount)
	return &st, err
}

func (s *Store) GetStatsScoped(ctx context.Context, installationIDs []int64) (*Stats, error) {
	var st Stats
	err := s.Pool.QueryRow(ctx, `
		WITH scoped_reviews AS (
			SELECT * FROM reviews WHERE repo_id IN (SELECT id FROM repos WHERE installation_id = ANY($1))
		)
		SELECT
			(SELECT COUNT(*) FROM scoped_reviews WHERE NOT (`+markerReviewFilter+`))::int,
			(SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int,
			COALESCE((SELECT AVG(score)::int FROM scoped_reviews WHERE score IS NOT NULL), 0),
			(SELECT COUNT(*) FROM repos WHERE installation_id = ANY($1) AND enabled = true)::int,
			(SELECT COUNT(*) FROM review_comments WHERE review_id IN (SELECT id FROM scoped_reviews) AND severity = 'critical')::int,
			(SELECT COUNT(*) FROM scoped_reviews WHERE status IN ('pending','in_progress'))::int,
			COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM scoped_reviews), 0),
			(SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int,
			(SELECT COUNT(*) FROM scoped_reviews WHERE score IS NOT NULL AND score <= 4)::int,
			COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM scoped_reviews WHERE completed_at IS NOT NULL), 0),
			(SELECT COUNT(*) FROM scoped_reviews WHERE deep_review = true)::int
	`, installationIDs).Scan(
		&st.TotalReviews, &st.CompletedToday, &st.AvgScore, &st.ActiveRepos, &st.CriticalFinds, &st.PendingReviews,
		&st.CatchRate, &st.PRsThisWeek, &st.HighRiskCount, &st.AvgReviewTimeMs, &st.DeepReviewCount,
	)
	return &st, err
}

// --- Activity ---

func (s *Store) ListActivity(ctx context.Context, installationIDs []int64, limit int) ([]ActivityLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, action, actor, resource, metadata, created_at
		FROM activity_log WHERE installation_id = ANY($1::bigint[]) ORDER BY created_at DESC LIMIT $2
	`, installationIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ActivityLog])
}

func (s *Store) LogActivity(ctx context.Context, installationID *int64, action, actor, resource string, metadata []byte) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO activity_log (installation_id, action, actor, resource, metadata)
		VALUES ($1, $2, $3, $4, $5)
	`, installationID, action, nilIfEmpty(actor), nilIfEmpty(resource), metadata)
	return err
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
	// ON CONFLICT matches migration 044's comment_outcomes_unique_per_comment
	// UNIQUE (review_comment_id, outcome). GitHub redelivers reaction.created
	// and a removed/re-added reaction replays the same (comment, outcome), so
	// the write must be idempotent instead of erroring on unique_violation.
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO comment_outcomes (review_comment_id, outcome)
		VALUES ($1, $2)
		ON CONFLICT (review_comment_id, outcome) DO NOTHING
	`, reviewCommentID, outcome)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetScenarioSupermemoryID records the Supermemory customID for a scenario in
// migration 045's mirror column. The pipeline calls this after a successful
// IndexScenario so a NULL supermemory_id genuinely means "write failed / pending
// reconciliation" instead of "never attempted" — otherwise the reconciler treats
// every freshly-created scenario as drift forever.
func (s *Store) SetScenarioSupermemoryID(ctx context.Context, id int64, supermemoryID string) error {
	return s.Q.UpdateScenarioSupermemoryID(ctx, db.UpdateScenarioSupermemoryIDParams{
		SupermemoryID: &supermemoryID,
		ID:            id,
	})
}

func (s *Store) GetCommentOutcomes(ctx context.Context, reviewCommentID uuid.UUID) ([]CommentOutcome, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, review_comment_id, outcome, created_at
		FROM comment_outcomes WHERE review_comment_id = $1 ORDER BY created_at DESC
	`, reviewCommentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[CommentOutcome])
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
	rows, err := s.Pool.Query(ctx, `
		SELECT rc.id, rc.file_path, COALESCE(rc.end_line, rc.start_line, 0), rc.created_at, rv.head_sha
		FROM review_comments rc
		JOIN reviews rv ON rv.id = rc.review_id
		WHERE rv.repo_id = $1 AND rv.pr_number = $2 AND rv.status = 'completed'
		  AND rc.suppressed_reason IS NULL
		  AND rc.github_comment_id IS NOT NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM comment_outcomes co
		      WHERE co.review_comment_id = rc.id
		        AND co.outcome IN ('addressed_human','addressed_agent','ignored','deferred')
		  )
		ORDER BY rc.created_at
	`, repoID, prNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[PostedFinding])
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
	rows, err := s.Pool.Query(ctx, `
		SELECT installation_id, category, change_class, posted_findings,
		       addressed_human, addressed_agent, dismissed, ignored, deferred,
		       address_rate, dismiss_rate, median_seconds_to_merge
		FROM vw_review_gauge
		WHERE installation_id = ANY($1)
		ORDER BY category, change_class
	`, installationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[GaugeRow])
}

// --- Prompt Templates ---

func (s *Store) ListPromptTemplates(ctx context.Context, repoID int64) ([]PromptTemplate, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, stage, prompt_text, created_at, updated_at
		FROM prompt_templates WHERE repo_id = $1 ORDER BY stage
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[PromptTemplate])
}

func (s *Store) UpsertPromptTemplate(ctx context.Context, repoID int64, stage, promptText string) (*PromptTemplate, error) {
	var pt PromptTemplate
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO prompt_templates (repo_id, stage, prompt_text)
		VALUES ($1, $2, $3)
		ON CONFLICT (repo_id, stage) DO UPDATE SET
			prompt_text = EXCLUDED.prompt_text,
			updated_at = NOW()
		RETURNING id, repo_id, stage, prompt_text, created_at, updated_at
	`, repoID, stage, promptText).Scan(&pt.ID, &pt.RepoID, &pt.Stage, &pt.PromptText, &pt.CreatedAt, &pt.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pt, nil
}

func (s *Store) DeletePromptTemplate(ctx context.Context, repoID int64, stage string) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM prompt_templates WHERE repo_id = $1 AND stage = $2`, repoID, stage)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
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

// --- Supermemory Key ---

func (s *Store) GetSupermemoryKey(ctx context.Context, installationID int64) (string, error) {
	var enc string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(supermemory_key_enc, '') FROM installations WHERE id = $1`, installationID).Scan(&enc)
	return enc, err
}

func (s *Store) SetSupermemoryKey(ctx context.Context, installationID int64, encKey string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE installations SET supermemory_key_enc = $2 WHERE id = $1`, installationID, encKey)
	return err
}

func (s *Store) ClearSupermemoryKey(ctx context.Context, installationID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE installations SET supermemory_key_enc = '' WHERE id = $1`, installationID)
	return err
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Consumer-facing sqlc pass-throughs (issue #137) ---
//
// These thin wrappers absorb the direct *db.Queries (`.Q`) accesses that used
// to leak out of the store package into orchestrator stages and API handlers.
// Routing them through *store.Store keeps callers (and the consumer-declared
// narrow interfaces over the store) off the generated query layer. Pure
// delegation — no business logic. SetScenarioSupermemoryID above is the same
// idiom; grouped here so the former leaks are reviewable in one place.

// GetInstallationFeatureFlags returns the raw feature_flags JSONB for an
// installation. Callers parse it (pipeline.loadFeatureFlags, the features
// handler); an empty or "{}" payload means "all defaults".
func (s *Store) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error) {
	return s.Q.GetInstallationFeatureFlags(ctx, installationID)
}

// UpdateInstallationFeatureFlags overwrites the feature_flags JSONB for an
// installation with an already-marshaled+clamped payload.
func (s *Store) UpdateInstallationFeatureFlags(ctx context.Context, installationID int64, flags json.RawMessage) error {
	return s.Q.UpdateInstallationFeatureFlags(ctx, db.UpdateInstallationFeatureFlagsParams{
		ID:           installationID,
		FeatureFlags: flags,
	})
}

// GetAllFileReviewsForReview returns the unfiltered per-file review payload
// (pre dedup/scoring) recorded for a review's latest run, as raw JSONB. The
// export path uses it to surface dropped findings.
func (s *Store) GetAllFileReviewsForReview(ctx context.Context, reviewID uuid.UUID) (json.RawMessage, error) {
	return s.Q.GetAllFileReviewsForReview(ctx, reviewID)
}

// GetTopChokePoints returns the highest fan-in files for a repo (up to limit) —
// the architecture-summary input.
func (s *Store) GetTopChokePoints(ctx context.Context, repoID int64, limit int32) ([]db.GetTopChokePointsRow, error) {
	return s.Q.GetTopChokePoints(ctx, db.GetTopChokePointsParams{RepoID: repoID, Limit: limit})
}

// ListArchNodes returns the per-symbol architecture rows (file, name, language,
// line span) for a repo.
func (s *Store) ListArchNodes(ctx context.Context, repoID int64) ([]db.ListArchNodesRow, error) {
	return s.Q.ListArchNodes(ctx, repoID)
}

// ListArchFileEdges returns the file→file dependency edges for a repo.
func (s *Store) ListArchFileEdges(ctx context.Context, repoID int64) ([]db.ListArchFileEdgesRow, error) {
	return s.Q.ListArchFileEdges(ctx, repoID)
}

// ListArchBugDensity returns per-file bug counts and PR-change frequency for a repo.
func (s *Store) ListArchBugDensity(ctx context.Context, repoID int64) ([]db.ListArchBugDensityRow, error) {
	return s.Q.ListArchBugDensity(ctx, repoID)
}

// ListArchCoupling returns per-PR file sets used to derive temporal coupling.
func (s *Store) ListArchCoupling(ctx context.Context, repoID int64) ([]db.ListArchCouplingRow, error) {
	return s.Q.ListArchCoupling(ctx, repoID)
}

// ListGraphNodes returns the code-graph nodes for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphNodes(ctx context.Context, repoID int64) ([]db.ListGraphNodesRow, error) {
	rows, err := s.Q.ListGraphNodes(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphNodesRow{}
	}
	return rows, err
}

// ListGraphEdges returns the code-graph edges for the repo UI, normalizing a nil
// result to an empty slice so the JSON response is [] rather than null.
func (s *Store) ListGraphEdges(ctx context.Context, repoID int64) ([]db.ListGraphEdgesRow, error) {
	rows, err := s.Q.ListGraphEdges(ctx, repoID)
	if rows == nil {
		rows = []db.ListGraphEdgesRow{}
	}
	return rows, err
}
