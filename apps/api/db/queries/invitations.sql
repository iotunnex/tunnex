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

-- name: RevokeInvitationByOrgEmail :execrows
UPDATE invitations
SET revoked_at = now()
WHERE org_id = $1 AND email = $2 AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: SupersedePendingInvites :exec
-- When a user joins an org another way (e.g. domain-capture JIT), pending
-- invites for that (org, email) become moot — revoke them so they can't be
-- accepted into a second membership attempt.
UPDATE invitations
SET revoked_at = now()
WHERE org_id = $1 AND email = $2 AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: ListInvitations :many
SELECT * FROM invitations
WHERE org_id = $1
ORDER BY created_at DESC;
