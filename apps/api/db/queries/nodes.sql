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

-- name: SetNodeWGPublicKey :execrows
-- lint:cross-org — keyed by id after cert authorization; the node reports its
-- locally-generated WireGuard public key. Returns rows affected so the caller
-- can distinguish a real write from a no-op (e.g. node revoked mid-report).
UPDATE nodes
SET wg_public_key = $2, last_seen_at = now()
WHERE id = $1 AND status = 'active';

-- name: RevokeNode :exec
UPDATE nodes
SET status = 'revoked', revoked_at = now()
WHERE org_id = $1 AND id = $2;
