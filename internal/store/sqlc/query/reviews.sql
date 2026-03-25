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
       deep_review, persona, is_incremental, created_at, completed_at
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
UPDATE reviews SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE NULL END
WHERE id = $1;

-- name: ListReviewsScoped :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE rv.repo_id = $1 AND r.installation_id = ANY($2::bigint[])
ORDER BY rv.created_at DESC LIMIT $3 OFFSET $4;
