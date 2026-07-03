-- name: CreateInvitation :one
INSERT INTO invitations (org_id, email, role, token_hash, expires_at, invited_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInvitationByTokenHash :one
-- Callers must still check expires_at/accepted_at/revoked_at (single-use).
SELECT * FROM invitations
WHERE token_hash = $1;

-- name: AcceptInvitation :one
-- Single-use: only transitions a pending, unexpired invite.
UPDATE invitations
SET accepted_at = now()
WHERE id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: RevokeInvitation :exec
UPDATE invitations
SET revoked_at = now()
WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL;
