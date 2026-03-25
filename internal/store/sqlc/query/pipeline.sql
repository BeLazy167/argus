-- name: UpsertPipelineState :exec
INSERT INTO pipeline_states (id, review_id, state, payload, error, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (id) DO UPDATE SET
    state = EXCLUDED.state,
    payload = EXCLUDED.payload,
    error = EXCLUDED.error,
    updated_at = NOW();

-- name: LoadPipelineState :one
SELECT payload FROM pipeline_states WHERE id = $1;

-- name: ListIncompleteRuns :many
SELECT id FROM pipeline_states WHERE state NOT IN ($1, $2) ORDER BY updated_at;

-- name: GetLatestRunForReview :one
SELECT id FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1;
