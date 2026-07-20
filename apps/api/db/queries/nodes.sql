-- name: GetPlatformSecret :one
SELECT * FROM platform_secrets WHERE name = $1;

-- name: InsertPlatformSecret :exec
-- Create-if-absent; the caller reads-back on conflict. Never overwrites, so a
-- concurrent boot can't clobber the CA (fail-loud-never-regenerate lives above).
INSERT INTO platform_secrets (name, secret_sealed, public_pem)
VALUES ($1, $2, $3)
ON CONFLICT (name) DO NOTHING;

-- name: CreateJoinToken :one
INSERT INTO node_join_tokens (org_id, node_name, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ConsumeJoinToken :one
-- lint:cross-org — the token itself is the credential; the org comes from the
-- returned row. Single-use + expiring.
UPDATE node_join_tokens
SET consumed_at = now()
WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
RETURNING *;

-- name: CreateNode :one
INSERT INTO nodes (org_id, name, cert_serial, agent_version)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetNodeByCertSerial :one
-- lint:cross-org — the mTLS client cert IS the identity; the org comes from the
-- node row. Used to authorize every agent request.
SELECT * FROM nodes
WHERE cert_serial = $1;

-- name: GetNodeByOrgName :one
SELECT * FROM nodes
WHERE org_id = $1 AND name = $2;

-- name: ListNodes :many
SELECT * FROM nodes
WHERE org_id = $1
ORDER BY created_at;

-- name: RenewNodeCert :exec
-- lint:cross-org — keyed by node id after the caller authorized via the current
-- cert; renewal rotates the serial and stamps activity/version.
UPDATE nodes
SET cert_serial = $2, agent_version = $3, last_seen_at = now()
WHERE id = $1 AND status = 'active';

-- name: TouchNodeSeen :exec
-- lint:cross-org — keyed by id after cert authorization.
UPDATE nodes
SET last_seen_at = now()
WHERE id = $1;

-- name: SetNodeWGInfo :execrows
-- lint:cross-org — keyed by id after cert authorization; the node reports its
-- locally-generated WireGuard public key and its public endpoint (host:port that
-- peer configs dial). Returns rows affected so the caller can distinguish a real
-- write from a no-op (e.g. node revoked mid-report).
-- endpoint uses COALESCE(NULLIF(...)) so an agent that reports an empty endpoint
-- (env unset on a restart) never clobbers a previously-good value.
UPDATE nodes
SET wg_public_key = @wg_public_key,
    endpoint = COALESCE(NULLIF(@endpoint::text, ''), nodes.endpoint),
    capabilities = @capabilities::jsonb,
    last_seen_at = now(),
    -- S7.4b fold [1]: the applied-policy REPORT time (this write IS the report), distinct from
    -- last_seen_at (also bumped by DesiredState polls) — the desync freshness gate reads this.
    policy_reported_at = now()
WHERE id = @id AND status = 'active';

-- name: RevokeNode :exec
UPDATE nodes
SET status = 'revoked', revoked_at = now()
WHERE org_id = $1 AND id = $2;

-- name: ListActiveNodeIDsForOrg :many
-- S7.2 push targeting: every active gateway in the org. A policy change is org-wide,
-- and member-removal can orphan a device whose node would drop out of a device-join
-- query — so the push set is ALL active nodes (an unaffected node's re-fetch recompiles
-- to identical bytes = reconcile no-op, so over-notifying is safe + correct).
SELECT id FROM nodes
WHERE org_id = $1 AND status = 'active';

-- name: StampNodePolicyDesyncSince :exec
-- S7.4b (X-4): stamp the term-3 desync ONSET, CONTROL-PLANE-ONLY, idempotent per episode —
-- the WHERE ... IS NULL preserves the first onset (a repeated mismatch never re-stamps a
-- newer time). Called from exactly one site (nodes.trackDesync); the value is the CP clock,
-- never an agent string. org_id-scoped (tenant isolation).
UPDATE nodes SET policy_desync_since = $3 WHERE id = $1 AND org_id = $2 AND policy_desync_since IS NULL;

-- name: ClearNodePolicyDesyncSince :exec
-- S7.4b (X-4): clear the desync stamp on RECONVERGENCE or non-enforcing (applied == pushed,
-- or pushed == "" ). Convergence is a STATE predicate — revert-to-clear (admin reverts the
-- pushed target back to the applied hash) legitimately clears. CP-only, single-writer, org-scoped.
UPDATE nodes SET policy_desync_since = NULL WHERE id = $1 AND org_id = $2 AND policy_desync_since IS NOT NULL;

-- name: UpsertNodePeerStatus :batchexec
-- lint:cross-org — keyed by node_id (the agent is cert-authorized for its own node) + the PEER's pubkey.
-- The SIBLING of UpsertDeviceStatus (S8.6): it stores a reporting GATEWAY's GATEWAY-peer telemetry
-- (site-link peers). The EXISTS guard admits ONLY a pubkey that is ANOTHER node (a real gateway) in the
-- SAME org — a DEVICE pubkey matches no node, so it no-ops here (device peers land in device_status,
-- gateway peers land here; neither crosses). Batched (one round-trip per report). rx/tx are raw gauges.
INSERT INTO node_peer_status (node_id, public_key, last_handshake_at, rx_bytes, tx_bytes, updated_at)
SELECT @node_id, @public_key, @last_handshake_at, @rx_bytes, @tx_bytes, now()
WHERE EXISTS (
    SELECT 1 FROM nodes peer
    WHERE peer.wg_public_key = @public_key
      AND peer.org_id = (SELECT org_id FROM nodes WHERE id = @node_id)
      AND peer.id <> @node_id
)
ON CONFLICT (node_id, public_key) DO UPDATE
SET last_handshake_at = EXCLUDED.last_handshake_at,
    rx_bytes = EXCLUDED.rx_bytes,
    tx_bytes = EXCLUDED.tx_bytes,
    updated_at = now();

-- name: ListNodePeerStatusForOrg :many
-- lint:cross-org — org-scoped via the reporting node's org. Every gateway's node-peer telemetry for the
-- org: the input to D3's per-hub freshness clock + the S8.5 L1 site-link card metrics (read path defined
-- with the storage, consumed by S8.6 Slice 4 + Slice 6).
SELECT nps.node_id, nps.public_key, nps.last_handshake_at, nps.rx_bytes, nps.tx_bytes, nps.updated_at
FROM node_peer_status nps
JOIN nodes n ON n.id = nps.node_id
WHERE n.org_id = @org_id;

-- name: GetOrgHubSet :one
-- lint:cross-org — org-scoped by PK. The persisted transit-hub election (S8.6 REDUCE): the two
-- writer-partitioned fields (configured + demoted) + the D5 generation. The ACTIVE order is DERIVED from
-- these by deriveActive (never stored). No rows until the first ReconcileHubSet.
SELECT org_id, configured, demoted, generation, updated_at FROM org_hub_set WHERE org_id = $1;

-- name: UpsertOrgHubSetConfigured :one
-- lint:cross-org — org-scoped by PK. ReconcileHubSet's writer (S8.6 REDUCE): writes `configured` ONLY —
-- the CONFIGURED membership (pins/capability/order). ATOMIC bump: the generation increments in the SAME
-- statement ONLY when `configured` actually changes (IS DISTINCT FROM) — an idempotent re-election never
-- bumps (no idle tick eroding the fence), concurrent reconciles converge. On INSERT `demoted` defaults to
-- '{}' (a fresh set has nothing demoted). This writer NEVER touches `demoted` (the field partition — the
-- controller owns it), so a bind landing during a live failover updates membership without clobbering the
-- demotion state.
INSERT INTO org_hub_set (org_id, configured, generation)
VALUES (@org_id, @configured, 1)
ON CONFLICT (org_id) DO UPDATE
SET configured = EXCLUDED.configured,
    generation = CASE WHEN org_hub_set.configured IS DISTINCT FROM EXCLUDED.configured
                      THEN org_hub_set.generation + 1
                      ELSE org_hub_set.generation END,
    updated_at = now()
RETURNING org_id, configured, demoted, generation, updated_at;

-- name: UpsertOrgHubSetDemoted :one
-- lint:cross-org — org-scoped by PK. The failover controller's writer (S8.6 REDUCE): writes `demoted` ONLY
-- — the members currently promoted-past for staleness. UPDATE (not upsert): a demotion only makes sense for
-- an org that already has a configured hub set, so no row → 0 rows → the controller skips (nothing to fail
-- over). ATOMIC bump: generation increments ONLY when `demoted` actually changes. NEVER touches
-- `configured` (the field partition — ReconcileHubSet owns it).
UPDATE org_hub_set
SET demoted = @demoted,
    generation = CASE WHEN org_hub_set.demoted IS DISTINCT FROM @demoted::uuid[]
                      THEN org_hub_set.generation + 1
                      ELSE org_hub_set.generation END,
    updated_at = now()
WHERE org_id = @org_id
RETURNING org_id, configured, demoted, generation, updated_at;

-- name: SetNodeHubPriority :execrows
-- lint:cross-org — org-scoped. The admin pin (S8.6 D1): a nullable rank; NULL clears the pin. Org-checked
-- so a cross-org node id no-ops (0 rows -> typed 404 at the service).
UPDATE nodes SET hub_priority = @hub_priority WHERE id = @node_id AND org_id = @org_id;

-- name: ListFailoverOrgs :many
-- lint:cross-org — CP-internal (the failover tick iterates every org). Orgs whose persisted hub set has
-- MORE THAN ONE member — i.e. a pinned HA set with at least one standby; a single-hub org has nothing to
-- fail over (S8.6 Slice 4). Reads the CONFIGURED membership (the intent) — the reduce's field rename.
SELECT org_id FROM org_hub_set WHERE array_length(configured, 1) > 1;

-- name: GetNodeHubPriority :one
-- lint:cross-org — org-scoped. The node's current hub_priority (nullable) so SetHubPriority can audit the
-- old→new transition (S8.6 Slice 6 — the pin is a topology-consequential act).
SELECT hub_priority FROM nodes WHERE id = $1 AND org_id = $2;
