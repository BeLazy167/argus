-- name: CreateScenario :one
INSERT INTO scenarios (installation_id, repo_id, description, source, source_ref, files, modules, severity)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: CreatePendingScenario :one
INSERT INTO scenarios (installation_id, repo_id, description, source, source_ref, files, modules, severity, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, FALSE)
RETURNING id;

-- name: ActivateScenario :exec
UPDATE scenarios SET active = TRUE WHERE id = $1;

-- name: ListScenariosForFiles :many
SELECT id, installation_id, repo_id, description, source, COALESCE(source_ref,'') as source_ref, files, modules, COALESCE(severity,'medium') as severity, active, created_at, COALESCE(steps,'[]') as steps, COALESCE(initial_state,'') as initial_state, COALESCE(expected_outcome,'') as expected_outcome, COALESCE(is_outdated,FALSE) as is_outdated, last_run_at
FROM scenarios
WHERE repo_id = $1 AND active = TRUE AND files && $2::text[]
ORDER BY created_at DESC
LIMIT 20;

-- name: ListScenariosForRepo :many
SELECT id, installation_id, repo_id, description, source, COALESCE(source_ref,'') as source_ref, files, modules, COALESCE(severity,'medium') as severity, active, created_at, COALESCE(steps,'[]') as steps, COALESCE(initial_state,'') as initial_state, COALESCE(expected_outcome,'') as expected_outcome, COALESCE(is_outdated,FALSE) as is_outdated, last_run_at
FROM scenarios
WHERE repo_id = $1
ORDER BY active DESC, created_at DESC
LIMIT $2;

-- name: DeactivateScenario :exec
UPDATE scenarios SET active = FALSE WHERE id = $1;

-- name: DeactivateScenarioScoped :execrows
UPDATE scenarios SET active = FALSE WHERE id = $1 AND installation_id = ANY($2::bigint[]);

-- name: MarkScenarioOutdated :exec
UPDATE scenarios SET is_outdated = TRUE
WHERE repo_id = $1 AND active = TRUE AND files && $2::text[];

-- name: UpdateScenarioLastRun :exec
UPDATE scenarios SET last_run_at = NOW(), is_outdated = FALSE WHERE id = $1;
