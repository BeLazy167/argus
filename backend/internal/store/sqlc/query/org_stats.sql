-- name: StatsOverview :one
WITH scoped AS (
    SELECT r.* FROM reviews r
    JOIN repos rp ON r.repo_id = rp.id
    WHERE rp.installation_id = ANY(@installation_ids::bigint[])
      AND r.created_at >= NOW() - @period::interval
      AND r.status IN ('completed','failed','cancelled')
)
SELECT
    (SELECT COUNT(*) FROM scoped)::int                                           AS total_reviews,
    COALESCE((SELECT SUM((token_usage->'total'->>'cost')::float) FROM scoped WHERE token_usage IS NOT NULL), 0)::float8  AS total_cost,
    COALESCE((SELECT AVG(score) FROM scoped WHERE score IS NOT NULL), 0)::float8 AS avg_score,
    COALESCE((SELECT AVG(EXTRACT(EPOCH FROM (completed_at - created_at)))::int FROM scoped WHERE completed_at IS NOT NULL), 0)::int AS avg_review_secs,
    COALESCE((SELECT SUM((token_usage->'total'->>'total_tokens')::int) FROM scoped WHERE token_usage IS NOT NULL), 0)::int AS total_tokens,
    (SELECT COUNT(*) FROM review_comments rc JOIN scoped s ON rc.review_id = s.id WHERE rc.severity = 'critical')::int AS critical_finds,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE score IS NOT NULL), 0))::int FROM scoped), 0) AS catch_rate;

-- name: StatsTimeseries :many
SELECT
    DATE(r.created_at) AS day,
    COUNT(*)::int AS review_count,
    COALESCE(AVG(r.score)::float8, 0) AS avg_score,
    COALESCE(SUM((r.token_usage->'total'->>'cost')::float), 0)::float8 AS total_cost,
    COALESCE(SUM((r.token_usage->'total'->>'total_tokens')::int), 0)::int AS total_tokens
FROM reviews r
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
  AND r.status IN ('completed','failed','cancelled')
GROUP BY DATE(r.created_at)
ORDER BY DATE(r.created_at);

-- name: StatsUsers :many
SELECT
    r.pr_author,
    COUNT(*)::int AS review_count,
    COALESCE(AVG(r.score)::float8, 0) AS avg_score,
    COALESCE(SUM((r.token_usage->'total'->>'cost')::float), 0)::float8 AS total_cost,
    MAX(r.created_at) AS last_review_at
FROM reviews r
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
  AND r.status IN ('completed','failed','cancelled')
  AND r.pr_author IS NOT NULL AND r.pr_author != ''
GROUP BY r.pr_author
ORDER BY COUNT(*) DESC
LIMIT 50;

-- name: StatsUserCriticals :many
SELECT
    r.pr_author,
    COUNT(*)::int AS critical_count
FROM review_comments rc
JOIN reviews r ON rc.review_id = r.id
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
  AND rc.severity = 'critical'
  AND r.pr_author IS NOT NULL AND r.pr_author != ''
GROUP BY r.pr_author;

-- name: StatsFindingsBySeverity :many
SELECT
    rc.severity,
    COUNT(*)::int AS count
FROM review_comments rc
JOIN reviews r ON rc.review_id = r.id
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
GROUP BY rc.severity
ORDER BY COUNT(*) DESC;

-- name: StatsFindingsByCategory :many
SELECT
    rc.category,
    COUNT(*)::int AS count
FROM review_comments rc
JOIN reviews r ON rc.review_id = r.id
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
GROUP BY rc.category
ORDER BY COUNT(*) DESC
LIMIT 10;

-- name: StatsFindingsNewVsPattern :one
SELECT
    COUNT(*) FILTER (WHERE rc.is_new_finding = true)::int AS new_findings,
    COUNT(*) FILTER (WHERE rc.matched_pattern_id IS NOT NULL)::int AS pattern_matches
FROM review_comments rc
JOIN reviews r ON rc.review_id = r.id
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval;

-- name: StatsAdoption :one
WITH scoped AS (
    SELECT r.* FROM reviews r
    JOIN repos rp ON r.repo_id = rp.id
    WHERE rp.installation_id = ANY(@installation_ids::bigint[])
      AND r.created_at >= NOW() - @period::interval
      AND r.status IN ('completed','failed','cancelled')
)
SELECT
    COALESCE((SELECT (COUNT(*) FILTER (WHERE deep_review) * 100.0 / NULLIF(COUNT(*), 0))::float8 FROM scoped), 0) AS deep_review_pct,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE is_incremental) * 100.0 / NULLIF(COUNT(*), 0))::float8 FROM scoped), 0) AS incremental_pct,
    COALESCE((SELECT AVG(file_count)::float8 FROM scoped WHERE file_count > 0), 0) AS avg_files_per_review,
    (SELECT COUNT(DISTINCT repo_id) FROM scoped)::int AS active_repos,
    (SELECT COUNT(*) FROM repos WHERE installation_id = ANY(@installation_ids::bigint[]) AND enabled = true)::int AS total_enabled_repos;

-- name: StatsModelsRaw :many
-- Returns token_usage JSONB for all reviews in period.
-- Model aggregation is done in Go because JSONB structure has per-stage models.
SELECT r.token_usage
FROM reviews r
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = ANY(@installation_ids::bigint[])
  AND r.created_at >= NOW() - @period::interval
  AND r.status IN ('completed','failed','cancelled')
  AND r.token_usage IS NOT NULL;

-- name: ListAllReviewsScopedByAuthor :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE r.installation_id = ANY(@installation_ids::bigint[])
  AND rv.pr_author = @author
ORDER BY rv.created_at DESC LIMIT @lim OFFSET @off;
