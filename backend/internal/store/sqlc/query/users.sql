-- name: LinkUserInstallation :one
INSERT INTO user_installations (clerk_user_id, installation_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (clerk_user_id, installation_id) DO NOTHING
RETURNING id, clerk_user_id, installation_id, role, created_at;

-- name: GetUserInstallationByUserAndInstallation :one
SELECT id, clerk_user_id, installation_id, role, created_at
FROM user_installations WHERE clerk_user_id = $1 AND installation_id = $2;

-- name: GetUserInstallationIDs :many
SELECT installation_id FROM user_installations WHERE clerk_user_id = $1;

-- name: ListUserInstallations :many
SELECT i.id, i.installation_id, i.org_login, i.clerk_org_id, i.plan_tier, i.created_at, i.suspended_at
FROM installations i
JOIN user_installations ui ON ui.installation_id = i.id
WHERE ui.clerk_user_id = $1
ORDER BY i.created_at DESC;
