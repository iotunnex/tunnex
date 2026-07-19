package sites

import (
	"context"
	"net/netip"
	"sort"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// ListRoutedRanges returns the org's DECLARED routed LAN ranges for split-tunnel device AllowedIPs
// (S8.5) — the org's APPROVED site subnets (D1: a routed range IS an approved site subnet, so PENDING
// subnets never appear — routing-before-approval is the inverse of the routed≠permitted thesis).
// RANGES ONLY: no keys, no endpoints, no pool, no policy — so the client's never-re-fetch IDENTITY
// invariant survives (routes were never identity). Canonical (masked prefix) + sorted + deduped so the
// client's churn-free merge (2c) can byte-compare + two calls return an identical body. Empty is a
// first-class answer: a no-ranges org returns [].
func (s *Service) ListRoutedRanges(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	subs, err := s.q.ListSiteSubnetsForOrg(ctx, orgID) // approved-only (the query filters status='approved')
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(subs))
	for _, ss := range subs {
		c := ss.Cidr.Masked().String() // canonical masked form (deterministic)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

// RouteLAN is the S8.5 D1 ONE-SCREEN affordance's backend: it routes a LAN CIDR through a gateway in a
// single call by COMPOSING the four existing service methods — RegisterSite → BindNode → AddSubnet →
// ApproveSubnet. It is deliberately a COMPOSITE OF THE SAME CODE, not a new bespoke flow: so the DB state
// (site row, node.site_id, the approved subnet) AND the audit trail (the FOUR constituent events, by
// construction) are BYTE-IDENTICAL to an admin performing the four steps by hand — the short path is
// exactly as auditable as the long one, and never emits a single composite event. If the disjointness
// validator REFUSES the approval (the range collides), the site + bind + PENDING subnet persist — again
// byte-identical to the long path's advertise-then-refused state — and the typed refusal (with its S8.5
// teaching text) is returned. name is optional: blank derives a sensible default from the CIDR.
func (s *Service) RouteLAN(ctx context.Context, actor, orgID, nodeID uuid.UUID, name string, cidr netip.Prefix) (sqlc.Site, sqlc.SiteSubnet, error) {
	if name == "" {
		name = "LAN " + cidr.Masked().String() // sensible default for the solo-admin path
	}
	site, err := s.RegisterSite(ctx, orgID, name)
	if err != nil {
		return sqlc.Site{}, sqlc.SiteSubnet{}, err
	}
	if err := s.BindNode(ctx, orgID, site.ID, nodeID); err != nil {
		return site, sqlc.SiteSubnet{}, err
	}
	sub, err := s.AddSubnet(ctx, orgID, site.ID, cidr)
	if err != nil {
		return site, sqlc.SiteSubnet{}, err
	}
	if err := s.ApproveSubnet(ctx, actor, orgID, sub.ID); err != nil {
		return site, sub, err // refusal: site+bind+pending persist (byte-identical to advertise-then-refused)
	}
	return site, sub, nil
}
