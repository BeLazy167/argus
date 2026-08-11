-- name: GetStats :one
-- critical_finds excludes state='suppressed': those findings were generated and
-- then withheld, so no PR author ever received them. Counting them advertises
-- review coverage that was never delivered. Kept in lockstep with the live
-- raw-SQL Store.GetStats — the sqlc migration swaps one for the other, and a
-- predicate on only one half is how #239 happened.
SELECT
    (SELECT COUNT(*) FROM reviews
     WHERE NOT (github_review_id IS NULL AND status = 'failed' AND error IN ('auto_run_disabled', 'no_api_key')))::int as total_reviews,
    (SELECT COUNT(*) FROM reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int as completed_today,
    COALESCE((SELECT AVG(score)::int FROM reviews WHERE score IS NOT NULL), 0)::int as avg_score,
    (SELECT COUNT(*) FROM repos WHERE enabled = true)::int as active_repos,
    (SELECT COUNT(*) FROM review_comments rc JOIN reviews rv ON rv.id = rc.review_id WHERE rc.attempt_generation = rv.attempt_generation AND rc.severity = 'critical' AND rc.state <> 'suppressed')::int as critical_finds,
    (SELECT COUNT(*) FROM reviews WHERE status IN ('pending','in_progress'))::int as pending_reviews,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM reviews), 0)::int as catch_rate,
    (SELECT COUNT(*) FROM reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int as prs_this_week,
    (SELECT COUNT(*) FROM reviews WHERE score IS NOT NULL AND score <= 4)::int as high_risk_count,
    COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM reviews WHERE completed_at IS NOT NULL), 0)::int as avg_review_time_ms,
    (SELECT COUNT(*) FROM reviews WHERE deep_review = true)::int as deep_review_count;

-- name: GetStatsScoped :one
WITH scoped_reviews AS (
    SELECT * FROM reviews WHERE repo_id IN (SELECT id FROM repos WHERE installation_id = ANY($1::bigint[]))
)
SELECT
    (SELECT COUNT(*) FROM scoped_reviews
     WHERE NOT (github_review_id IS NULL AND status = 'failed' AND error IN ('auto_run_disabled', 'no_api_key')))::int as total_reviews,
    (SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= CURRENT_DATE AND status = 'completed')::int as completed_today,
    COALESCE((SELECT AVG(score)::int FROM scoped_reviews WHERE score IS NOT NULL), 0)::int as avg_score,
    (SELECT COUNT(*) FROM repos WHERE installation_id = ANY($1::bigint[]) AND enabled = true)::int as active_repos,
    (SELECT COUNT(*) FROM review_comments rc JOIN scoped_reviews rv ON rv.id = rc.review_id WHERE rc.attempt_generation = rv.attempt_generation AND rc.severity = 'critical' AND rc.state <> 'suppressed')::int as critical_finds,
    (SELECT COUNT(*) FROM scoped_reviews WHERE status IN ('pending','in_progress'))::int as pending_reviews,
    COALESCE((SELECT (COUNT(*) FILTER (WHERE score < 10) * 100 / NULLIF(COUNT(*) FILTER (WHERE status = 'completed'), 0))::int FROM scoped_reviews), 0)::int as catch_rate,
    (SELECT COUNT(*) FROM scoped_reviews WHERE created_at >= NOW() - INTERVAL '7 days')::int as prs_this_week,
    (SELECT COUNT(*) FROM scoped_reviews WHERE score IS NOT NULL AND score <= 4)::int as high_risk_count,
    COALESCE((SELECT (AVG(EXTRACT(EPOCH FROM (completed_at - created_at)) * 1000))::int FROM scoped_reviews WHERE completed_at IS NOT NULL), 0)::int as avg_review_time_ms,
    (SELECT COUNT(*) FROM scoped_reviews WHERE deep_review = true)::int as deep_review_count;

-- name: ListActivity :many
SELECT id, installation_id, action, actor, resource, metadata, created_at
FROM activity_log WHERE installation_id = ANY($1::bigint[]) ORDER BY created_at DESC LIMIT sqlc.arg(row_limit)::bigint;

-- name: LogActivity :exec
INSERT INTO activity_log (installation_id, action, actor, resource, metadata)
VALUES ($1, $2, $3, $4, $5);

-- name: ListReviewGauge :many
SELECT installation_id, category, change_class, posted_findings,
       addressed_human, addressed_agent, dismissed, ignored, deferred,
       address_rate, dismiss_rate, median_seconds_to_merge
FROM vw_review_gauge
WHERE installation_id = ANY($1::bigint[])
ORDER BY category, change_class;
