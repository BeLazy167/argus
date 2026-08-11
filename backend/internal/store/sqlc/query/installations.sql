-- name: CreateInstallation :one
INSERT INTO installations (installation_id, org_login)
VALUES ($1, $2)
ON CONFLICT (installation_id) DO UPDATE SET org_login = $2
RETURNING id, installation_id, org_login, clerk_org_id, created_at, suspended_at;

-- name: GetInstallation :one
SELECT id, installation_id, org_login, clerk_org_id, created_at, suspended_at
FROM installations WHERE id = $1;

-- name: GetInstallationByGitHubID :one
SELECT id, installation_id, org_login, clerk_org_id, created_at, suspended_at
FROM installations WHERE installation_id = $1;

-- name: GetInstallationByClerkOrgID :one
SELECT id, installation_id, org_login, clerk_org_id, created_at, suspended_at
FROM installations WHERE clerk_org_id = $1;

-- name: SetInstallationClerkOrgID :exec
UPDATE installations SET clerk_org_id = $1 WHERE id = $2;

-- name: SuspendInstallation :exec
UPDATE installations SET suspended_at = NOW() WHERE installation_id = $1;



-- name: GetOrgDefaults :one
SELECT COALESCE(default_settings, '{}')::jsonb FROM installations WHERE id = $1;

-- name: SetOrgDefaults :exec
UPDATE installations SET default_settings = $1 WHERE id = $2;

-- name: GetInstallationFeatureFlags :one
SELECT COALESCE(feature_flags, '{}')::jsonb FROM installations WHERE id = $1;

-- name: MergeInstallationFeatureFlags :exec
-- Merge the given keys into feature_flags, leaving every other key intact.
-- jsonb || jsonb is a right-biased top-level merge applied inside the UPDATE,
-- so there is no read-modify-write window: an operator setting another key
-- by hand cannot be silently reverted by a concurrent settings save. The
-- column is NOT NULL (migration 030) with a jsonb_typeof = 'object' CHECK
-- (032), so both operands are always objects.
UPDATE installations SET feature_flags = feature_flags || @patch::jsonb WHERE id = @id;

-- name: ListInstallations :many
SELECT id, installation_id, org_login, clerk_org_id, created_at, suspended_at
FROM installations
ORDER BY created_at DESC;
