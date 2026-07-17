// Package sites is the S8.1 site/gateway model service (EPIC 8). A SITE is a first-class org
// ENTITY that OWNS a gateway node (D6) — replacing the node preserves the site's identity, subnets,
// and future advertisements. This layer is CORE (all editions, D11): the model + routing are
// plumbing like the IP allocator; Zero-Trust GOVERNANCE of site traffic (enforcing gates a site
// subnet) is the enterprise half, landing in Slice 3.
package sites

import (
	"context"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/pgerr"
)

type Service struct {
	q *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{q: sqlc.New(pool)} }

// RegisterSite creates a site entity in the org. A duplicate name in the org is a 409.
func (s *Service) RegisterSite(ctx context.Context, orgID uuid.UUID, name string) (sqlc.Site, error) {
	site, err := s.q.CreateSite(ctx, sqlc.CreateSiteParams{OrgID: orgID, Name: name})
	if pgerr.IsUnique(err) {
		return sqlc.Site{}, apierr.Conflict("site_name_taken", "a site with that name already exists in this organization")
	}
	return site, err
}

// ListSites returns the org's sites.
func (s *Service) ListSites(ctx context.Context, orgID uuid.UUID) ([]sqlc.Site, error) {
	return s.q.ListSitesByOrg(ctx, orgID)
}

// GetSite fetches one org-scoped site (404 when absent / cross-org).
func (s *Service) GetSite(ctx context.Context, orgID, siteID uuid.UUID) (sqlc.Site, error) {
	site, err := s.q.GetSite(ctx, sqlc.GetSiteParams{ID: siteID, OrgID: orgID})
	if err != nil {
		return sqlc.Site{}, apierr.NotFound("site_not_found", "no such site in this organization")
	}
	return site, nil
}

// AddSubnet attaches a routed subnet to a site (org-checked). Advertisement approval + pool
// disjointness are Slice 4 — this is the model. A duplicate (site, cidr) is a 409.
func (s *Service) AddSubnet(ctx context.Context, orgID, siteID uuid.UUID, cidr netip.Prefix) (sqlc.SiteSubnet, error) {
	if _, err := s.GetSite(ctx, orgID, siteID); err != nil {
		return sqlc.SiteSubnet{}, err
	}
	sub, err := s.q.AddSiteSubnet(ctx, sqlc.AddSiteSubnetParams{SiteID: siteID, Cidr: cidr.Masked()})
	if pgerr.IsUnique(err) {
		return sqlc.SiteSubnet{}, apierr.Conflict("subnet_exists", "that subnet is already on the site")
	}
	return sub, err
}

// ListSubnets returns a site's routed subnets (org-checked).
func (s *Service) ListSubnets(ctx context.Context, orgID, siteID uuid.UUID) ([]sqlc.SiteSubnet, error) {
	if _, err := s.GetSite(ctx, orgID, siteID); err != nil {
		return nil, err
	}
	return s.q.ListSiteSubnets(ctx, siteID)
}

// BindNode binds a gateway node to a site (single-node v1). Cross-org bind or unknown node -> 404;
// binding to an already-occupied site -> 409 (the single-node partial unique index). The node's
// bytes are copied into a pgtype.UUID because nodes.site_id is nullable.
func (s *Service) BindNode(ctx context.Context, orgID, siteID, nodeID uuid.UUID) error {
	n, err := s.q.BindNodeToSite(ctx, sqlc.BindNodeToSiteParams{
		ID: nodeID, OrgID: orgID, SiteID: pgtype.UUID{Bytes: siteID, Valid: true},
	})
	if pgerr.IsUnique(err) {
		return apierr.Conflict("site_has_gateway", "this site already has a gateway (single-node v1); unbind it first")
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.NotFound("node_or_site_not_found", "no such node in this organization, or the site is not in this organization")
	}
	return nil
}

// UnbindNode detaches a node from its site — the SITE entity survives (the point of D6:
// replace-node is unbind-old + bind-new; site identity + subnets are untouched).
func (s *Service) UnbindNode(ctx context.Context, orgID, nodeID uuid.UUID) error {
	n, err := s.q.UnbindNode(ctx, sqlc.UnbindNodeParams{ID: nodeID, OrgID: orgID})
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.NotFound("node_not_found", "no such node in this organization")
	}
	return nil
}
