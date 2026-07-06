-- name: CreateDevice :one
INSERT INTO devices (org_id, user_id, node_id, name, platform, public_key, assigned_ip)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetDevice :one
SELECT * FROM devices
WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL;

-- name: ListDevicesByUser :many
SELECT sqlc.embed(d), ds.last_handshake_at, ds.rx_bytes, ds.tx_bytes
FROM devices d
LEFT JOIN device_status ds ON ds.device_id = d.id
WHERE d.org_id = $1 AND d.user_id = $2 AND d.deleted_at IS NULL
ORDER BY d.created_at;

-- name: ListDevicesByOrg :many
SELECT sqlc.embed(d), ds.last_handshake_at, ds.rx_bytes, ds.tx_bytes
FROM devices d
LEFT JOIN device_status ds ON ds.device_id = d.id
WHERE d.org_id = $1 AND d.deleted_at IS NULL
ORDER BY d.created_at;

-- name: CountActiveDevicesForUser :one
SELECT count(*) FROM devices
WHERE org_id = $1 AND user_id = $2 AND status = 'active' AND deleted_at IS NULL;

-- name: RevokeDevice :one
-- Returns the gateway node_id (for the push) so the caller needs no extra read;
-- pgx.ErrNoRows means the device was not active (already revoked / wrong org).
-- Clears assigned_ip to release the address explicitly (rather than relying on
-- every reader to also filter status='active').
UPDATE devices
SET status = 'revoked', revoked_at = now(), assigned_ip = NULL
WHERE id = $1 AND org_id = $2 AND status = 'active' AND deleted_at IS NULL
RETURNING node_id;

-- name: RevokeDevicesForNode :execrows
-- lint:cross-org — keyed by node_id; when a node is revoked its peers can no
-- longer reach a gateway, so they are revoked too (no dangling active devices).
UPDATE devices
SET status = 'revoked', revoked_at = now()
WHERE node_id = $1 AND status = 'active' AND deleted_at IS NULL;

-- name: DeleteDeviceStatus :exec
-- lint:cross-org — keyed by device_id (the caller already authorized the device
-- via its org). Clears a device's live status (on revoke) so a revoked device
-- never reports stale online/handshake via the API.
DELETE FROM device_status WHERE device_id = $1;

-- name: LockDeviceKey :exec
-- lint:cross-org — a transaction-scoped advisory lock on an arbitrary key (a
-- user id or org id, passed as text). Create takes BOTH (in sorted order, so no
-- deadlock) to make the per-user cap check AND the org-wide IP allocation atomic
-- against concurrent creates.
--
-- TWO CLIENTS, both load-bearing: (1) device allocation (per-org mutual
-- exclusion); (2) CIDR resize (S4.5b) — ResizePool takes the org key so its
-- orphan check can't race a concurrent allocation during the resize window. A
-- future S3.5 refactor that rescopes/weakens this lock (per-device keys, etc.)
-- MUST keep resize and allocation contending on the SAME per-org key, or it
-- silently reopens that race — see TestResizeAllocationRace (the red-without-lock
-- guard). Resize takes only the org key; allocation takes {owner,org} sorted;
-- resize never waits on the owner key, so no inversion/deadlock.
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: ListAssignedIPsForOrg :many
-- The org's live tunnel allocations (flat pool, across all nodes). Read under the
-- org advisory lock during Create so the lowest-free choice can't be raced.
SELECT assigned_ip FROM devices
WHERE org_id = $1 AND assigned_ip IS NOT NULL AND status = 'active' AND deleted_at IS NULL;

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

-- name: UpsertDeviceStatus :batchexec
-- lint:cross-org — keyed by node_id (agent is cert-authorized) + pubkey. Batched
-- (pgx.Batch) so a whole report is a single round-trip; no per-peer write
-- amplification and the write lands on the lean status table, not the devices
-- row. Maps pubkey->active device on this node; an unknown pubkey is a no-op.
-- rx/tx are raw gauges.
INSERT INTO device_status (device_id, last_handshake_at, rx_bytes, tx_bytes, updated_at)
SELECT d.id, @last_handshake_at, @rx_bytes, @tx_bytes, now()
FROM devices d
WHERE d.node_id = @node_id AND d.public_key = @public_key
  AND d.status = 'active' AND d.deleted_at IS NULL
ON CONFLICT (device_id) DO UPDATE
SET last_handshake_at = EXCLUDED.last_handshake_at,
    rx_bytes = EXCLUDED.rx_bytes,
    tx_bytes = EXCLUDED.tx_bytes,
    updated_at = now();
