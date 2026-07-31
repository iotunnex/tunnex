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
INSERT INTO nodes (org_id, name, cert_serial, agent_version, cert_not_after, cert_public_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetNodeByCertSerial :one
-- lint:cross-org — the mTLS client cert IS the identity; the org comes from the
-- node row. Used to authorize every agent request.
SELECT * FROM nodes
WHERE cert_serial = $1;

-- name: GetNodeForOrg :one
-- An ORG-SCOPED node by id, whatever its status. Revoked rows included deliberately: the operator restore
-- (S13.1 Slice 7) names a REVOKED node as its source — that is the whole point — and a lookup that filtered them
-- out would make the one legitimate case unreachable. The caller decides what each side must be.
SELECT * FROM nodes WHERE id = $1 AND org_id = $2;

-- name: GetNodeForOrgForUpdate :one
-- The node row, ORG-SCOPED and LOCKED (review pass 1 #7). The restore reads it inside its own transaction and
-- refuses if the node is not active — because revoke takes the same row lock, so a revoke that lands mid-restore
-- either commits first (we see it revoked and refuse) or waits for us. Without the lock the restore authorized on
-- state read BEFORE the identity commit and applied AFTER it, and a re-key racing an operator revoke re-activated
-- the very devices that revoke had just cascaded.
SELECT * FROM nodes WHERE id = $1 AND org_id = $2 FOR UPDATE;

-- name: GetNodeByOrgName :one
-- ACTIVE rows only (S11 WF-S11-8). Since 0056 a name may be held by several REVOKED rows plus at most one
-- active one, so an unfiltered name lookup is ambiguous — and a :one query answering "multiple rows" is a
-- confusing runtime failure rather than a compile-time one. Filtering here makes the query correct by
-- construction instead of correct by the caller remembering.
SELECT * FROM nodes
WHERE org_id = $1 AND name = $2 AND revoked_at IS NULL;

-- name: ListNodes :many
SELECT * FROM nodes
WHERE org_id = $1
ORDER BY created_at;

-- name: RenewNodeCert :exec
-- lint:cross-org — keyed by node id after the caller authorized via the current
-- cert; renewal rotates the serial and stamps activity/version.
UPDATE nodes
SET cert_serial = $2, agent_version = $3, cert_not_after = $4, cert_public_key = $5, last_seen_at = now()
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
-- KEEPS THE SITE BINDING. The unbind added for WF-S11-14 was RULED REVERSED ON EVIDENCE after review, because the
-- status filter on ListSiteNodesForOrg was sufficient for the compiler input on its own and the unbind bought
-- nothing while costing three things:
--
--   1. BindNodeToSite has no status guard and authorizes purely on site_id being NULL, so an unbound-by-revocation
--      gateway could be bound to any site via API/CLI/GitOps — previously refused as already-bound. The site then
--      held a dead gateway that the status-filtered compiler input excludes, so cross-site traffic was silently
--      denied while the UI showed a gateway present: the exact policy-reads-correct-traffic-denied class the
--      filter was added to close.
--   2. assembleTopology joins a site's gateways with `n.site_id === s.id`, so a revoked gateway vanished from the
--      Sites card entirely — indistinguishable from a site that never had one, and it made the badge-suppression
--      fix landed in the same commit unreachable.
--   3. Nothing recorded which site the gateway served. The node.revoked audit row's metadata was an empty map, so
--      after the unbind neither UI, API nor audit log could answer it — while the docs told the operator to
--      re-apply that very binding.
--
-- THE PRINCIPLE THIS ESTABLISHES CONCRETELY: revocation preserves what it invalidates. Marking a row revoked is
-- enough; destroying the facts that explain it is not part of the job. Readers that must ignore a revoked gateway
-- filter on status — which is one predicate in one place, versus three consequences spread across three surfaces.
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

-- name: CreateRekeyChallenge :exec
-- lint:cross-org — a challenge is not org-scoped: it is issued BEFORE the caller is known to be anyone, and the
-- identifier it names is only resolved to a node at submit time. That is the anti-enumeration property (D9), not an
-- oversight.
-- The KIND is stored alongside the value (D10) so a nonce issued for one identifier kind cannot be spent under the
-- other. Two kinds sharing a string is not realistic today; a format change is how "not realistic" stops holding.
-- cert_serial is written TRANSITIONALLY alongside identifier, and only for serial-kind challenges (NULL for a
-- fingerprint, which is not a serial). It is the rolling-upgrade shim from migration 0061: a previous-version
-- replica reads cert_serial, and without this it could not consume a challenge a new replica issued — degrading
-- re-key during exactly the window an operator is most likely to be recovering a gateway. NOTHING in this version
-- reads it, so the copy cannot diverge in a way that matters, and the CONTRACT migration that drops it is
-- registered in docs/S13.1-decisions.md.
INSERT INTO node_rekey_challenges (nonce, identifier, identifier_kind, expires_at, cert_serial)
VALUES ($1, $2, $3, $4, CASE WHEN $3 = 'cert_serial' THEN $2 END);

-- name: ConsumeRekeyChallenge :one
-- lint:cross-org — a challenge carries no org and cannot: it is issued before the caller is known to be anyone,
-- and binding it to an org would require resolving the serial at issue time, which is the enumeration oracle D9
-- exists to avoid. The org is established later, from the node the serial resolves to.
-- SINGLE-USE, enforced by the UPDATE's own WHERE clause rather than by a read-then-write: two concurrent submits
-- with the same nonce cannot both win, because only one row can transition consumed_at from NULL. A read-check-write
-- would have a race exactly wide enough to matter here.
--
-- Returns the row only when it was unconsumed AND unexpired AND bound to this exact identifier AND KIND. No rows =
-- refuse, and the caller must not distinguish which of those it was.
-- coalesce(identifier, cert_serial) is the OTHER HALF of migration 0061's rolling-upgrade shim, and it is needed for
-- the same reason the write half is: during a roll the agent's two round trips (challenge, then submit) can land on
-- DIFFERENT replicas. The write half lets a previous-version replica consume a challenge this version issued; this
-- lets THIS version consume a challenge the previous one issued, whose `identifier` is NULL because that version did
-- not know the column. Half a shim would leave a straddled attempt failing — bounded, since the agent retries, but
-- degrading re-key during exactly the window an operator is most likely to be recovering a gateway.
-- It collapses to `identifier = $2` when the CONTRACT migration drops cert_serial.
UPDATE node_rekey_challenges
SET consumed_at = now()
WHERE nonce = $1 AND coalesce(identifier, cert_serial) = $2 AND identifier_kind = $3
  AND consumed_at IS NULL AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredRekeyChallenges :execrows
-- lint:cross-org — a retention sweep over a table with no org column, by design (see ConsumeRekeyChallenge).
-- Pruning for a table an unauthenticated endpoint writes to. Consumed rows are kept briefly too — a consumed nonce
-- must keep failing rather than becoming unknown, so deleting it the instant it is spent would turn replay into
-- "no such challenge" and lose the distinction in the log.
DELETE FROM node_rekey_challenges
WHERE expires_at < now() - interval '1 hour';

-- name: GetNodesByCertKeyFingerprint :many
-- The SECOND re-key identifier (S13.1 D10). :many, and the plurality is the POINT.
--
-- cert_key_fingerprint is deliberately NOT unique (see migration 0061: a UNIQUE index would turn a lookup ambiguity
-- into a migration failure on any fleet that already enrolled two nodes with the same key). So this query returns up
-- to TWO rows and its caller REFUSES when it gets more than one. A `:one` query would have raised a runtime
-- "multiple rows" error — a refusal by accident, at a moment when identity is being trusted, distinguishable in
-- timing and in the log from a clean refusal. Ambiguity here fails CLOSED, deliberately and visibly.
--
-- EXACT MATCH ONLY — `=` on the full 64-hex digest. Never a prefix, never a LIKE: a prefix match would let a caller
-- narrow the fleet's key space one request at a time, which is precisely the enumeration property D9 chose the
-- serial over the node name to avoid.
--
-- REVOKED ROWS ARE NOT FILTERED, matching GetNodeByCertSerial. A revoked node must be refused by the GONE-GATE, at
-- the same stage and with the same logged reason as it is on the serial path — filtering it out here would make the
-- two identifiers produce different diagnostics for the same condition, and an operator reading "no node holds this
-- key" for a node they revoked yesterday is being misled by their own tooling.
-- lint:cross-org — same reasoning as GetNodeByCertSerial and RekeyNode: the caller is an unauthenticated agent with
-- no session and no org context, and the recorded key material IS the identity claim being tested.
SELECT * FROM nodes
WHERE cert_key_fingerprint = $1
LIMIT 2;

-- name: RekeyNode :one
-- THE IDENTITY CHANGE, atomic (S13.1 D2). SAME node id — that is the whole point: the gateway that comes back IS
-- the gateway that left, keeping its site binding, its history and its metrics series.
--
-- Rotates the serial, the recorded public key and the expiry together. A row half-re-keyed — new certificate,
-- old recorded key — is a node whose proof-of-possession material no longer matches what it holds, so the columns
-- move in one statement.
--
-- Guarded on the CALLER having already authorized: status must still be what it was when RekeyAuthorized ran, so a
-- node revoked or renewed in the meantime cannot be re-keyed on a stale decision.
-- lint:cross-org — authorization here is the CERT SERIAL plus proof of possession, not an org membership: the
-- caller is an unauthenticated agent that holds no session and no org context, which is the whole premise of
-- recovery (its certificate is the thing that failed). The serial is globally unique (nodes_cert_serial_key), so it
-- identifies exactly one node and therefore exactly one org — the same reasoning that annotates
-- GetNodeByCertSerial, which is how every authenticated agent request already resolves its node.
-- IT CANNOT RESURRECT. This statement does not mention `status` or `revoked_at` at ALL — not "sets them
-- carefully", does not reference them. Re-key is therefore incapable of un-revoking a node rather than merely
-- forbidden from it, the same instinct as the gone-gate having no liveness parameter to pass. And it is guarded on
-- `status = 'active'` so a revoked row cannot be re-keyed even if a future caller reached here without the gate.
-- TestRekeyQueryCannotResurrect enforces both halves against this text.
UPDATE nodes
SET cert_serial = $2, cert_public_key = $3, cert_not_after = $4, agent_version = $5, last_seen_at = now()
WHERE id = $1 AND cert_serial = $6 AND status = 'active'
RETURNING *;
