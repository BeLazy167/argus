-- name: ListProviderKeys :many
SELECT id, installation_id, repo_id, provider, api_key_enc, base_url, model, key_hint, created_at, updated_at
FROM provider_keys WHERE installation_id = $1 ORDER BY provider, repo_id NULLS FIRST;

-- name: UpsertProviderKeyOrgLevel :one
-- PATCH semantics on conflict (Store.UpsertProviderKey delegates here):
-- omitted base_url/model preserve stored values; an empty api_key_enc preserves
-- the stored key (keyless config updates must not destroy a stored key).
INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, base_url, key_hint, model)
VALUES ($1, NULL, $2, $3, $4, $5, $6)
ON CONFLICT (installation_id, provider) WHERE repo_id IS NULL DO UPDATE SET
    api_key_enc = COALESCE(NULLIF(EXCLUDED.api_key_enc, ''), provider_keys.api_key_enc),
    key_hint = COALESCE(NULLIF(EXCLUDED.key_hint, ''), provider_keys.key_hint),
    base_url = COALESCE(EXCLUDED.base_url, provider_keys.base_url),
    model = COALESCE(EXCLUDED.model, provider_keys.model),
    updated_at = NOW()
RETURNING id, installation_id, repo_id, provider, api_key_enc, base_url, model, key_hint, created_at, updated_at;

-- name: UpsertProviderKeyRepoLevel :one
INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, base_url, key_hint, model)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (installation_id, repo_id, provider) DO UPDATE SET
    api_key_enc = COALESCE(NULLIF(EXCLUDED.api_key_enc, ''), provider_keys.api_key_enc),
    key_hint = COALESCE(NULLIF(EXCLUDED.key_hint, ''), provider_keys.key_hint),
    base_url = COALESCE(EXCLUDED.base_url, provider_keys.base_url),
    model = COALESCE(EXCLUDED.model, provider_keys.model),
    updated_at = NOW()
RETURNING id, installation_id, repo_id, provider, api_key_enc, base_url, model, key_hint, created_at, updated_at;

-- name: DeleteProviderKey :one
DELETE FROM provider_keys WHERE id = $1 AND installation_id = $2
RETURNING provider;

-- name: ResolveAPIKeyRepoLevel :one
SELECT api_key_enc, base_url FROM provider_keys
WHERE installation_id = $1 AND repo_id = $2 AND provider = $3;

-- name: ResolveAPIKeyOrgLevel :one
SELECT api_key_enc, base_url FROM provider_keys
WHERE installation_id = $1 AND repo_id IS NULL AND provider = $2;
