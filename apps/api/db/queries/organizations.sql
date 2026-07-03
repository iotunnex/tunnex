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

-- name: UpdateOrganizationName :one
-- Slug is immutable after creation (S1.2); only name is updatable here.
UPDATE organizations
SET name = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOrganization :execrows
UPDATE organizations
SET deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertOrganization :one
-- Used by the seed with a fixed id; idempotent. Also clears deleted_at so
-- re-seeding restores a previously soft-deleted demo org to a clean live state.
INSERT INTO organizations (id, name, slug)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
    SET name = EXCLUDED.name, slug = EXCLUDED.slug, deleted_at = NULL
RETURNING *;
