-- name: ListReviews :many
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at
FROM reviews WHERE repo_id = $1
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllReviewsScoped :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE r.installation_id = ANY($1::bigint[])
ORDER BY rv.created_at DESC LIMIT $2 OFFSET $3;

-- name: GetReview :one
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at, simulation_results, diagram, diagram_title
FROM reviews WHERE id = $1;

-- name: GetLastCompletedReview :one
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at
FROM reviews WHERE repo_id = $1 AND pr_number = $2 AND status = 'completed'
ORDER BY completed_at DESC LIMIT 1;

-- name: GetLatestReviewBySHA :one
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv JOIN repos r ON rv.repo_id = r.id
WHERE r.full_name = $1 AND rv.pr_number = $2 AND rv.head_sha = $3
  AND rv.status = 'completed'
ORDER BY rv.created_at DESC LIMIT 1;

-- name: GetLatestReviewByPR :one
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv JOIN repos r ON rv.repo_id = r.id
WHERE r.full_name = $1 AND rv.pr_number = $2
  AND rv.status = 'completed'
ORDER BY rv.created_at DESC LIMIT 1;

-- name: CountReviewsThisMonth :one
SELECT COUNT(*)::int FROM reviews r
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = $1
AND r.created_at >= date_trunc('month', NOW());

-- name: UpdateReviewStatus :exec
UPDATE reviews SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','failed','cancelled') THEN NOW() ELSE NULL END
WHERE id = $1;

-- name: ListReviewsScoped :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE rv.repo_id = $1 AND r.installation_id = ANY($2::bigint[])
ORDER BY rv.created_at DESC LIMIT $3 OFFSET $4;

-- name: GetRepoReviewStats :one
-- Returns averaged token + cost stats over the last N completed reviews for a repo,
-- used to estimate cost for the "Trigger review" checkbox comment. `cost_available`
-- is true only when at least one review in the sample has token_usage.total.cost.
WITH recent AS (
  SELECT token_usage FROM reviews
  WHERE repo_id = $1 AND status = 'completed' AND token_usage IS NOT NULL
  ORDER BY created_at DESC
  LIMIT $2
)
SELECT
  COUNT(*)::int AS sample_size,
  COALESCE(AVG((token_usage->'total'->>'total_tokens')::bigint), 0)::bigint AS avg_tokens,
  COALESCE(AVG(NULLIF((token_usage->'total'->>'cost')::float8, 0)), 0)::float8 AS avg_cost,
  COALESCE(BOOL_OR((token_usage->'total'->>'cost') IS NOT NULL AND (token_usage->'total'->>'cost')::float8 > 0), false) AS cost_available
FROM recent;
