-- name: CreateOrganization :one
INSERT INTO organizations (name, slug)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations
WHERE slug = $1 AND deleted_at IS NULL;

-- name: ListOrganizations :many
-- Admin/system listing of all orgs; user-facing listing uses
-- ListOrganizationsForUser (membership-scoped).
SELECT * FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at;

-- name: ListOrganizationsForUser :many
SELECT o.* FROM organizations o
JOIN memberships m ON m.org_id = o.id
WHERE m.user_id = $1 AND o.deleted_at IS NULL
ORDER BY o.created_at;

-- name: CountOrganizations :one
SELECT count(*) FROM organizations
WHERE deleted_at IS NULL;

-- name: UpdateOrganizationName :one
-- Slug is immutable after creation (S1.2); only name is updatable here.
UPDATE organizations
SET name = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOrganization :execrows
UPDATE organizations
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertOrganization :one
-- Used by the seed with a fixed id; idempotent. Also clears deleted_at so
-- re-seeding restores a previously soft-deleted demo org to a clean live state.
INSERT INTO organizations (id, name, slug)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
    SET name = EXCLUDED.name, slug = EXCLUDED.slug, deleted_at = NULL
RETURNING *;

-- name: UpdateOrgPoolCidr :one
-- Resize the org tunnel pool. The service refuses a shrink that would orphan
-- live allocations (checked in Go before calling this); this just persists it.
UPDATE organizations
SET pool_cidr = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CountMembersByOrg :one
-- Org roster size. Joins users to exclude soft-deleted accounts (whose
-- membership row survives a soft-delete); deactivated members are still on the
-- roster, so they are intentionally counted.
SELECT count(*) FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND u.deleted_at IS NULL;

-- name: CountActiveDevicesByOrg :one
SELECT count(*) FROM devices WHERE org_id = $1 AND status = 'active' AND deleted_at IS NULL;

-- name: CountActiveNodesByOrg :one
SELECT count(*) FROM nodes WHERE org_id = $1 AND status = 'active';

-- name: CountOnlineDevicesByOrg :one
-- "Seen recently": last handshake within the window ($2 = now - OnlineWindow),
-- an S3.6-style online approximation. The boundary is inclusive (>=) to match
-- deviceOnline's `time.Since(h) <= threshold`. Requires an ACTIVE owner too: a
-- deactivated user's peers are offboarded from the data plane (they fall out of
-- the node's desired state) even though the device row stays 'active', so
-- counting them as "online" would be dishonest.
SELECT count(*) FROM devices d
JOIN device_status ds ON ds.device_id = d.id
JOIN users u ON u.id = d.user_id
WHERE d.org_id = $1 AND d.status = 'active' AND d.deleted_at IS NULL
  AND u.status = 'active' AND u.deleted_at IS NULL
  AND ds.last_handshake_at >= $2;
