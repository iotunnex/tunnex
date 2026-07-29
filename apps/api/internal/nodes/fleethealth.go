package nodes

import "context"

// FleetHealthCounts tallies gateways by health kind across EVERY org — the provider behind the
// tunnex_gateway_policy_health metric (S11 D3.1/D3.3).
//
// ONE COMPUTE. It walks orgs and calls PolicyHealthForNodes, the same function the dashboard reads, rather
// than re-deriving health from the node rows — so the metric and the console can never disagree about what
// "apply_failing" means. Every kind in AllKinds() is present in the result (zero when no gateway is in it),
// so the caller never has to distinguish "no gateways in this state" from "this kind was forgotten".
//
// FLEET-WIDE, UNLABELLED (D3.3): the return is counts only. Per-org/per-node series would be unbounded
// cardinality on a shared Prometheus; that detail already lives in the API + dashboard.
//
// Cost: one org list plus one node list + health compute per org, run at SCRAPE time. The caller bounds it
// with a context (the metrics handler uses 10s); on error it returns what it has rather than failing the
// whole scrape — a partial fleet count is more useful to an operator than a 500, and the DB-down case is
// already visible through /readyz.
func (s *Service) FleetHealthCounts(ctx context.Context) map[PolicyDegradedKind]int {
	counts := make(map[PolicyDegradedKind]int, len(AllKinds()))
	for _, k := range AllKinds() {
		counts[k] = 0 // every kind present, always — an absent series reads as a scrape failure
	}

	orgs, err := s.q.ListOrganizations(ctx)
	if err != nil {
		return counts
	}
	for _, org := range orgs {
		if ctx.Err() != nil {
			return counts // scrape deadline hit — return the partial tally, never block the scraper
		}
		ns, err := s.ListNodes(ctx, org.ID)
		if err != nil || len(ns) == 0 {
			continue
		}
		for _, h := range s.PolicyHealthForNodes(ctx, org.ID, ns) {
			counts[h.Kind]++
		}
	}
	return counts
}
