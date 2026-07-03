-- name: UpsertMembership :one
-- Idempotent on (org_id, user_id).
INSERT INTO memberships (org_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO UPDATE
    SET role = EXCLUDED.role
RETURNING *;

-- name: ListMembershipsByUser :many
-- lint:cross-org — intentionally spans orgs: a user's memberships across all
-- their organizations (used to resolve which orgs a principal belongs to).
SELECT * FROM memberships
WHERE user_id = $1
ORDER BY created_at;

-- name: ListMembershipsByOrg :many
SELECT * FROM memberships
WHERE org_id = $1
ORDER BY created_at;

-- name: GetMembership :one
SELECT * FROM memberships
WHERE org_id = $1 AND user_id = $2;

-- name: ChangeMemberRole :one
UPDATE memberships
SET role = $3
WHERE org_id = $1 AND user_id = $2
RETURNING *;

-- name: RemoveMember :execrows
DELETE FROM memberships
WHERE org_id = $1 AND user_id = $2;

-- name: CountOwners :one
SELECT count(*) FROM memberships
WHERE org_id = $1 AND role = 'owner';
