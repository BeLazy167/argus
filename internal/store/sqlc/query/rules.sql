-- name: ListRules :many
SELECT id, category, content, priority, enabled, created_at, updated_at
FROM rules ORDER BY priority DESC, category;

-- name: CreateRule :one
INSERT INTO rules (category, content, priority, enabled)
VALUES ($1, $2, $3, $4)
RETURNING id, category, content, priority, enabled, created_at, updated_at;

-- name: UpdateRule :one
UPDATE rules SET
    category = COALESCE($2, category),
    content = COALESCE($3, content),
    priority = COALESCE($4, priority),
    enabled = COALESCE($5, enabled),
    updated_at = NOW()
WHERE id = $1
RETURNING id, category, content, priority, enabled, created_at, updated_at;

-- name: DeleteRule :execrows
DELETE FROM rules WHERE id = $1;
