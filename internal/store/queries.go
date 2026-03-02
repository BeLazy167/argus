package store

import (
	"context"
	"fmt"
	"strings"

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
		RETURNING id, installation_id, org_login, created_at, suspended_at
	`, installationID, orgLogin).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.CreatedAt, &inst.SuspendedAt)
	return &inst, err
}

func (s *Store) ListInstallations(ctx context.Context) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, installation_id, org_login, created_at, suspended_at FROM installations ORDER BY created_at DESC`)
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

func (s *Store) ListUserInstallations(ctx context.Context, clerkUserID string) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id, i.installation_id, i.org_login, i.created_at, i.suspended_at
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
		SELECT id, installation_id, org_login, created_at, suspended_at
		FROM installations WHERE id = $1
	`, id).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.CreatedAt, &inst.SuspendedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Store) GetInstallationByGitHubID(ctx context.Context, ghInstallationID int64) (*Installation, error) {
	var inst Installation
	err := s.Pool.QueryRow(ctx, `
		SELECT id, installation_id, org_login, created_at, suspended_at
		FROM installations WHERE installation_id = $1
	`, ghInstallationID).Scan(&inst.ID, &inst.InstallationID, &inst.OrgLogin, &inst.CreatedAt, &inst.SuspendedAt)
	if err != nil {
		return nil, err
	}
	return &inst, nil
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
			settings_json = COALESCE($4, settings_json),
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

func (s *Store) ListReviews(ctx context.Context, repoID int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, github_review_id,
		       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
		       deep_review, persona, is_incremental, created_at, completed_at
		FROM reviews WHERE repo_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, repoID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Review])
}

func (s *Store) GetReview(ctx context.Context, id uuid.UUID) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, github_review_id,
		       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
		       deep_review, persona, is_incremental, created_at, completed_at
		FROM reviews WHERE id = $1
	`, id).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetReviewComments(ctx context.Context, reviewID uuid.UUID) ([]ReviewComment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
		       specialist, confidence_score, code_snippet, github_comment_id, created_at
		FROM review_comments WHERE review_id = $1 ORDER BY file_path, start_line
	`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ReviewComment])
}

func (s *Store) UpdateReviewStatus(ctx context.Context, id uuid.UUID, status, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE reviews SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE NULL END
		WHERE id = $1
	`, id, status, nilIfEmpty(errMsg))
	return err
}

func (s *Store) ListReviewsScoped(ctx context.Context, repoID int64, installationIDs []int64, limit, offset int) ([]Review, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, rv.github_review_id,
		       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
		FROM reviews rv
		JOIN repos r ON rv.repo_id = r.id
		WHERE rv.repo_id = $1 AND r.installation_id = ANY($2)
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
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, rv.github_review_id,
		       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
		FROM reviews rv
		JOIN repos r ON rv.repo_id = r.id
		WHERE r.installation_id = ANY($1)
		ORDER BY rv.created_at DESC LIMIT $2 OFFSET $3
	`, installationIDs, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Review])
}

// --- Rules ---

func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, category, content, priority, enabled, created_at, updated_at
		FROM rules ORDER BY priority DESC, category
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Rule])
}

func (s *Store) CreateRule(ctx context.Context, category, content string, priority int, enabled bool) (*Rule, error) {
	var r Rule
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO rules (category, content, priority, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id, category, content, priority, enabled, created_at, updated_at
	`, category, content, priority, enabled).Scan(&r.ID, &r.Category, &r.Content, &r.Priority, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (s *Store) UpdateRule(ctx context.Context, id int64, category, content *string, priority *int, enabled *bool) (*Rule, error) {
	var r Rule
	err := s.Pool.QueryRow(ctx, `
		UPDATE rules SET
			category = COALESCE($2, category),
			content = COALESCE($3, content),
			priority = COALESCE($4, priority),
			enabled = COALESCE($5, enabled),
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, category, content, priority, enabled, created_at, updated_at
	`, id, category, content, priority, enabled).Scan(&r.ID, &r.Category, &r.Content, &r.Priority, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM rules WHERE id = $1`, id)
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
		SELECT id, repo_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
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
		RETURNING id, repo_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
	`, repoID, stage, provider, model, baseURL, maxTokens, temperature).Scan(
		&mc.ID, &mc.RepoID, &mc.Stage, &mc.Provider, &mc.Model, &mc.BaseURL,
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

// --- Review Comments ---

func (s *Store) CreateReviewComment(ctx context.Context, reviewID uuid.UUID, filePath string, startLine, endLine *int, side *string, body string, severity, category, specialist, codeSnippet *string, confidenceScore *int, githubCommentID *int64) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO review_comments (review_id, file_path, start_line, end_line, side, body, severity, category, specialist, confidence_score, code_snippet, github_comment_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, reviewID, filePath, startLine, endLine, side, body, severity, category, specialist, confidenceScore, codeSnippet, githubCommentID)
	return err
}

// GetCommentByGithubID looks up a review comment by its GitHub comment ID.
func (s *Store) GetCommentByGithubID(ctx context.Context, githubCommentID int64) (*ReviewComment, error) {
	var c ReviewComment
	err := s.Pool.QueryRow(ctx, `
		SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
		       specialist, confidence_score, code_snippet, github_comment_id, created_at
		FROM review_comments WHERE github_comment_id = $1
	`, githubCommentID).Scan(&c.ID, &c.ReviewID, &c.FilePath, &c.StartLine, &c.EndLine, &c.Side, &c.Body, &c.Severity, &c.Category,
		&c.Specialist, &c.ConfidenceScore, &c.CodeSnippet, &c.GithubCommentID, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetLastCompletedReview returns the most recent completed review for a repo+PR.
func (s *Store) GetLastCompletedReview(ctx context.Context, repoID int64, prNumber int) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, github_review_id,
		       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
		       deep_review, persona, is_incremental, created_at, completed_at
		FROM reviews WHERE repo_id = $1 AND pr_number = $2 AND status = 'completed'
		ORDER BY completed_at DESC LIMIT 1
	`, repoID, prNumber).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetLatestReviewBySHA(ctx context.Context, repoFullName string, prNumber int, headSHA string) (*Review, error) {
	var r Review
	err := s.Pool.QueryRow(ctx, `
		SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, rv.github_review_id,
		       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
		       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
		FROM reviews rv JOIN repos r ON rv.repo_id = r.id
		WHERE r.full_name = $1 AND rv.pr_number = $2 AND rv.head_sha = $3
		  AND rv.status = 'completed'
		ORDER BY rv.created_at DESC LIMIT 1
	`, repoFullName, prNumber, headSHA).Scan(&r.ID, &r.RepoID, &r.PRNumber, &r.PRTitle, &r.PRAuthor, &r.HeadSHA, &r.BaseSHA, &r.GithubReviewID,
		&r.Status, &r.Summary, &r.Score, &r.TokenUsage, &r.Trigger, &r.TriggeredBy, &r.DurationMs, &r.Error,
		&r.DeepReview, &r.Persona, &r.IsIncremental, &r.CreatedAt, &r.CompletedAt)
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
			(SELECT COUNT(*) FROM reviews)::int,
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
			(SELECT COUNT(*) FROM scoped_reviews)::int,
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

func (s *Store) ListActivity(ctx context.Context, limit int) ([]ActivityLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, action, actor, resource, metadata, created_at
		FROM activity_log ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ActivityLog])
}

func (s *Store) LogActivity(ctx context.Context, action, actor, resource string, metadata []byte) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO activity_log (action, actor, resource, metadata)
		VALUES ($1, $2, $3, $4)
	`, action, nilIfEmpty(actor), nilIfEmpty(resource), metadata)
	return err
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
