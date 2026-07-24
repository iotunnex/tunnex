-- S9.1 Slice 2: OpenVPN client-cert records. The issuance path records the cert identity so the
-- Slice 5 revocation full-sweep + CRL have their source (B2). The private key is never stored.

-- name: InsertOVPNClientCert :one
INSERT INTO ovpn_client_certs (org_id, device_id, serial, common_name, not_after)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListActiveOVPNClientCertsByOrg :many
-- The CRL source: every un-revoked, issued client cert for an org (Slice 5 builds the CRL from
-- the COMPLEMENT — revoked serials — but this read backs the "live profiles" surface).
SELECT * FROM ovpn_client_certs
WHERE org_id = $1 AND revoked_at IS NULL
ORDER BY issued_at;

-- name: ListRevokedOVPNSerialsByOrg :many
-- The CRL entries for an org: serials revoked and not yet past expiry (an expired cert need not
-- appear on the CRL — it's rejected on validity anyway). Slice 5 renders these into the CRL.
SELECT serial, not_after, revoked_at FROM ovpn_client_certs
WHERE org_id = $1 AND revoked_at IS NOT NULL AND not_after > now()
ORDER BY revoked_at;

-- name: RevokeOVPNClientCertsForDevice :many
-- The B2 sweep member: revoking a device revokes ALL its live OVPN certs, returning their serials
-- so the caller pushes the updated CRL to the gateway (one sweep with address-release + status-clear).
-- lint:cross-org — keyed by device_id inside the device-revoke transaction, which the caller has
-- already org-authorized (mirrors RevokeDevicesForNode); the device->org binding is verified upstream.
UPDATE ovpn_client_certs
SET revoked_at = now()
WHERE device_id = $1 AND revoked_at IS NULL
RETURNING serial;

-- name: GetOVPNServerCertForNode :one
-- lint:cross-org — keyed by node_id; the caller (DesiredState) already authorized the node via mTLS.
SELECT * FROM ovpn_server_certs WHERE node_id = $1;

-- name: InsertOVPNServerCert :one
INSERT INTO ovpn_server_certs (org_id, node_id, serial, cert_pem, sealed_key, not_after)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: BumpOVPNCRLNumber :one
-- Atomically ALLOCATE the next monotonic per-org CRL number (D-S9.5-1: per-org, never a global counter).
-- Concurrent rebuilds get DISTINCT numbers; the crl_pem is set immediately after by SetOVPNCRL for THIS
-- number, so the highest-numbered (latest) CRL wins. On first revocation the placeholder crl_pem is empty
-- for the microseconds until SetOVPNCRL runs — delivery treats an empty crl_pem as not-yet-ready.
INSERT INTO ovpn_crls (org_id, crl_pem, number) VALUES ($1, ''::bytea, 1)
ON CONFLICT (org_id) DO UPDATE SET number = ovpn_crls.number + 1
RETURNING number;

-- name: SetOVPNCRL :exec
-- Store the signed CRL for the number THIS rebuild allocated. WHERE number = $3 so a concurrent rebuild
-- that bumped past us (higher number, later revocation snapshot) is authoritative — our lower-numbered CRL
-- is simply not stored (the latest full-set CRL wins).
UPDATE ovpn_crls SET crl_pem = $2, updated_at = now() WHERE org_id = $1 AND number = $3;

-- name: GetOVPNCRLForOrg :one
-- The org's current signed CRL (delivery reads this; empty crl_pem = not-yet-ready, skip this tick).
SELECT crl_pem, number FROM ovpn_crls WHERE org_id = $1;

-- name: RevokeOVPNClientCertsForNode :many
-- The node-revoke sweep member: revoking a NODE revokes all its devices (RevokeDevicesForNode), so their
-- live OVPN client certs are revoked too (revoked_at), returning the affected orgs so the shared RebuildCRL
-- runs once per org. lint:cross-org — keyed by node_id inside the node-revoke transaction (org-authorized
-- upstream, mirrors RevokeDevicesForNode).
UPDATE ovpn_client_certs SET revoked_at = now()
WHERE device_id IN (SELECT id FROM devices WHERE node_id = $1 AND deleted_at IS NULL) AND revoked_at IS NULL
RETURNING org_id;
