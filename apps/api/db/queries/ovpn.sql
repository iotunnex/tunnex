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
