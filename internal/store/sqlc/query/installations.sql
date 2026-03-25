-- name: CreateInstallation :one
INSERT INTO installations (installation_id, org_login)
VALUES ($1, $2)
ON CONFLICT (installation_id) DO UPDATE SET org_login = $2
RETURNING id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at;

-- name: GetInstallation :one
SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
FROM installations WHERE id = $1;

-- name: GetInstallationByGitHubID :one
SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
FROM installations WHERE installation_id = $1;

-- name: GetInstallationByClerkOrgID :one
SELECT id, installation_id, org_login, clerk_org_id, plan_tier, created_at, suspended_at
FROM installations WHERE clerk_org_id = $1;

-- name: SetInstallationClerkOrgID :exec
UPDATE installations SET clerk_org_id = $1 WHERE id = $2;

-- name: SuspendInstallation :exec
UPDATE installations SET suspended_at = NOW() WHERE installation_id = $1;

-- name: GetPlanTier :one
SELECT plan_tier FROM installations WHERE id = $1;

-- name: SetPlanTier :exec
UPDATE installations SET plan_tier = $1 WHERE id = $2;

-- name: GetOrgDefaults :one
SELECT COALESCE(default_settings, '{}')::jsonb FROM installations WHERE id = $1;

-- name: SetOrgDefaults :exec
UPDATE installations SET default_settings = $1 WHERE id = $2;
