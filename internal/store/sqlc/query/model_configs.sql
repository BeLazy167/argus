-- name: ListModelConfigs :many
SELECT id, repo_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at
FROM model_configs WHERE repo_id = $1 ORDER BY stage;

-- name: UpsertModelConfig :one
INSERT INTO model_configs (repo_id, stage, provider, model, base_url, max_tokens, temperature)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (repo_id, stage) DO UPDATE SET
    provider = EXCLUDED.provider,
    model = EXCLUDED.model,
    base_url = EXCLUDED.base_url,
    max_tokens = EXCLUDED.max_tokens,
    temperature = EXCLUDED.temperature,
    updated_at = NOW()
RETURNING id, repo_id, stage, provider, model, base_url, max_tokens, temperature, created_at, updated_at;

-- name: DeleteModelConfig :execrows
DELETE FROM model_configs WHERE repo_id = $1 AND stage = $2;
