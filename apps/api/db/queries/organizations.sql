-- name: CreateOrganization :one
INSERT INTO organizations (name, slug)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations
WHERE slug = $1 AND deleted_at IS NULL;

-- name: ListOrganizations :many
SELECT * FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at;

-- name: CountOrganizations :one
SELECT count(*) FROM organizations
WHERE deleted_at IS NULL;

-- name: SoftDeleteOrganization :exec
UPDATE organizations
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertOrganization :one
-- Used by the seed with a fixed id; idempotent.
INSERT INTO organizations (id, name, slug)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
    SET name = EXCLUDED.name, slug = EXCLUDED.slug
RETURNING *;
