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
SELECT
    id, installation_id, repo_id, description, source,
    COALESCE(source_ref,'') AS source_ref, files, modules,
    COALESCE(severity,'medium') AS severity, active, created_at,
    COALESCE(steps,'[]') AS steps,
    COALESCE(initial_state,'') AS initial_state,
    COALESCE(expected_outcome,'') AS expected_outcome,
    COALESCE(is_outdated,FALSE) AS is_outdated,
    last_run_at,
    last_verdict, last_confidence, last_why, last_fix, last_pr_number, last_review_id,
    trigger_count
FROM scenarios
WHERE repo_id = $1 AND active = TRUE AND files && $2::text[]
ORDER BY trigger_count DESC, created_at DESC
LIMIT 20;

-- name: ListScenariosForRepo :many
SELECT
    id, installation_id, repo_id, description, source,
    COALESCE(source_ref,'') AS source_ref, files, modules,
    COALESCE(severity,'medium') AS severity, active, created_at,
    COALESCE(steps,'[]') AS steps,
    COALESCE(initial_state,'') AS initial_state,
    COALESCE(expected_outcome,'') AS expected_outcome,
    COALESCE(is_outdated,FALSE) AS is_outdated,
    last_run_at,
    last_verdict, last_confidence, last_why, last_fix, last_pr_number, last_review_id,
    trigger_count
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
UPDATE scenarios
SET last_run_at     = NOW(),
    is_outdated     = FALSE,
    last_verdict    = $2,
    last_confidence = $3,
    last_why        = $4,
    last_fix        = $5,
    last_pr_number  = $6,
    last_review_id  = $7
WHERE id = $1;

-- name: IncrementScenarioTriggerCount :exec
UPDATE scenarios SET trigger_count = trigger_count + 1 WHERE id = $1;

-- name: CreateScenarioRun :one
INSERT INTO scenario_runs (
    scenario_id, review_id, pr_number, verdict, confidence, why, fix, root_cause, impact
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (scenario_id, review_id) DO UPDATE SET
    verdict    = EXCLUDED.verdict,
    confidence = EXCLUDED.confidence,
    why        = EXCLUDED.why,
    fix        = EXCLUDED.fix,
    root_cause = EXCLUDED.root_cause,
    impact     = EXCLUDED.impact,
    created_at = NOW()
RETURNING id, scenario_id, review_id, pr_number, verdict, confidence, why, fix, root_cause, impact, created_at;

-- name: GetScenarioRuns :many
SELECT id, scenario_id, review_id, pr_number, verdict, confidence, why, fix, root_cause, impact, created_at
FROM scenario_runs
WHERE scenario_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetScenarioKPIs :one
-- All four counts are scoped to active scenarios — an inactive-but-broken row would inflate
-- "Broken this week" without being visible in the list, so we exclude them everywhere.
SELECT
    COUNT(*) FILTER (WHERE active)                                                                           AS active,
    COUNT(*) FILTER (WHERE active AND last_verdict = 'broken' AND last_run_at > NOW() - INTERVAL '7 days') AS broken_this_week,
    COUNT(*) FILTER (WHERE active AND last_verdict = 'fixed'  AND last_run_at > NOW() - INTERVAL '7 days') AS fixed_this_week,
    COUNT(*) FILTER (WHERE active AND is_outdated)                                                         AS outdated
FROM scenarios
WHERE repo_id = $1;
