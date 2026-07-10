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
