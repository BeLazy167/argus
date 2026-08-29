-- name: ListRules :many
SELECT id, installation_id, category, content, priority, enabled, created_at, updated_at
FROM rules WHERE installation_id = ANY($1::bigint[]) ORDER BY priority DESC, category;

-- name: CreateRule :one
INSERT INTO rules (installation_id, category, content, priority, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at;

-- name: UpdateRule :one
UPDATE rules SET
    category = COALESCE(sqlc.narg(category)::text, category),
    content = COALESCE(sqlc.narg(content)::text, content),
    priority = COALESCE(sqlc.narg(priority)::int, priority),
    enabled = COALESCE(sqlc.narg(enabled)::boolean, enabled),
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint AND installation_id = ANY(sqlc.arg(installation_ids)::bigint[])
RETURNING id, installation_id, category, content, priority, enabled, created_at, updated_at;

-- name: DeleteRule :one
DELETE FROM rules WHERE id = sqlc.arg(id)::bigint AND installation_id = ANY(sqlc.arg(installation_ids)::bigint[])
RETURNING installation_id;
