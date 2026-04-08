-- name: GetStats :one
SELECT
    (SELECT COUNT(*) FROM reviews)::int as total_reviews,
    (SELECT COUNT(*) FROM reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int as completed_today,
    COALESCE((SELECT AVG(score)::int FROM reviews WHERE score IS NOT NULL), 0) as avg_score,
    (SELECT COUNT(*) FROM repos WHERE enabled = true)::int as active_repos,
    (SELECT COUNT(*) FROM review_comments WHERE severity = 'critical')::int as critical_finds,
    (SELECT COUNT(*) FROM reviews WHERE status IN ('pending','in_progress'))::int as pending_reviews,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM reviews), 0) as catch_rate,
    (SELECT COUNT(*) FROM reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int as prs_this_week,
    (SELECT COUNT(*) FROM reviews WHERE score IS NOT NULL AND score <= 4)::int as high_risk_count,
    COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM reviews WHERE completed_at IS NOT NULL), 0) as avg_review_time_ms,
    (SELECT COUNT(*) FROM reviews WHERE deep_review = true)::int as deep_review_count;

-- name: GetStatsScoped :one
WITH scoped_reviews AS (
    SELECT * FROM reviews WHERE repo_id IN (SELECT id FROM repos WHERE installation_id = ANY($1::bigint[]))
)
SELECT
    (SELECT COUNT(*) FROM scoped_reviews)::int as total_reviews,
    (SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int as completed_today,
    COALESCE((SELECT AVG(score)::int FROM scoped_reviews WHERE score IS NOT NULL), 0) as avg_score,
    (SELECT COUNT(*) FROM repos WHERE installation_id = ANY($1::bigint[]) AND enabled = true)::int as active_repos,
    (SELECT COUNT(*) FROM review_comments WHERE review_id IN (SELECT id FROM scoped_reviews) AND severity = 'critical')::int as critical_finds,
    (SELECT COUNT(*) FROM scoped_reviews WHERE status IN ('pending','in_progress'))::int as pending_reviews,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM scoped_reviews), 0) as catch_rate,
    (SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int as prs_this_week,
    (SELECT COUNT(*) FROM scoped_reviews WHERE score IS NOT NULL AND score <= 4)::int as high_risk_count,
    COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM scoped_reviews WHERE completed_at IS NOT NULL), 0) as avg_review_time_ms,
    (SELECT COUNT(*) FROM scoped_reviews WHERE deep_review = true)::int as deep_review_count;

-- name: ListActivity :many
SELECT id, installation_id, action, actor, resource, metadata, created_at
FROM activity_log WHERE installation_id = ANY($1::bigint[]) ORDER BY created_at DESC LIMIT $2;

-- name: LogActivity :exec
INSERT INTO activity_log (installation_id, action, actor, resource, metadata)
VALUES ($1, $2, $3, $4, $5);
