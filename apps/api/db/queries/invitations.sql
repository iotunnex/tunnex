-- name: CreateInvitation :one
INSERT INTO invitations (org_id, email, role, token_hash, expires_at, invited_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInvitationByTokenHash :one
-- lint:cross-org — the token hash IS the authorization key; lookup is by token,
-- not org. Callers must still check expires_at/accepted_at/revoked_at (single-use).
SELECT * FROM invitations
WHERE token_hash = $1;

-- name: AcceptInvitation :one
-- lint:cross-org — authorized by the invitation id obtained via its token, not
-- by org scope. Single-use: only transitions a pending, unexpired invite.
UPDATE invitations
SET accepted_at = now()
WHERE id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: RevokeInvitation :exec
-- lint:cross-org — revocation targets a specific invitation id (already
-- authorized at the handler layer by org membership before this runs).
UPDATE invitations
SET revoked_at = now()
WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL;
