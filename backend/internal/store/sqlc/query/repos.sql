-- name: GetRepo :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE id = $1;

-- name: GetRepoByFullName :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE full_name = $1;

-- name: UpdateRepo :one
UPDATE repos SET
    enabled = COALESCE(sqlc.narg(enabled)::boolean, enabled),
    default_branch = COALESCE(sqlc.narg(default_branch)::text, default_branch),
    settings_json = CASE WHEN sqlc.narg(settings_json)::jsonb IS NULL THEN settings_json ELSE settings_json || sqlc.narg(settings_json)::jsonb END,
    graph_default_head_sha = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NULL
        ELSE graph_default_head_sha END,
    graph_default_head_observed_at = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NULL
        ELSE graph_default_head_observed_at END,
    graph_default_head_event_at = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NULL
        ELSE graph_default_head_event_at END,
    graph_refresh_requested_at = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NOW()
        ELSE graph_refresh_requested_at END,
    graph_refresh_commit_sha = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NULL
        ELSE graph_refresh_commit_sha END,
    graph_refresh_version = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN graph_refresh_version + 1
        ELSE graph_refresh_version END,
    graph_index_attempted_at = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN NULL
        ELSE graph_index_attempted_at END,
    graph_index_cursor = CASE
        WHEN sqlc.narg(default_branch)::text IS NOT NULL AND default_branch IS DISTINCT FROM sqlc.narg(default_branch)::text THEN 0
        ELSE graph_index_cursor END,
    updated_at = NOW()
WHERE id = sqlc.arg(id)::bigint
RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at;

-- name: UpsertRepo :one
INSERT INTO repos (installation_id, github_id, full_name, default_branch)
VALUES ($1, $2, $3, $4)
ON CONFLICT (github_id) DO UPDATE SET
    full_name = EXCLUDED.full_name,
    default_branch = EXCLUDED.default_branch,
    graph_default_head_sha = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NULL ELSE repos.graph_default_head_sha END,
    graph_default_head_observed_at = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NULL ELSE repos.graph_default_head_observed_at END,
    graph_default_head_event_at = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NULL ELSE repos.graph_default_head_event_at END,
    graph_refresh_requested_at = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NOW() ELSE repos.graph_refresh_requested_at END,
    graph_refresh_commit_sha = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NULL ELSE repos.graph_refresh_commit_sha END,
    graph_refresh_version = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN repos.graph_refresh_version + 1 ELSE repos.graph_refresh_version END,
    graph_index_attempted_at = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN NULL ELSE repos.graph_index_attempted_at END,
    graph_index_cursor = CASE WHEN repos.default_branch IS DISTINCT FROM EXCLUDED.default_branch THEN 0 ELSE repos.graph_index_cursor END,
    updated_at = NOW()
RETURNING id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at;

-- name: GetRepoScoped :one
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE id = $1 AND installation_id = ANY($2::bigint[]);

-- name: ListReposScoped :many
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos WHERE installation_id = ANY($1::bigint[]) ORDER BY full_name;

-- name: CountEnabledRepos :one
SELECT COUNT(*)::int FROM repos WHERE installation_id = $1 AND enabled = TRUE;

-- name: ListRepos :many
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos
ORDER BY full_name;

-- name: ListReposByOwner :many
SELECT id, installation_id, github_id, full_name, default_branch, enabled, settings_json, created_at, updated_at
FROM repos
WHERE full_name LIKE $1::text ESCAPE '\'
ORDER BY full_name;
