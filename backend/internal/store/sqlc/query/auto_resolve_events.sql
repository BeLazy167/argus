-- Queries for auto_resolve_events — see migration 037.

-- name: InsertAutoResolveEvent :exec
INSERT INTO auto_resolve_events
    (installation_id, repo_id, pr_number, source_sha,
     resolved_count, attempted_count, github_api_calls,
     resolved_thread_keys)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetAutoResolveStats :one
-- Aggregate auto-resolve activity for an installation over a period.
-- period is a Postgres interval literal like '7 days' or '30 days'.
SELECT
    COUNT(*)::int                               AS event_count,
    COALESCE(SUM(resolved_count), 0)::int       AS resolved_total,
    COALESCE(SUM(attempted_count), 0)::int      AS attempted_total,
    COALESCE(SUM(github_api_calls), 0)::int     AS api_calls_total
FROM auto_resolve_events
WHERE installation_id = ANY(@installation_ids::bigint[])
  AND created_at >= NOW() - @period::interval;

-- name: GetLearnLayerCounts :one
-- Counts new rows across the learn-layer tables for an installation in a
-- period. Each subquery scopes to installation via the most direct column:
--   patterns, scenarios         — installation_id directly
--   decision_traces             — via reviews.repo_id -> repos.installation_id
--   comment_outcomes            — via review_comments.review_id -> ...
SELECT
    COALESCE((
        SELECT COUNT(*) FROM patterns p
        WHERE p.installation_id = ANY(@installation_ids::bigint[])
          AND p.created_at >= NOW() - @period::interval
    ), 0)::int AS patterns_learned,
    COALESCE((
        SELECT COUNT(*) FROM scenarios s
        WHERE s.installation_id = ANY(@installation_ids::bigint[])
          AND s.created_at >= NOW() - @period::interval
    ), 0)::int AS scenarios_stored,
    COALESCE((
        SELECT COUNT(*) FROM decision_traces dt
        JOIN repos r ON dt.repo_id = r.id
        WHERE r.installation_id = ANY(@installation_ids::bigint[])
          AND dt.created_at >= NOW() - @period::interval
    ), 0)::int AS decision_traces,
    COALESCE((
        SELECT COUNT(*) FROM comment_outcomes co
        JOIN review_comments rc ON co.review_comment_id = rc.id
        JOIN reviews rv ON rc.review_id = rv.id
        JOIN repos r ON rv.repo_id = r.id
        WHERE r.installation_id = ANY(@installation_ids::bigint[])
          AND co.created_at >= NOW() - @period::interval
    ), 0)::int AS feedback_indexed;
