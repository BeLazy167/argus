-- Reconciler queries for cmd/reconcile-memory.
-- Find rows whose Supermemory write failed at creation time (supermemory_id
-- IS NULL) and update them once the retry succeeds. Partial indexes on
-- supermemory_id IS NULL (migration 039) keep these scans cheap.

-- name: ListPatternsPendingSM :many
-- Returns patterns with NULL supermemory_id for one installation, oldest
-- first so backfill progresses in insertion order and small incidents don't
-- starve the queue.
SELECT id, installation_id, repo_id, content, COALESCE(source, 'manual') as source, category, pr_number, created_at
FROM patterns
WHERE installation_id = $1 AND supermemory_id IS NULL
ORDER BY created_at ASC
LIMIT $2;

-- name: UpdatePatternSupermemoryID :execrows
-- :execrows (not :exec) so the reconciler can detect the row vanishing mid-run:
-- if a pattern is deleted from Postgres between the pending/repush snapshot and
-- the index call, this UPDATE matches 0 rows and the caller deletes the just-
-- created (otherwise orphaned, undeletable) Supermemory doc.
UPDATE patterns SET supermemory_id = $1, updated_at = NOW() WHERE id = $2;

-- name: GetRepoFullName :one
-- Resolve a repo's owner/name identifier for the reconciler's container-tag
-- derivation. Selects full_name (NOT the nonexistent `name` column — schema has
-- only full_name, see 001_initial) so sqlc fails generation if the schema
-- drifts. The caller splits owner/repo in Go with the SAME semantics as
-- pipeline.splitRepoFullName so container tags / customIDs match the live
-- pipeline's writes instead of landing in a divergent container.
SELECT full_name FROM repos WHERE id = $1;

-- name: InstallationHasSMKey :one
-- Whether an installation has a non-empty encrypted Supermemory key. Lets the
-- reconciler distinguish "no key configured" (BYOK optional — skip quietly at
-- Debug) from "key present but the client couldn't be built" (decrypt/load
-- failure — Warn + count in the run summary).
SELECT (supermemory_key_enc IS NOT NULL AND supermemory_key_enc <> '')::boolean AS has_key
FROM installations WHERE id = $1;

-- name: ListAllPatternsForRepush :many
-- Full re-push (reconcile-memory --full): every pattern for one installation
-- regardless of supermemory_id, so the unified {repo}/_shared containers get
-- seeded and Supermemory rebuilds the relationship graph. The result set does
-- NOT shrink as rows are processed (unlike the pending sweep), so the caller
-- must do a single bounded pass — LIMIT caps the run, no loop-requery.
SELECT id, installation_id, repo_id, content, COALESCE(source, 'manual') as source, category, pr_number, created_at
FROM patterns
WHERE installation_id = $1
ORDER BY created_at ASC
LIMIT $2;

-- name: ListScenariosPendingSM :many
-- Active scenarios only; retired ones are excluded from the reconciler sweep
-- because retrying a Supermemory write for a deactivated scenario would
-- re-surface it in specialist retrieval. repo_id IS NOT NULL: issue-webhook
-- scenarios for un-synced repos are created with NULL repo_id (handlers_
-- scenarios.go) — they can never resolve to a {repo} container, so leaving
-- them in the sweep parks permanent-failure rows at the head of the created_at
-- ASC page and wedges the circuit breaker every night.
SELECT id, installation_id, repo_id, description, severity, files
FROM scenarios
WHERE installation_id = $1 AND supermemory_id IS NULL AND active = TRUE
  AND repo_id IS NOT NULL
ORDER BY created_at ASC
LIMIT $2;

-- name: UpdateScenarioSupermemoryID :exec
UPDATE scenarios SET supermemory_id = $1 WHERE id = $2;

-- name: ListAllScenariosForRepush :many
-- Full re-push sibling of ListAllPatternsForRepush. Active scenarios only,
-- matching the pending sweep's filter so retired scenarios stay out of
-- specialist retrieval. repo_id IS NOT NULL for the same reason as the pending
-- sweep — NULL-repo scenarios cannot route to a {repo} container. Single
-- bounded pass — see ListAllPatternsForRepush.
SELECT id, installation_id, repo_id, description, severity, files
FROM scenarios
WHERE installation_id = $1 AND active = TRUE
  AND repo_id IS NOT NULL
ORDER BY created_at ASC
LIMIT $2;

-- name: ListTracesPendingSM :many
-- Joined against repos to surface installation_id since decision_traces is
-- repo-scoped only in the base schema. The reconciler uses installation_id to
-- resolve the per-installation Supermemory key.
SELECT dt.id, dt.repo_id, dt.file_path, dt.trace_type, dt.content, COALESCE(dt.severity, '') as severity, r.installation_id
FROM decision_traces dt
JOIN repos r ON r.id = dt.repo_id
WHERE r.installation_id = $1 AND dt.supermemory_id IS NULL
ORDER BY dt.created_at ASC
LIMIT $2;

-- name: UpdateTraceSupermemoryID :exec
UPDATE decision_traces SET supermemory_id = $1 WHERE id = $2;

-- name: ListAllTracesForRepush :many
-- Full re-push sibling of ListAllPatternsForRepush. Joined against repos for
-- installation_id, same as the pending sweep. Single bounded pass.
SELECT dt.id, dt.repo_id, dt.file_path, dt.trace_type, dt.content, COALESCE(dt.severity, '') as severity, r.installation_id
FROM decision_traces dt
JOIN repos r ON r.id = dt.repo_id
WHERE r.installation_id = $1
ORDER BY dt.created_at ASC
LIMIT $2;

-- name: ListInstallationsWithSMKey :many
-- Every installation that has a configured Supermemory key. The reconciler
-- iterates over these and runs the drift-repair sweep for each.
SELECT id FROM installations
WHERE supermemory_key_enc IS NOT NULL AND supermemory_key_enc <> ''
ORDER BY id ASC;
