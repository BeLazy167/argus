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

-- name: GetAllFileReviewsForReview :one
-- Returns the unfiltered comments (before dedup/scoring) from the latest pipeline run for a review.
-- Used by the export endpoint to surface dropped findings.
-- Explicit ::jsonb cast is needed so sqlc infers a concrete type and applies
-- the RawMessage override from sqlc.yaml (otherwise it defaults to interface{}
-- which pgx decodes via json.Unmarshal, breaking the []byte assertion in export).
SELECT (payload->'AllFileReviews')::jsonb AS all_file_reviews
FROM pipeline_states
WHERE review_id = $1
ORDER BY updated_at DESC
LIMIT 1;
