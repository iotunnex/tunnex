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

-- name: ListOrgMembersWithUser :many
-- The org roster for the Users page: membership joined to the user record so the
-- UI has name/email/status/verified in one query. Soft-deleted users are
-- excluded (their membership row survives a soft-delete); deactivated members
-- stay on the roster (status carries that).
SELECT m.user_id, m.role, m.created_at AS joined_at,
       u.email, u.name, u.status, (u.email_verified_at IS NOT NULL)::boolean AS email_verified
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND u.deleted_at IS NULL
ORDER BY m.created_at;

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

-- name: CountOrgsWhereSoleOwner :one
-- lint:cross-org — spans a user's orgs to protect the last-owner invariant on
-- global deactivation; each row's org_id is used in the correlated subquery.
SELECT count(*) FROM memberships m
WHERE m.user_id = $1 AND m.role = 'owner'
  AND (SELECT count(*) FROM memberships o WHERE o.org_id = m.org_id AND o.role = 'owner') = 1;
