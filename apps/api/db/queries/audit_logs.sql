-- audit_logs is append-only: there are intentionally NO update or delete queries
-- here, and the DB enforces it (see 0002 triggers).

-- name: InsertAuditLog :one
INSERT INTO audit_logs (org_id, actor_user_id, action, target_type, target_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAuditLogsByOrg :many
SELECT * FROM audit_logs
WHERE org_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
