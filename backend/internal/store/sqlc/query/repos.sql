-- name: GetRepo :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE id = $1;

-- name: GetRepoByFullName :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE full_name = $1;

-- name: UpdateRepo :one
UPDATE repos SET
    enabled = COALESCE($2, enabled),
    default_branch = COALESCE($3, default_branch),
    settings_json = CASE WHEN $4 IS NULL THEN settings_json ELSE settings_json || $4 END,
    updated_at = NOW()
WHERE id = $1
RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at;

-- name: UpsertRepo :one
INSERT INTO repos (installation_id, github_id, full_name, default_branch)
VALUES ($1, $2, $3, $4)
ON CONFLICT (github_id) DO UPDATE SET full_name = $3, default_branch = $4, updated_at = NOW()
RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at;

-- name: GetRepoScoped :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE id = $1 AND installation_id = ANY($2::bigint[]);

-- name: ListReposScoped :many
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE installation_id = ANY($1::bigint[]) ORDER BY full_name;

-- name: CountEnabledRepos :one
SELECT COUNT(*)::int FROM repos WHERE installation_id = $1 AND enabled = TRUE;
