-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertUser :one
-- Used by the seed with a fixed id; idempotent.
INSERT INTO users (id, email, name)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
    SET email = EXCLUDED.email, name = EXCLUDED.name
RETURNING *;

-- name: CreateUser :one
INSERT INTO users (email, name, password_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: SetUserPassword :exec
UPDATE users
SET password_hash = $2
WHERE id = $1 AND deleted_at IS NULL;

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified_at = now()
WHERE id = $1 AND deleted_at IS NULL AND email_verified_at IS NULL;
