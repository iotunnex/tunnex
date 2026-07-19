package http

import (
	"context"
	"net/netip"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// S8.1 site-to-site handlers (EPIC 8). All are site:manage-gated (owner/admin) — site registration,
// binding, subnet advertisement, and approval are network-shaping powers. The sites service is CORE
// (all editions, D11); Zero-Trust governance of the resulting site traffic is enterprise (Slice 3).

func toAPISite(s sqlc.Site) api.Site {
	var mtu *int
	if s.LinkMtu != nil {
		v := int(*s.LinkMtu)
		mtu = &v
	}
	return api.Site{Id: s.ID, Name: s.Name, LinkTransport: api.SiteLinkTransport(s.LinkTransport), LinkMtu: mtu, CreatedAt: s.CreatedAt}
}

func (s apiServer) ListSites(ctx context.Context, req api.ListSitesRequestObject) (api.ListSitesResponseObject, error) {
	// S8.3 D5: a MEMBER reads the topology their traffic traverses (org:view — the same member-read gate as
	// ListNodes/ListDevices/Overview). Mutations + getSiteReferences + the pending queue stay site:manage.
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	list, err := s.sites.ListSites(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.Site, len(list))
	for i, x := range list {
		out[i] = toAPISite(x)
	}
	return api.ListSites200JSONResponse{Body: out, Headers: api.ListSites200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ListRoutedRanges GET /organizations/{orgId}/routed-ranges — the org's declared routed LAN ranges for
// split-tunnel device AllowedIPs (S8.5). SAME auth class as the revocation poll: a member (org:view) of
// THIS org. The org-scoped authorize IS the cross-org guard — a device (its user's bearer) in org A can
// never fetch org B's ranges (authorize 403s a non-member). Ranges-ONLY; approved-only; sorted/canonical;
// empty is a first-class answer.
func (s apiServer) ListRoutedRanges(ctx context.Context, req api.ListRoutedRangesRequestObject) (api.ListRoutedRangesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	ranges, err := s.sites.ListRoutedRanges(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	return api.ListRoutedRanges200JSONResponse{
		Body:    api.RoutedRanges{Ranges: ranges},
		Headers: api.ListRoutedRanges200ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

// RouteLAN POST /organizations/{orgId}/routed-lans — the S8.5 D1 one-screen affordance: route a LAN
// through a gateway in one call (register-site + bind + advertise + approve, byte-identical to the long
// path). site:manage (all-editions core, D11). A range collision returns the typed refusal (site+bind+
// pending persist). name is optional.
func (s apiServer) RouteLAN(ctx context.Context, req api.RouteLANRequestObject) (api.RouteLANResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "node_id + cidr are required")
	}
	cidr, err := netip.ParsePrefix(req.Body.Cidr)
	if err != nil || !cidr.Addr().Is4() {
		return nil, apierr.BadRequest("invalid_cidr", "cidr must be a valid IPv4 CIDR")
	}
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	p, _ := authctx.PrincipalFrom(ctx)
	site, _, err := s.sites.RouteLAN(ctx, p.UserID, req.OrgId, req.Body.NodeId, name, cidr)
	if err != nil {
		return nil, err
	}
	return api.RouteLAN201JSONResponse{Body: toAPISite(site), Headers: api.RouteLAN201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) RegisterSite(ctx context.Context, req api.RegisterSiteRequestObject) (api.RegisterSiteResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	if req.Body == nil || req.Body.Name == "" {
		return nil, apierr.BadRequest("name_required", "a site name is required")
	}
	site, err := s.sites.RegisterSite(ctx, req.OrgId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return api.RegisterSite201JSONResponse{Body: toAPISite(site), Headers: api.RegisterSite201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) AddSiteSubnet(ctx context.Context, req api.AddSiteSubnetRequestObject) (api.AddSiteSubnetResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	cidr, err := netip.ParsePrefix(req.Body.Cidr)
	if err != nil || !cidr.Addr().Is4() {
		return nil, apierr.BadRequest("invalid_cidr", "cidr must be a valid IPv4 CIDR")
	}
	sub, err := s.sites.AddSubnet(ctx, req.OrgId, req.SiteId, cidr)
	if err != nil {
		return nil, err
	}
	return api.AddSiteSubnet201JSONResponse{
		Body:    api.SiteSubnet{Id: sub.ID, SiteId: sub.SiteID, Cidr: sub.Cidr.String(), Status: api.SiteSubnetStatus(sub.Status)},
		Headers: api.AddSiteSubnet201ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

func (s apiServer) BindSiteNode(ctx context.Context, req api.BindSiteNodeRequestObject) (api.BindSiteNodeResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if err := s.sites.BindNode(ctx, req.OrgId, req.SiteId, req.Body.NodeId); err != nil {
		return nil, err
	}
	return api.BindSiteNode204Response{}, nil
}

// UnbindSiteNode detaches the site's gateway (D6 replace-node = unbind then bind a new node).
func (s apiServer) UnbindSiteNode(ctx context.Context, req api.UnbindSiteNodeRequestObject) (api.UnbindSiteNodeResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	if err := s.sites.UnbindSiteNode(ctx, req.OrgId, req.SiteId); err != nil {
		return nil, err
	}
	return api.UnbindSiteNode204Response{}, nil
}

func (s apiServer) ListSiteSubnets(ctx context.Context, req api.ListSiteSubnetsRequestObject) (api.ListSiteSubnetsResponseObject, error) {
	// S8.3 D5: member-readable (org:view) — part of the read-only topology; approval/advertise stay site:manage.
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	list, err := s.sites.ListSubnets(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	out := make([]api.SiteSubnet, len(list))
	for i, x := range list {
		out[i] = api.SiteSubnet{Id: x.ID, SiteId: x.SiteID, Cidr: x.Cidr.String(), Status: api.SiteSubnetStatus(x.Status)}
	}
	return api.ListSiteSubnets200JSONResponse{Body: out, Headers: api.ListSiteSubnets200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) ListPendingSiteSubnets(ctx context.Context, req api.ListPendingSiteSubnetsRequestObject) (api.ListPendingSiteSubnetsResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	list, err := s.sites.ListPendingSubnets(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.SiteSubnet, len(list))
	for i, x := range list {
		out[i] = api.SiteSubnet{Id: x.ID, SiteId: x.SiteID, Cidr: x.Cidr.String(), Status: api.SiteSubnetStatus(x.Status)}
	}
	return api.ListPendingSiteSubnets200JSONResponse{Body: out, Headers: api.ListPendingSiteSubnets200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) ApproveSiteSubnet(ctx context.Context, req api.ApproveSiteSubnetRequestObject) (api.ApproveSiteSubnetResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.sites.ApproveSubnet(ctx, p.UserID, req.OrgId, req.SubnetId); err != nil {
		return nil, err
	}
	return api.ApproveSiteSubnet204Response{}, nil
}

// ListSiteDNSForwards GET /sites/{siteId}/dns-forwards — the site's cross-site DNS zones (S8.4; site:manage, core).
func (s apiServer) ListSiteDNSForwards(ctx context.Context, req api.ListSiteDNSForwardsRequestObject) (api.ListSiteDNSForwardsResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	fwds, err := s.sites.ListDNSForwards(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	body := make([]api.DNSForward, 0, len(fwds))
	for _, f := range fwds {
		body = append(body, api.DNSForward{Domain: f.Domain, ResolverIp: f.ResolverIP})
	}
	return api.ListSiteDNSForwards200JSONResponse{Body: body, Headers: api.ListSiteDNSForwards200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// SetSiteDNSForward POST /sites/{siteId}/dns-forwards — add/update a forwarded zone (S8.4; site:manage, core).
func (s apiServer) SetSiteDNSForward(ctx context.Context, req api.SetSiteDNSForwardRequestObject) (api.SetSiteDNSForwardResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_body", "domain + resolver_ip required")
	}
	if err := s.sites.SetDNSForward(ctx, p.UserID, req.OrgId, req.SiteId, req.Body.Domain, req.Body.ResolverIp); err != nil {
		return nil, err
	}
	return api.SetSiteDNSForward204Response{}, nil
}

// RemoveSiteDNSForward DELETE /sites/{siteId}/dns-forwards/{domain} — full-sweep withdraw (S8.4; site:manage, core).
func (s apiServer) RemoveSiteDNSForward(ctx context.Context, req api.RemoveSiteDNSForwardRequestObject) (api.RemoveSiteDNSForwardResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.sites.RemoveDNSForward(ctx, p.UserID, req.OrgId, req.SiteId, req.Domain); err != nil {
		return nil, err
	}
	return api.RemoveSiteDNSForward204Response{}, nil
}

// RemoveSiteSubnet DELETE /site-subnets/{subnetId} — un-advertise / remove a subnet (WF-5). All-editions
// core like the rest of the site model (authorize FIRST, no edition gate); route withdrawn full-sweep.
func (s apiServer) RemoveSiteSubnet(ctx context.Context, req api.RemoveSiteSubnetRequestObject) (api.RemoveSiteSubnetResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.sites.RemoveSubnet(ctx, p.UserID, req.OrgId, req.SubnetId); err != nil {
		return nil, err
	}
	return api.RemoveSiteSubnet204Response{}, nil
}

// GetSiteReferences GET /sites/{siteId} — the D1 reverse link + D4 cascade preview counts.
func (s apiServer) GetSiteReferences(ctx context.Context, req api.GetSiteReferencesRequestObject) (api.GetSiteReferencesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	refs, err := s.sites.GetReferences(ctx, req.OrgId, req.SiteId)
	if err != nil {
		return nil, err
	}
	return api.GetSiteReferences200JSONResponse{
		Body:    api.SiteReferences{RuleCount: int(refs.RuleCount), SubnetCount: int(refs.SubnetCount)},
		Headers: api.GetSiteReferences200ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

// DeleteSite DELETE /sites/{siteId} — cascades subnets + site-referencing rules; unbinds the gateway (D4).
func (s apiServer) DeleteSite(ctx context.Context, req api.DeleteSiteRequestObject) (api.DeleteSiteResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.sites.DeleteSite(ctx, p.UserID, req.OrgId, req.SiteId); err != nil {
		return nil, err
	}
	return api.DeleteSite204Response{}, nil
}
