-- S10.3: Kubernetes cluster + exposed-Service queries. Org-scoped (tenant isolation).

-- name: CreateK8sCluster :one
INSERT INTO k8s_clusters (org_id, site_id, name, vip_range, service_cidr, dns_zone, dns_vip)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetK8sCluster :one
SELECT * FROM k8s_clusters WHERE org_id = $1 AND id = $2;

-- name: ListK8sClustersForOrg :many
SELECT * FROM k8s_clusters WHERE org_id = $1 ORDER BY name;

-- name: DeleteK8sCluster :exec
DELETE FROM k8s_clusters WHERE org_id = $1 AND id = $2;

-- CountClusterCascade returns what a DeregisterCluster will destroy, for the audit trail (H2): the number of
-- LIVE exposed Services in the cluster, and the number of policy grants (rules) that reference ANY Service in
-- it. Both are FK ON DELETE CASCADE'd when the cluster row is deleted, so the audit must capture them BEFORE
-- the delete — a governance cascade must never vanish untraceably.
-- name: CountClusterCascade :one
SELECT
  (SELECT count(*) FROM k8s_services s WHERE s.cluster_id = $2 AND s.org_id = $1 AND s.deleted_at IS NULL) AS service_count,
  (SELECT count(*) FROM policy_rules r WHERE r.org_id = $1 AND r.dst_k8s_service_id IN (SELECT s2.id FROM k8s_services s2 WHERE s2.cluster_id = $2)) AS grant_count;

-- ListVIPRangesForOrg feeds the subnetguard collector: EVERY disjointness check (cluster-VIP creation,
-- pool resize, site-subnet approval) must include the org's VIP ranges so disjointness stays bidirectional
-- (the validator-input-filtering law). Returns the raw cidr text.
-- name: ListVIPRangesForOrg :many
SELECT vip_range::text AS vip_range FROM k8s_clusters WHERE org_id = $1;

-- ListK8sClusterZonesForOrg feeds (a) cross-mechanism one-zone-one-resolver enforcement (S10.3 (A)): a site
-- dns_forwarding domain must not collide with a K8s cluster's DNS zone (<cluster>.<dns_zone>), and vice versa;
-- and (b) the client-side resolver push (fork-1): the {<cluster>.<dns_zone> -> reserved DNS VIP} mapping the
-- routed-forwards channel hands split-tunnel/OVPN clients so they resolve exposed Service names.
-- name: ListK8sClusterZonesForOrg :many
SELECT name, dns_zone, COALESCE(host(dns_vip), '')::text AS dns_vip FROM k8s_clusters WHERE org_id = $1;

-- name: CreateK8sService :one
INSERT INTO k8s_services (org_id, cluster_id, name, namespace, protocol, port_low, port_high, vip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetK8sService :one
SELECT * FROM k8s_services WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL;

-- ListUsedVIPsInCluster returns the LIVE VIPs in a cluster (the used-set ipalloc allocates around).
-- org_id-scoped for tenant isolation (defence-in-depth; the caller already authorized the cluster).
-- name: ListUsedVIPsInCluster :many
SELECT host(vip) AS vip FROM k8s_services WHERE org_id = $1 AND cluster_id = $2 AND deleted_at IS NULL;

-- ListActiveK8sServicesForOrg is the compiler's resolution source: id -> current VIP (+ proto/ports), LIVE
-- only. A soft-deleted Service is absent, so a grant referencing it compiles to nothing (honest, not silent).
-- name: ListActiveK8sServicesForOrg :many
SELECT s.id, s.cluster_id, s.name, s.namespace, s.protocol, s.port_low, s.port_high,
       host(s.vip) AS vip, c.site_id, host(c.vip_range) AS vip_range, c.service_cidr::text AS service_cidr,
       c.name AS cluster_name, c.dns_zone, COALESCE(host(c.dns_vip), '')::text AS dns_vip
FROM k8s_services s
JOIN k8s_clusters c ON c.id = s.cluster_id
WHERE s.org_id = $1 AND s.deleted_at IS NULL
ORDER BY s.id;

-- name: SoftDeleteK8sService :exec
UPDATE k8s_services SET deleted_at = now() WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL;
