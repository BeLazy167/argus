-- name: ListPatterns :many
SELECT id, installation_id, repo_id, content, supermemory_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE installation_id = ANY($1::bigint[]) ORDER BY created_at DESC;

-- name: ListPatternsForRepo :many
SELECT id, installation_id, repo_id, content, supermemory_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE installation_id = ANY($1::bigint[]) AND (repo_id IS NULL OR repo_id = $2) ORDER BY created_at DESC;

-- name: CreatePattern :one
INSERT INTO patterns (installation_id, repo_id, content, supermemory_id, created_by, source, category, pr_number)
VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'manual'), $7, $8)
RETURNING id, installation_id, repo_id, content, supermemory_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at;

-- name: GetPattern :one
SELECT id, installation_id, repo_id, content, supermemory_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE id = $1;

-- name: DeletePattern :execrows
DELETE FROM patterns WHERE id = $1 AND installation_id = ANY($2::bigint[]);

-- name: GetPatternStats :many
SELECT DATE_TRUNC('week', created_at) as week, COALESCE(source, 'manual') as source, COUNT(*)::int as count
FROM patterns WHERE installation_id = ANY($1::bigint[])
GROUP BY week, source ORDER BY week;
