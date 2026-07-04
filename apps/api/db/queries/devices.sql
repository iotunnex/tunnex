-- name: CreateDevice :one
INSERT INTO devices (org_id, user_id, node_id, name, platform, public_key, assigned_ip)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetDevice :one
SELECT * FROM devices
WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL;

-- name: ListDevicesByUser :many
SELECT * FROM devices
WHERE org_id = $1 AND user_id = $2 AND deleted_at IS NULL
ORDER BY created_at;

-- name: ListDevicesByOrg :many
SELECT * FROM devices
WHERE org_id = $1 AND deleted_at IS NULL
ORDER BY created_at;

-- name: CountActiveDevicesForUser :one
SELECT count(*) FROM devices
WHERE org_id = $1 AND user_id = $2 AND status = 'active' AND deleted_at IS NULL;

-- name: RevokeDevice :one
-- Returns the gateway node_id (for the push) so the caller needs no extra read;
-- pgx.ErrNoRows means the device was not active (already revoked / wrong org).
UPDATE devices
SET status = 'revoked', revoked_at = now()
WHERE id = $1 AND org_id = $2 AND status = 'active' AND deleted_at IS NULL
RETURNING node_id;

-- name: RevokeDevicesForNode :execrows
-- lint:cross-org — keyed by node_id; when a node is revoked its peers can no
-- longer reach a gateway, so they are revoked too (no dangling active devices).
UPDATE devices
SET status = 'revoked', revoked_at = now()
WHERE node_id = $1 AND status = 'active' AND deleted_at IS NULL;

-- name: LockUserDeviceCreation :exec
-- lint:cross-org — a transaction-scoped advisory lock keyed on the user, so the
-- per-user device-cap check-and-insert is atomic against concurrent creates.
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: ListActivePeersForNode :many
-- lint:cross-org — keyed by node_id after mTLS cert authorization (the agent
-- fetches the peers for its own node). A peer is present only while BOTH the
-- device is active AND its owning user is active — so deactivating a user drops
-- their peers from every node's desired state (and reactivation restores them).
SELECT d.public_key, d.assigned_ip
FROM devices d
JOIN users u ON u.id = d.user_id
WHERE d.node_id = $1
  AND d.status = 'active' AND d.deleted_at IS NULL
  AND u.status = 'active' AND u.deleted_at IS NULL
ORDER BY d.created_at;

-- name: ListNodeIDsForUserActiveDevices :many
-- lint:cross-org — keyed by user_id; used to find which nodes to push after a
-- user's peers change (create/revoke/deactivate). Not org-scoped: a user's
-- devices may span orgs and all affected nodes must be nudged to reconcile.
SELECT DISTINCT node_id FROM devices
WHERE user_id = $1 AND status = 'active' AND deleted_at IS NULL;

-- name: GetOrgNode :one
-- Verifies a node belongs to the org (id+org scoped) before a device attaches to it.
SELECT * FROM nodes
WHERE id = $1 AND org_id = $2 AND status = 'active';
