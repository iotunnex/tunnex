-- Machine credentials (S10.2): an org-scoped, NON-USER principal for the GitOps operator. Mirror of the
-- cli_credentials pattern — sha256 hash storage, fingerprint-only display, revoke-severs-on-next-request.
-- The raw token NEVER reaches SQL. Revoke is org-scoped (a caller can only revoke its own org's creds).

-- name: CreateMachineCredential :one
INSERT INTO machine_credentials (org_id, name, role, token_hash, fingerprint)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMachineCredentialByHash :one
-- lint:cross-org — an auth lookup by the secret HASH; the row resolves the org (the hash IS the credential).
-- Returns the row regardless of revoked state — the auth path applies the NO-ORACLE check (revoked /
-- unknown are indistinguishable at the wire), exactly like the CLI credential path.
SELECT * FROM machine_credentials WHERE token_hash = $1;

-- name: TouchMachineCredentialUsed :exec
-- lint:cross-org — best-effort telemetry keyed by the credential id resolved from the hash lookup above.
UPDATE machine_credentials SET last_used_at = now() WHERE id = $1;

-- name: ListMachineCredentialsForOrg :many
SELECT * FROM machine_credentials
WHERE org_id = $1 AND revoked_at IS NULL
ORDER BY created_at DESC;

-- name: RevokeMachineCredential :execrows
-- Org-scoped + idempotent (already-revoked returns 0 rows). Revocation severs on the very next request
-- (the auth path re-reads the row every time — no session cache).
UPDATE machine_credentials SET revoked_at = now()
WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL;

-- name: AssignMachineCredentialOwner :execrows
-- S15.1 (D14/D19 step 2) — an admin NAMES the owner. There is no created_by on this table, so the minting
-- user is not recoverable from the row: the admin is CHOOSING, not confirming, and nothing here guesses.
--
-- ⛔ THE OWNER MUST BE IN THE CREDENTIAL'S ORG, ENFORCED IN THE STATEMENT. A cross-org owner would attribute a
-- machine principal to someone who cannot see it. The EXISTS is org-scoped both ways — credential and user —
-- so a mismatched pair updates zero rows rather than succeeding quietly.
UPDATE machine_credentials mc
SET user_id = $3
WHERE mc.id = $1
  AND mc.org_id = $2
  AND mc.revoked_at IS NULL
  -- ⚠ MEMBERSHIP IS RELATIONAL — `users` has NO org_id (measured, not assumed; the first draft of this
  -- statement joined a column that does not exist and would have matched nothing). Org scoping goes through
  -- `memberships`, and the user must still be live.
  AND EXISTS (
      SELECT 1 FROM memberships m
      JOIN users u ON u.id = m.user_id
      WHERE m.user_id = $3 AND m.org_id = $2
        AND u.deleted_at IS NULL AND u.status = 'active'
  );
