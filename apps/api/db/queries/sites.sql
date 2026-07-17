-- name: CreateSite :one
INSERT INTO sites (org_id, name) VALUES ($1, $2)
RETURNING *;

-- name: GetSite :one
SELECT * FROM sites WHERE id = $1 AND org_id = $2;

-- name: ListSitesByOrg :many
SELECT * FROM sites WHERE org_id = $1 ORDER BY created_at;

-- name: DeleteSite :execrows
DELETE FROM sites WHERE id = $1 AND org_id = $2;

-- name: AddSiteSubnet :one
-- lint:cross-org — site_id is org-checked by the caller (GetSite) before this insert; site_subnets
-- has no org_id column of its own (it inherits the site's org via the FK).
INSERT INTO site_subnets (site_id, cidr) VALUES ($1, $2)
RETURNING *;

-- name: ListSiteSubnets :many
-- lint:cross-org — scoped by site_id, which the caller org-checks via GetSite.
SELECT * FROM site_subnets WHERE site_id = $1 ORDER BY created_at;

-- name: BindNodeToSite :execrows
-- Bind a gateway node to a site IN THE SAME ORG. The EXISTS guard refuses a cross-org bind (a
-- node must not bind to another org's site). The single-node-per-site partial unique index makes a
-- second bind to an already-occupied site a unique violation, which the service maps to a typed 409.
UPDATE nodes SET site_id = $3
WHERE nodes.id = $1 AND nodes.org_id = $2
  AND EXISTS (SELECT 1 FROM sites s WHERE s.id = $3 AND s.org_id = $2);

-- name: UnbindNode :execrows
UPDATE nodes SET site_id = NULL WHERE id = $1 AND org_id = $2;

-- name: GetSiteNode :one
-- lint:cross-org — scoped by site_id (the site is org-checked via GetSite by the caller); returns
-- the single node bound to the site (single-node v1), or no rows when the site has no gateway yet.
SELECT * FROM nodes WHERE site_id = $1;
