// Package k8s is the control-plane side of exposing in-cluster Kubernetes Services to the fabric (S10.3).
// A k8s_cluster owns a disjoint synthetic VIP range (validated by the shared subnetguard collector); an
// exposed Service is a STABLE-IDENTITY destination allocated a /32 VIP from that range. The gateway DNATs
// VIP -> the real ClusterIP; the compiler resolves a grant's Service identity -> its CURRENT VIP at
// compile time (Slice 2), so a freed-then-reused VIP never confuses grants — see the VIP-stability note.
//
// Governance (a grant that reaches an exposed Service) is enterprise; the model here is CORE (like sites),
// with the enterprise gate landing in the API layer (Slice 7).
package k8s

import (
	"context"
	"errors"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/ipalloc"
	"github.com/tunnexio/tunnex/apps/api/internal/pgerr"
	"github.com/tunnexio/tunnex/apps/api/internal/subnetguard"
	"github.com/tunnexio/tunnex/apps/api/internal/subnetsrc"
)

type Service struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, q: sqlc.New(pool)} }

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RegisterCluster creates a K8s cluster and its synthetic VIP range. The range must be DISJOINT from
// every allocatable class in the org — the device pool, every site subnet, AND other clusters' VIP
// ranges — assembled by the ONE shared collector (F2). A collision is the forbidden outcome (ambiguous
// routing), refused with a typed, teaching error naming the class and what to do.
func (s *Service) RegisterCluster(ctx context.Context, orgID, siteID uuid.UUID, name string, vipRange netip.Prefix) (sqlc.K8sCluster, error) {
	var out sqlc.K8sCluster
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		// The cluster must be fronted by a real site in THIS org (one gateway = one site, D1).
		if _, e := q.GetSite(ctx, sqlc.GetSiteParams{ID: siteID, OrgID: orgID}); e != nil {
			return apierr.NotFound("site_not_found", "no such site in this organization")
		}
		ranges, e := subnetguard.Collect(ctx, subnetsrc.Source{Q: q}, orgID)
		if e != nil {
			return e
		}
		if ov, ok := subnetguard.Check(vipRange, ranges); !ok {
			return apierr.BadRequest("vip_range_overlap",
				"the VIP range "+vipRange.String()+" overlaps "+string(ov.Class)+" "+ov.With.String()+
					"; choose a range disjoint from your device pool, your site subnets, and other clusters' VIP ranges")
		}
		c, e := q.CreateK8sCluster(ctx, sqlc.CreateK8sClusterParams{
			OrgID: orgID, SiteID: siteID, Name: name, VipRange: vipRange.Masked(),
		})
		if pgerr.IsUnique(e) {
			return apierr.Conflict("cluster_exists", "a cluster with that name or VIP range already exists in this organization")
		}
		if e != nil {
			return e
		}
		out = c
		return nil
	})
	return out, err
}

// ExposeService allocates a /32 VIP from the cluster's range (ipalloc, used-set = the cluster's LIVE VIPs)
// and records the exposed Service.
//
// VIP-STABILITY (the reassignment hazard is born here): the used-set is LIVE Services only, so a
// soft-deleted Service's VIP is immediately reusable. That is SAFE because a grant references a Service's
// stable ID, and the compiler resolves ID -> CURRENT VIP at compile time and NEVER caches a VIP (Slice 2):
// a deleted Service vanishes from the resolution set (its grant compiles to nothing), and the reused VIP
// belongs unambiguously to the NEW Service's identity. Identity-resolution is therefore sufficient — no
// VIP quarantine is needed. (The reassignment-trap red lives in Slice 2, where the resolution is built.)
func (s *Service) ExposeService(ctx context.Context, orgID, clusterID uuid.UUID, name, namespace, protocol string, portLow, portHigh *int32) (sqlc.K8sService, error) {
	if protocol == "" {
		protocol = "any"
	}
	var out sqlc.K8sService
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		cluster, e := q.GetK8sCluster(ctx, sqlc.GetK8sClusterParams{OrgID: orgID, ID: clusterID})
		if e != nil {
			return apierr.NotFound("cluster_not_found", "no such cluster in this organization")
		}
		used, e := q.ListUsedVIPsInCluster(ctx, clusterID)
		if e != nil {
			return e
		}
		vipStr, e := ipalloc.Allocate(cluster.VipRange.String(), used)
		if errors.Is(e, ipalloc.ErrPoolExhausted) {
			return apierr.Conflict("vip_range_exhausted",
				"the cluster's VIP range "+cluster.VipRange.String()+" is full; register the cluster with a larger range to expose more Services")
		}
		if e != nil {
			return e
		}
		vip, e := netip.ParseAddr(vipStr)
		if e != nil {
			return e
		}
		svc, e := q.CreateK8sService(ctx, sqlc.CreateK8sServiceParams{
			OrgID: orgID, ClusterID: clusterID, Name: name, Namespace: namespace,
			Protocol: protocol, PortLow: portLow, PortHigh: portHigh, Vip: vip,
		})
		if pgerr.IsUnique(e) {
			return apierr.Conflict("service_exists", "that Service (namespace/name) is already exposed in this cluster")
		}
		if e != nil {
			return e
		}
		out = svc
		return nil
	})
	return out, err
}
