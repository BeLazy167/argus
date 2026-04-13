-- name: ListRules :many
SELECT id, installation_id, category, content, priority, enabled, created_at, updated_at
FROM rules WHERE installation_id = ANY($1::bigint[]) ORDER BY priority DESC, category;

-- name: CreateRule :one
INSERT INTO rules (installation_id, category, content, priority, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at;

-- name: UpdateRule :one
UPDATE rules SET
    category = COALESCE($3, category),
    content = COALESCE($4, content),
    priority = COALESCE($5, priority),
    enabled = COALESCE($6, enabled),
    updated_at = NOW()
WHERE id = $1 AND installation_id = ANY($2::bigint[])
RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at;

-- name: DeleteRule :execrows
DELETE FROM rules WHERE id = $1 AND installation_id = ANY($2::bigint[]);
