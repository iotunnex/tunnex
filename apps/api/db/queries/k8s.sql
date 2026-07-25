-- S10.3: Kubernetes cluster + exposed-Service queries. Org-scoped (tenant isolation).

-- name: CreateK8sCluster :one
INSERT INTO k8s_clusters (org_id, site_id, name, vip_range, service_cidr)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetK8sCluster :one
SELECT * FROM k8s_clusters WHERE org_id = $1 AND id = $2;

-- name: ListK8sClustersForOrg :many
SELECT * FROM k8s_clusters WHERE org_id = $1 ORDER BY name;

-- name: DeleteK8sCluster :exec
DELETE FROM k8s_clusters WHERE org_id = $1 AND id = $2;

-- ListVIPRangesForOrg feeds the subnetguard collector: EVERY disjointness check (cluster-VIP creation,
-- pool resize, site-subnet approval) must include the org's VIP ranges so disjointness stays bidirectional
-- (the validator-input-filtering law). Returns the raw cidr text.
-- name: ListVIPRangesForOrg :many
SELECT vip_range::text AS vip_range FROM k8s_clusters WHERE org_id = $1;

-- name: CreateK8sService :one
INSERT INTO k8s_services (org_id, cluster_id, name, namespace, protocol, port_low, port_high, vip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetK8sService :one
SELECT * FROM k8s_services WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL;

-- ListUsedVIPsInCluster returns the LIVE VIPs in a cluster (the used-set ipalloc allocates around).
-- name: ListUsedVIPsInCluster :many
SELECT host(vip) AS vip FROM k8s_services WHERE cluster_id = $1 AND deleted_at IS NULL;

-- ListActiveK8sServicesForOrg is the compiler's resolution source: id -> current VIP (+ proto/ports), LIVE
-- only. A soft-deleted Service is absent, so a grant referencing it compiles to nothing (honest, not silent).
-- name: ListActiveK8sServicesForOrg :many
SELECT s.id, s.cluster_id, s.name, s.namespace, s.protocol, s.port_low, s.port_high,
       host(s.vip) AS vip, c.site_id, host(c.vip_range) AS vip_range, c.service_cidr::text AS service_cidr
FROM k8s_services s
JOIN k8s_clusters c ON c.id = s.cluster_id
WHERE s.org_id = $1 AND s.deleted_at IS NULL
ORDER BY s.id;

-- name: SoftDeleteK8sService :exec
UPDATE k8s_services SET deleted_at = now() WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL;
