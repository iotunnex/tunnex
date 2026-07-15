-- S7.5.2 IdP-group sync. Enterprise. All tenant-scoped by org_id.
-- The reconciler reads the desired membership from a DirectoryProvider (Graph/etc.) and drives
-- these to converge group_members(origin='idp_sync') — NEVER touching manual rows (disjoint by D1).

-- ── sync configs (per org, per provider) ─────────────────────────────────────────
-- name: GetIdpSyncConfig :one
SELECT * FROM idp_sync_configs
WHERE org_id = $1 AND provider = $2;

-- name: ListEnabledIdpSyncConfigs :many
-- The poller's work-list: every org/provider with sync turned on.
SELECT * FROM idp_sync_configs
WHERE enabled = true
ORDER BY org_id, provider;

-- name: RecordIdpSyncResult :exec
-- One stamp for all three poll outcomes (the two-tier health, D2):
--   success  → ok=true,  advance_clock=true  (last_sync_at = now; error cleared)
--   transient→ ok=false, advance_clock=false (last_sync_at FROZEN at the last good sync — the
--              staleness the escalation tier measures; a Graph outage must NOT reset that clock)
--   gone     → ok=false, advance_clock=true  (fetch itself succeeded, so data is fresh: immediate
--              tier only, never escalates — a stable known-bad mapping, not a worsening outage)
-- advance_clock also drives whether last_sync_error is cleared vs set.
UPDATE idp_sync_configs
SET last_sync_ok    = $3,
    last_sync_error = $4,
    last_sync_at    = CASE WHEN $5::boolean THEN $6 ELSE last_sync_at END,
    updated_at      = $6
WHERE org_id = $1 AND provider = $2;

-- ── idp_sync groups (the mappings to reconcile) ──────────────────────────────────
-- name: ListIdpSyncGroups :many
-- Every Tunnex group bound to an IdP group for this org+provider — the units the reconciler
-- walks. origin/shape is schema-guaranteed (0026), so idp_provider/idp_group_id are non-null.
SELECT id, idp_group_id
FROM user_groups
WHERE org_id = $1 AND origin = 'idp_sync' AND idp_provider = $2
ORDER BY id;

-- ── idp-origin membership (the reconcile target) ─────────────────────────────────
-- name: ListIdpGroupMemberIDs :many
-- Current idp-origin members of one group (user ids). Filtered to origin='idp_sync' so a
-- hand-added row could never appear here and get computed into a removal (belt over disjoint).
SELECT user_id
FROM group_members
WHERE org_id = $1 AND group_id = $2 AND origin = 'idp_sync'
ORDER BY user_id;

-- name: AddIdpGroupMember :execrows
-- Idempotent add of a synced member. Explicit origin='idp_sync'. 0 rows on conflict = already
-- present (no state change).
INSERT INTO group_members (org_id, group_id, user_id, origin)
VALUES ($1, $2, $3, 'idp_sync')
ON CONFLICT (group_id, user_id) DO NOTHING;

-- name: RemoveIdpGroupMember :execrows
-- Remove a synced member — scoped to origin='idp_sync' so the reconcile can NEVER delete a
-- manual membership even if one somehow shared the (group,user) key.
DELETE FROM group_members
WHERE org_id = $1 AND group_id = $2 AND user_id = $3 AND origin = 'idp_sync';

-- ── directory email → Tunnex org user ────────────────────────────────────────────
-- name: GetOrgUserByEmail :one
-- Resolve a directory member's email to a Tunnex user that BELONGS to this org (membership
-- join). A directory member with no matching org user is skipped by the reconciler (sync grants
-- access to existing users; it does not JIT-provision — that's S2.5). Case-insensitive: the
-- provider already lower-cases; users.email is stored lower-cased.
SELECT u.id, u.status
FROM users u
JOIN memberships m ON m.user_id = u.id AND m.org_id = $1
WHERE u.email = $2 AND u.deleted_at IS NULL;
