package http

import (
	"context"
	"net/netip"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	k8ssvc "github.com/tunnexio/tunnex/apps/api/internal/k8s"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// S10.3 Kubernetes handlers (EPIC 10). Cluster registration + Service exposure are CONNECTIVITY, so they are
// k8s:manage-gated but CORE (all editions, like sites) — GOVERNANCE (a grant reaching a Service, dst_kind=
// k8s_service) is the separate enterprise gate in the policy handler. List reads use org:view (member-read,
// mirroring ListSites).

func ptrInt32ToInt(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func ptrIntToInt32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

func toAPIK8sCluster(c sqlc.K8sCluster) api.K8sCluster {
	var dnsVIP *string
	if c.DnsVip != nil {
		s := c.DnsVip.String()
		dnsVIP = &s
	}
	created := c.CreatedAt
	return api.K8sCluster{
		Id: c.ID, SiteId: c.SiteID, Name: c.Name,
		VipRange: c.VipRange.String(), ServiceCidr: c.ServiceCidr.String(),
		DnsZone: c.DnsZone, DnsVip: dnsVIP, CreatedAt: &created,
	}
}

func toAPIK8sService(svc sqlc.K8sService, fqdn string) api.K8sService {
	return api.K8sService{
		Id: svc.ID, ClusterId: svc.ClusterID, Name: svc.Name, Namespace: svc.Namespace,
		Protocol: api.K8sServiceProtocol(svc.Protocol),
		PortLow:  ptrInt32ToInt(svc.PortLow), PortHigh: ptrInt32ToInt(svc.PortHigh),
		Vip: svc.Vip.String(), Fqdn: fqdn,
	}
}

func (s apiServer) ListK8sClusters(ctx context.Context, req api.ListK8sClustersRequestObject) (api.ListK8sClustersResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	list, err := s.k8s.ListClusters(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.K8sCluster, len(list))
	for i, c := range list {
		out[i] = toAPIK8sCluster(c)
	}
	return api.ListK8sClusters200JSONResponse{Body: out, Headers: api.ListK8sClusters200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) RegisterK8sCluster(ctx context.Context, req api.RegisterK8sClusterRequestObject) (api.RegisterK8sClusterResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermK8sManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "a request body is required")
	}
	vipRange, err := netip.ParsePrefix(req.Body.VipRange)
	if err != nil {
		return nil, apierr.BadRequest("invalid_vip_range", "vip_range must be a valid CIDR (e.g. 100.64.0.0/16)")
	}
	serviceCIDR, err := netip.ParsePrefix(req.Body.ServiceCidr)
	if err != nil {
		return nil, apierr.BadRequest("invalid_service_cidr", "service_cidr must be a valid CIDR (e.g. 10.96.0.0/12)")
	}
	c, err := s.k8s.RegisterCluster(ctx, req.OrgId, req.Body.SiteId, req.Body.Name, vipRange, serviceCIDR, req.Body.DnsZone, machineID(ctx))
	if err != nil {
		return nil, err
	}
	return api.RegisterK8sCluster201JSONResponse{Body: toAPIK8sCluster(c), Headers: api.RegisterK8sCluster201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) DeregisterK8sCluster(ctx context.Context, req api.DeregisterK8sClusterRequestObject) (api.DeregisterK8sClusterResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermK8sManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	uid, sys, cause := p.AuditActor() // machine → actor_system + cause; human → actor_user_id
	if err := s.k8s.DeregisterCluster(ctx, uid, sys, cause, req.OrgId, req.ClusterId); err != nil {
		return nil, err
	}
	return api.DeregisterK8sCluster204Response{}, nil
}

func (s apiServer) ListK8sServices(ctx context.Context, req api.ListK8sServicesRequestObject) (api.ListK8sServicesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	views, err := s.k8s.ListServicesForCluster(ctx, req.OrgId, req.ClusterId)
	if err != nil {
		return nil, err
	}
	out := make([]api.K8sService, len(views))
	for i, v := range views {
		out[i] = toAPIK8sService(v.Svc, v.FQDN)
	}
	return api.ListK8sServices200JSONResponse{Body: out, Headers: api.ListK8sServices200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) ListK8sServicesForOrg(ctx context.Context, req api.ListK8sServicesForOrgRequestObject) (api.ListK8sServicesForOrgResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	views, err := s.k8s.ListServicesForOrg(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.K8sService, len(views))
	for i, v := range views {
		out[i] = toAPIK8sService(v.Svc, v.FQDN)
	}
	return api.ListK8sServicesForOrg200JSONResponse{Body: out, Headers: api.ListK8sServicesForOrg200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) ExposeK8sService(ctx context.Context, req api.ExposeK8sServiceRequestObject) (api.ExposeK8sServiceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermK8sManage); err != nil {
		return nil, err
	}
	if req.Body == nil || req.Body.Name == "" || req.Body.Namespace == "" {
		return nil, apierr.BadRequest("invalid_request", "name and namespace are required")
	}
	proto := ""
	if req.Body.Protocol != nil {
		proto = string(*req.Body.Protocol)
	}
	svc, err := s.k8s.ExposeService(ctx, req.OrgId, req.ClusterId, req.Body.Name, req.Body.Namespace, proto, ptrIntToInt32(req.Body.PortLow), ptrIntToInt32(req.Body.PortHigh), machineID(ctx))
	if err != nil {
		return nil, err
	}
	// The response carries the resolvable FQDN — built from the owning cluster's name + zone (one truth,
	// k8ssvc.FQDN, the same string loadSiteTopology puts on the wire).
	cluster, err := s.k8s.GetCluster(ctx, req.OrgId, req.ClusterId)
	if err != nil {
		return nil, err
	}
	fqdn := k8ssvc.FQDN(svc.Name, svc.Namespace, cluster.Name, cluster.DnsZone)
	return api.ExposeK8sService201JSONResponse{Body: toAPIK8sService(svc, fqdn), Headers: api.ExposeK8sService201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) UnexposeK8sService(ctx context.Context, req api.UnexposeK8sServiceRequestObject) (api.UnexposeK8sServiceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermK8sManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	uid, sys, cause := p.AuditActor() // machine → actor_system + cause; human → actor_user_id
	if err := s.k8s.UnexposeService(ctx, uid, sys, cause, req.OrgId, req.ServiceId); err != nil {
		return nil, err
	}
	return api.UnexposeK8sService204Response{}, nil
}
