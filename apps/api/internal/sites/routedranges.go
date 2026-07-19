package sites

import (
	"context"
	"sort"

	"github.com/google/uuid"
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
