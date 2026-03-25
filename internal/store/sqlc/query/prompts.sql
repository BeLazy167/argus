-- name: ListPromptTemplates :many
SELECT id, repo_id, stage, prompt_text, created_at, updated_at
FROM prompt_templates WHERE repo_id = $1 ORDER BY stage;

-- name: UpsertPromptTemplate :one
INSERT INTO prompt_templates (repo_id, stage, prompt_text)
VALUES ($1, $2, $3)
ON CONFLICT (repo_id, stage) DO UPDATE SET
    prompt_text = EXCLUDED.prompt_text,
    updated_at = NOW()
RETURNING id, repo_id, stage, prompt_text, created_at, updated_at;

-- name: DeletePromptTemplate :execrows
DELETE FROM prompt_templates WHERE repo_id = $1 AND stage = $2;
