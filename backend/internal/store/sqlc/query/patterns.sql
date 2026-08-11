-- name: ListPatterns :many
SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE installation_id = ANY($1::bigint[]) ORDER BY created_at DESC;

-- name: ListPatternsForRepo :many
SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE installation_id = ANY($1::bigint[]) AND (repo_id IS NULL OR repo_id = $2) ORDER BY created_at DESC;

-- name: CreatePattern :one
INSERT INTO patterns (installation_id, repo_id, content, memory_doc_id, created_by, source, category, pr_number)
VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'manual'), $7, $8)
RETURNING id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at;

-- name: GetPattern :one
SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE id = $1;

-- name: DeletePattern :execrows
DELETE FROM patterns WHERE id = $1 AND installation_id = ANY($2::bigint[]);

-- name: GetPatternStats :many
SELECT DATE_TRUNC('week', created_at)::timestamptz AS week, COALESCE(source, 'manual') as source, COUNT(*)::int as count
FROM patterns WHERE installation_id = ANY($1::bigint[])
GROUP BY week, source ORDER BY week;

-- name: GetLowQualityPatterns :many
SELECT id, installation_id, repo_id, memory_doc_id, content_hash, category,
       times_matched, times_confirmed, times_dismissed, quality_score,
       last_matched_at, created_at, updated_at
FROM pattern_stats
WHERE installation_id = $1 AND quality_score <= $2
ORDER BY quality_score ASC
LIMIT sqlc.arg(row_limit)::bigint;
