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
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
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
	if _, err := authorize(ctx, req.OrgId, rbac.PermSiteManage); err != nil {
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
