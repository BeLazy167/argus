-- name: CreateReviewComment :exec
INSERT INTO review_comments (review_id, file_path, start_line, end_line, side, body, severity, category, specialist, confidence_score, code_snippet, github_comment_id, matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: GetReviewComments :many
SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
       specialist, confidence_score, code_snippet, github_comment_id,
       matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding,
       created_at, state, suppressed_reason, resolved_sha, attempt_generation
FROM review_comments
WHERE review_id = $1
  AND attempt_generation = (SELECT attempt_generation FROM reviews WHERE id = $1)
ORDER BY file_path, start_line;

-- name: GetCommentByGithubID :one
SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
       specialist, confidence_score, code_snippet, github_comment_id,
       matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding,
       created_at
FROM review_comments WHERE github_comment_id = $1;

-- name: RecordCommentOutcome :exec
-- Idempotent: webhook retries delivering the same reaction event produce no-op
-- second inserts instead of duplicate rows. Paired with the UNIQUE constraint
-- added in migration 037.
INSERT INTO comment_outcomes (review_comment_id, outcome)
VALUES ($1, $2)
ON CONFLICT (review_comment_id, outcome) DO NOTHING;

-- name: GetCommentOutcomes :many
SELECT id, review_comment_id, outcome, created_at
FROM comment_outcomes WHERE review_comment_id = $1 ORDER BY created_at DESC;

-- name: HydrateThreadNodeID :execrows
-- ThreadRegistry (#162): authoritatively bind a posted finding to its GraphQL
-- review-thread node id. Joined on github_comment_id (the REST comment id) — an
-- exact id match, never a proximity guess. Only fills NULL rows so a webhook
-- retry or re-post can't overwrite an already-hydrated link. Returns rows
-- affected so the caller can log hydration coverage.
UPDATE review_comments
SET graphql_thread_node_id = $1
WHERE review_id = $2 AND github_comment_id = $3 AND graphql_thread_node_id IS NULL;

-- name: GetThreadLinkForComment :one
-- ThreadRegistry lookup: the full thread identity for one finding. Powers
-- "dismissing finding X targets exactly X's thread" — the node id returned is
-- X's own, not a neighbour's picked by line proximity.
SELECT id, review_id, file_path, end_line, github_comment_id, graphql_thread_node_id
FROM review_comments WHERE id = $1;

-- name: ListThreadLinksForReview :many
-- All hydrated thread links for a review — the "threads for review R" lookup
-- consumers use instead of re-listing every GitHub thread and re-matching by
-- proximity.
SELECT id, review_id, file_path, end_line, github_comment_id, graphql_thread_node_id
FROM review_comments
WHERE review_id = $1 AND graphql_thread_node_id IS NOT NULL
ORDER BY file_path, end_line;


-- name: GetPRCompletedReviewComments :many
SELECT rc.id, rc.review_id, rc.file_path, rc.start_line, rc.end_line, rc.side, rc.body, rc.severity, rc.category,
       rc.specialist, rc.confidence_score, rc.code_snippet, rc.github_comment_id,
       rc.matched_pattern_id, rc.matched_pattern_score, rc.enforced_rule_content, rc.is_new_finding,
       rc.created_at, rc.state, rc.suppressed_reason, rc.resolved_sha, rc.attempt_generation
FROM review_comments rc
JOIN reviews r ON rc.review_id = r.id
WHERE r.repo_id = $1 AND r.pr_number = $2 AND r.status = 'completed'
  AND rc.attempt_generation = r.attempt_generation
ORDER BY rc.file_path, rc.start_line, rc.created_at;

-- name: ListPostedFindings :many
SELECT rc.id, rc.file_path, COALESCE(rc.end_line, rc.start_line, 0)::int AS line,
       rc.created_at AS posted_at, rv.head_sha
FROM review_comments rc
JOIN reviews rv ON rv.id = rc.review_id
WHERE rv.repo_id = $1 AND rv.pr_number = $2 AND rv.status = 'completed'
  AND rc.attempt_generation = rv.attempt_generation
  AND rc.suppressed_reason IS NULL
  AND rc.github_comment_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM comment_outcomes co
      WHERE co.review_comment_id = rc.id
        AND co.outcome IN ('addressed_human','addressed_agent','ignored','deferred')
  )
ORDER BY rc.created_at;
