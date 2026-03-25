-- name: ListProviderKeys :many
SELECT id, installation_id, repo_id, provider, api_key_enc, base_url, created_at, updated_at
FROM provider_keys WHERE installation_id = $1 ORDER BY provider, repo_id NULLS FIRST;

-- name: UpsertProviderKeyOrgLevel :one
INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, base_url)
VALUES ($1, NULL, $2, $3, $4)
ON CONFLICT (installation_id, provider) WHERE repo_id IS NULL DO UPDATE SET
    api_key_enc = EXCLUDED.api_key_enc,
    base_url = EXCLUDED.base_url,
    updated_at = NOW()
RETURNING id, installation_id, repo_id, provider, api_key_enc, base_url, created_at, updated_at;

-- name: UpsertProviderKeyRepoLevel :one
INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, base_url)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (installation_id, repo_id, provider) DO UPDATE SET
    api_key_enc = EXCLUDED.api_key_enc,
    base_url = EXCLUDED.base_url,
    updated_at = NOW()
RETURNING id, installation_id, repo_id, provider, api_key_enc, base_url, created_at, updated_at;

-- name: DeleteProviderKey :execrows
DELETE FROM provider_keys WHERE id = $1 AND installation_id = $2;

-- name: ResolveAPIKeyRepoLevel :one
SELECT api_key_enc, base_url FROM provider_keys
WHERE installation_id = $1 AND repo_id = $2 AND provider = $3;

-- name: ResolveAPIKeyOrgLevel :one
SELECT api_key_enc, base_url FROM provider_keys
WHERE installation_id = $1 AND repo_id IS NULL AND provider = $2;
