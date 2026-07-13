-- name: CreateDevice :one
-- status is 'active' normally, or 'pending' when the org requires device approval
-- (S7.3). A pending device holds its assigned_ip from creation (excluded from every
-- status='active' reader EXCEPT the allocator, which counts its IP as in-flight).
INSERT INTO devices (org_id, user_id, node_id, name, platform, public_key, assigned_ip, full_tunnel, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ApproveDevice :one
-- S7.3: pending -> active, recording the approver (approved_by). Only a PENDING device
-- can be approved (pgx.ErrNoRows => not pending: already active / rejected / wrong org).
-- Returns the owner so the caller can distinguish self-approval for the audit.
UPDATE devices
SET status = 'active', approved_by = $3, updated_at = now()
WHERE id = $1 AND org_id = $2 AND status = 'pending' AND deleted_at IS NULL
RETURNING user_id;

-- name: RejectDevice :one
-- S7.3: pending -> revoked, FREEING the held pool IP (assigned_ip=NULL) so it returns to
-- the pool for reuse (D1b — the same release RevokeDevice does). Only a PENDING device
-- can be rejected. Returns node_id for the (own-node) push.
UPDATE devices
SET status = 'revoked', revoked_at = now(), assigned_ip = NULL
WHERE id = $1 AND org_id = $2 AND status = 'pending' AND deleted_at IS NULL
RETURNING node_id;

-- name: ListPendingDevicesByOrg :many
-- The approval queue (S7.3): devices awaiting admin approval, oldest first.
SELECT sqlc.embed(d), ds.last_handshake_at, ds.rx_bytes, ds.tx_bytes
FROM devices d
LEFT JOIN device_status ds ON ds.device_id = d.id
WHERE d.org_id = $1 AND d.status = 'pending' AND d.deleted_at IS NULL
ORDER BY d.created_at;

-- name: CountActiveDevicesForOrg :one
-- Grandfathered count when flipping device_approval off->on (best-effort blast radius,
-- S7.3 D4 — existing active devices stay active, not retro-pended).
SELECT count(*) FROM devices
WHERE org_id = $1 AND status = 'active' AND deleted_at IS NULL;

-- name: SetOrgDeviceApproval :one
-- S7.3: flip the org device-approval gate. Enterprise-gated at the HTTP layer; the open
-- build can never set it 'on', so enrollment there stays immediately-active.
UPDATE organizations SET device_approval = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListActiveFullTunnelDevices :many
-- S7.2 decision 2a: the devices whose internet egress is governed by policy once the
-- org enters enforcing mode -- enumerated (count + names) in the mode-enable response
-- so the warn-and-confirm shows real blast radius. Owner must be a CURRENT org member
-- (the F1 convention: policy-input queries re-verify membership, not just status).
SELECT d.id, d.user_id, d.name, d.assigned_ip
FROM devices d
JOIN users u ON u.id = d.user_id
JOIN memberships mem ON mem.org_id = d.org_id AND mem.user_id = d.user_id
WHERE d.org_id = $1
  AND d.status = 'active' AND d.deleted_at IS NULL
  AND u.status = 'active' AND u.deleted_at IS NULL
  AND d.full_tunnel
ORDER BY d.name;

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

-- name: ListActiveDeviceAllocations :many
-- The org's live tunnel allocations (flat pool, across all nodes) WITH the owning
-- device (id, name). The SINGLE definition of "live allocation" — used by BOTH
-- device-create's lowest-free choice AND resize's orphan check/409 objects, so
-- there are no two filtered reads to drift apart. Read under the org advisory
-- lock so allocation and resize serialize on the same snapshot.
--
-- INCLUDES 'pending' (S7.3): a pending device HOLDS its assigned_ip from creation, so it
-- is IN-FLIGHT — create must not hand its IP to another device (silent duplicate; the
-- org_ip unique index is likewise widened to active+pending), and resize's orphan check
-- must see it (else a shrink silently strands a pending device's allocation). Revoked/
-- rejected devices have assigned_ip=NULL and never appear.
SELECT id, name, assigned_ip FROM devices
WHERE org_id = $1 AND assigned_ip IS NOT NULL AND status IN ('active', 'pending') AND deleted_at IS NULL
ORDER BY assigned_ip;

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
