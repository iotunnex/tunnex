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
