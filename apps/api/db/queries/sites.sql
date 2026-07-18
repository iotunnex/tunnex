-- name: CreateSite :one
INSERT INTO sites (org_id, name) VALUES ($1, $2)
RETURNING *;

-- name: GetSite :one
SELECT * FROM sites WHERE id = $1 AND org_id = $2;

-- name: ListSitesByOrg :many
SELECT * FROM sites WHERE org_id = $1 ORDER BY created_at;

-- name: DeleteSite :execrows
-- S8.3 D4: WIRED — the deleteSite endpoint (name-typed confirm + cascade preview in the UI). The cascade
-- it triggers (dst_kind='site'/src_kind='site' rules + subnets, ON DELETE CASCADE) is exercised by
-- TestPolicyRuleSiteDstCascade; the bound gateway is unbound (nodes.site_id -> NULL via the FK).
DELETE FROM sites WHERE id = $1 AND org_id = $2;

-- name: CountPolicyRulesReferencingSite :one
-- lint:cross-org — org-scoped by org_id; counts policy rules that name this site as src OR dst. S8.3 D1/D4:
-- the reverse-link "rules referencing this site" and the delete-cascade preview share this ONE count.
SELECT COUNT(*) FROM policy_rules
WHERE org_id = $1 AND (dst_site_id = $2 OR src_site_id = $2);

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

-- name: ListSiteGatewaysForOrg :many
-- S8.2: every site-bound gateway that has reported a WG key, with its site + public endpoint — the
-- input to the hub-and-spoke site-link peer graph + per-node route set. A gateway with no wg_public_key
-- yet can't be a peer, so it is excluded. endpoint is '' for a NAT'd spoke (it dials out).
SELECT id, site_id, wg_public_key, endpoint FROM nodes
WHERE org_id = $1 AND site_id IS NOT NULL AND wg_public_key <> '';

-- name: ListSiteNodesForOrg :many
-- S8.2 compiler input: the (site_id, node_id, endpoint) binding for every site-bound gateway in the org.
-- The compiler places a src_kind='site' grant on the src + dst gateways AND the transit HUB (B1) — the
-- hub is the site gateway with a public endpoint, so endpoint is needed to designate it. site_id is
-- org-scoped via the node row (nodes.org_id).
SELECT id, site_id, endpoint FROM nodes
WHERE org_id = $1 AND site_id IS NOT NULL;

-- name: ListSiteSubnetsForOrg :many
-- lint:cross-org — site_subnets has no org_id of its own; scoped via the join to sites.org_id. The
-- S8.1 compiler input: every APPROVED (site_id, cidr) in the org (D5 — a pending advertisement does NOT
-- propagate), so the compiler expands a dst_kind='site' rule to one AllowEntry per the target site's
-- APPROVED subnets. This same set is the resize disjointness input.
SELECT ss.site_id, ss.cidr
FROM site_subnets ss
JOIN sites s ON s.id = ss.site_id
WHERE s.org_id = $1 AND ss.status = 'approved'
ORDER BY ss.site_id, ss.cidr;

-- name: GetSiteSubnetForOrg :one
-- lint:cross-org — org-scoped via the join to sites.org_id.
SELECT ss.id, ss.site_id, ss.cidr, ss.status
FROM site_subnets ss
JOIN sites s ON s.id = ss.site_id
WHERE ss.id = $1 AND s.org_id = $2;

-- name: ApproveSiteSubnet :one
-- lint:cross-org — the subnet is org-checked via GetSiteSubnetForOrg before approval. Idempotent-ish:
-- approving an already-approved subnet is a no-op UPDATE.
UPDATE site_subnets SET status = 'approved' WHERE id = $1
RETURNING *;

-- name: DeleteSiteSubnet :exec
-- lint:cross-org — the subnet is org-checked via GetSiteSubnetForOrg before deletion (WF-5 un-advertise).
DELETE FROM site_subnets WHERE id = $1;

-- name: ListPendingSiteSubnetsForOrg :many
-- lint:cross-org — org-scoped via the join. The admin review queue (advertised, awaiting approval).
SELECT ss.id, ss.site_id, ss.cidr, ss.status
FROM site_subnets ss
JOIN sites s ON s.id = ss.site_id
WHERE s.org_id = $1 AND ss.status = 'pending'
ORDER BY ss.created_at;
