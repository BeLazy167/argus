-- name: CreateReviewComment :exec
INSERT INTO review_comments (review_id, file_path, start_line, end_line, side, body, severity, category, specialist, confidence_score, code_snippet, github_comment_id, matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: GetReviewComments :many
SELECT id, review_id, file_path, start_line, end_line, side, body, severity, category,
       specialist, confidence_score, code_snippet, github_comment_id,
       matched_pattern_id, matched_pattern_score, enforced_rule_content, is_new_finding,
       created_at
FROM review_comments WHERE review_id = $1 ORDER BY file_path, start_line;

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
