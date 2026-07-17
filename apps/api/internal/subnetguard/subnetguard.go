// Package subnetguard is the ONE disjointness validator (S8.1 D5/D7). A candidate prefix — a site
// subnet being advertised, OR a resized device pool — must be DISJOINT from three classes: the org's
// other site subnets, the device pool CIDR, and reserved ranges. It is called from BOTH seams
// (advertisement-approval AND pool-resize) so the check can't diverge, with the class carried so each
// caller renders its own typed error (gateway_no_egress-class vs illegal_resize-class).
//
// The CIDR math is netip.Prefix.Overlaps — stdlib — so ADJACENCY (touching-but-disjoint, e.g.
// 10.0.0.0/24 and 10.0.1.0/24) is NOT an overlap, an EXACT-BOUNDARY subset (10.0.0.0/24 vs
// 10.0.0.0/25) IS, and there is no hand-rolled off-by-one (where these validators actually break).
package subnetguard

import "net/netip"

// OverlapClass names which input a candidate collided with (for the caller's typed error).
type OverlapClass string

const (
	ClassSiteSubnet OverlapClass = "site_subnet"
	ClassPool       OverlapClass = "pool"
	ClassReserved   OverlapClass = "reserved"
)

// Overlap is the first collision found: the existing prefix and its class.
type Overlap struct {
	With  netip.Prefix
	Class OverlapClass
}

// Check reports whether candidate is DISJOINT from every input (ok=true), or the FIRST overlap it hit
// (ok=false). Order is site subnets → pool → reserved, so the class of the first collision is stable.
// candidate and all inputs are compared masked (host bits ignored). An invalid `pool` is skipped
// (the pool-resize caller passes an invalid pool because it IS resizing the pool).
func Check(candidate netip.Prefix, siteSubnets []netip.Prefix, pool netip.Prefix, reserved []netip.Prefix) (Overlap, bool) {
	c := candidate.Masked()
	for _, s := range siteSubnets {
		if c.Overlaps(s.Masked()) {
			return Overlap{With: s, Class: ClassSiteSubnet}, false
		}
	}
	if pool.IsValid() && c.Overlaps(pool.Masked()) {
		return Overlap{With: pool, Class: ClassPool}, false
	}
	for _, r := range reserved {
		if c.Overlaps(r.Masked()) {
			return Overlap{With: r, Class: ClassReserved}, false
		}
	}
	return Overlap{}, true
}
