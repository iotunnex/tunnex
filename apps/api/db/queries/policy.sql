-- Zero Trust policy model (S7.1). Enterprise feature; model-only (no data plane).
-- All tenant tables scope by org_id (tenant-lint). Policy objects are hard-deleted
-- (FK ON DELETE CASCADE), so there is no deleted_at filter here.

-- ── user_groups (the rule SUBJECT) ──────────────────────────────────────────────
-- name: CreateUserGroup :one
INSERT INTO user_groups (org_id, name, description)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserGroup :one
SELECT * FROM user_groups
WHERE id = $1 AND org_id = $2;

-- name: ListUserGroupsByOrg :many
SELECT * FROM user_groups
WHERE org_id = $1
ORDER BY name;

-- name: UpdateUserGroup :one
UPDATE user_groups
SET name = $3, description = $4
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeleteUserGroup :execrows
DELETE FROM user_groups
WHERE id = $1 AND org_id = $2;

-- ── group_members ───────────────────────────────────────────────────────────────
-- name: AddGroupMember :execrows
-- Returns rows-affected: 0 on ON CONFLICT (already a member) so the caller can skip
-- the audit event for a no-op re-add (idempotent, still 204).
INSERT INTO group_members (org_id, group_id, user_id)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, user_id) DO NOTHING;

-- name: RemoveGroupMember :execrows
DELETE FROM group_members
WHERE org_id = $1 AND group_id = $2 AND user_id = $3;

-- name: ListGroupMembers :many
SELECT u.id, u.email, u.name, gm.created_at
FROM group_members gm
JOIN users u ON u.id = gm.user_id
WHERE gm.org_id = $1 AND gm.group_id = $2 AND u.deleted_at IS NULL
ORDER BY u.email;

-- name: ListGroupMembershipsByOrg :many
-- Compiler input: every (group, user) pair in the org.
SELECT group_id, user_id
FROM group_members
WHERE org_id = $1;

-- ── resources (static destinations) ─────────────────────────────────────────────
-- name: CreateResource :one
INSERT INTO resources (org_id, name, cidr, protocol, port_low, port_high)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetResource :one
SELECT * FROM resources
WHERE id = $1 AND org_id = $2;

-- name: ListResourcesByOrg :many
SELECT * FROM resources
WHERE org_id = $1
ORDER BY name;

-- name: UpdateResource :one
UPDATE resources
SET name = $3, cidr = $4, protocol = $5, port_low = $6, port_high = $7
WHERE id = $1 AND org_id = $2
RETURNING *;

-- name: DeleteResource :execrows
DELETE FROM resources
WHERE id = $1 AND org_id = $2;

-- ── policy_rules (allow grants) ─────────────────────────────────────────────────
-- name: CreatePolicyRule :one
-- S7.5.4: src_kind ∈ {group,user} (exactly one of src_group_id/src_user_id, CHECK-enforced);
-- expires_at NULL = permanent, set = a temporary grant.
INSERT INTO policy_rules (org_id, src_kind, src_group_id, src_user_id, dst_kind, dst_resource_id, dst_group_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListPolicyRulesByOrg :many
-- Admin LIST — every rule incl. expired ones (the UI shows a lapsed grant distinctly).
SELECT * FROM policy_rules
WHERE org_id = $1
ORDER BY created_at;

-- name: ListActivePolicyRulesForOrg :many
-- COMPILER INPUT — excludes EXPIRED temporary grants (the expiry correctness backstop:
-- an expired rule stops compiling on the next recompile REGARDLESS of the sweeper). The
-- pure compiler stays clockless; this query applies now() at snapshot-build time.
SELECT * FROM policy_rules
WHERE org_id = $1 AND (expires_at IS NULL OR expires_at > now())
ORDER BY created_at;

-- name: DeletePolicyRule :execrows
DELETE FROM policy_rules
WHERE id = $1 AND org_id = $2;

-- name: ExtendPolicyRule :one
-- S7.5.4: move a temporary grant's window IN PLACE (never delete+recreate — that would
-- churn the /32 out+back and cause a spurious push). The `expires_at > now()` predicate
-- is the LAPSE GUARD: a grant that has already expired matches 0 rows, so extend and the
-- expiry sweeper compose deterministically on the row lock — a grant lapsing mid-extend
-- resolves to extended-OR-(0 rows -> 409 grant_lapsed), never torn. Only a TEMPORARY
-- (expires_at NOT NULL), still-LIVE grant can be extended.
UPDATE policy_rules
SET expires_at = sqlc.arg(new_expires_at)
WHERE id = $1 AND org_id = $2 AND expires_at IS NOT NULL AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredGrants :many
-- The expiry sweeper (S7.5.4 story-end AMENDMENT — delete-on-sweep, replaced linger). A
-- lapsed temporary grant is DELETED (not lingered), returning id+org for the same-tx
-- grant_expired audit + the org-wide push. STATELESS — no window/`last` cursor: every
-- currently-expired grant is deleted each tick, so a failed or interrupted (downtime) tick
-- simply leaves rows for the NEXT tick to delete+audit (closes the window-skip + downtime-
-- audit-gap by construction). Composes with ExtendPolicyRule on the row lock: a grant an
-- extend rescued (expires_at moved to the future) no longer matches expires_at <= now().
DELETE FROM policy_rules
WHERE expires_at IS NOT NULL AND expires_at <= now()
RETURNING id, org_id;

-- ── compiler inputs ─────────────────────────────────────────────────────────────
-- name: ListActiveDevicesForOrg :many
-- Every active device whose owner is an active, CURRENT org member, org-wide (all
-- nodes) — the compiler resolves group destinations to these devices' /32s and keys
-- allows by src /32. The memberships join is load-bearing: a removed member's device
-- must not participate in policy (as a source OR a destination) even if the device
-- itself was never revoked. NOT health_blocked (S7.5.3): a health-blocked device's
-- /32 leaves the compiled allow-sets (source AND destination) the same way.
SELECT d.id, d.user_id, d.node_id, d.assigned_ip
FROM devices d
JOIN users u ON u.id = d.user_id
JOIN memberships mem ON mem.org_id = d.org_id AND mem.user_id = d.user_id
WHERE d.org_id = $1
  AND d.status = 'active' AND NOT d.health_blocked AND d.deleted_at IS NULL
  AND u.status = 'active' AND u.deleted_at IS NULL
  AND d.assigned_ip IS NOT NULL AND d.assigned_ip <> ''
ORDER BY d.assigned_ip;

-- ── org enforcement mode ────────────────────────────────────────────────────────
-- name: SetOrgZeroTrustMode :one
UPDATE organizations
SET zero_trust_mode = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: GetPolicyRuleForOrg :one
-- Resolve one rule (org-scoped) — S7.5.1 ingest enriches an allow event's kernel-stamped
-- rule_id into the grant's destination (resource/group) it named, captured AT EVENT TIME so
-- it survives a later rule delete. Returns no rows if the rule was already deleted.
SELECT * FROM policy_rules WHERE id = $1 AND org_id = $2;
