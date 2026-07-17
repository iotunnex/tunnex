package subnetguard

import (
	"net/netip"
	"testing"
)

func p(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// TestCheck pins the disjointness validator's CIDR math — especially the ADJACENCY vs EXACT-BOUNDARY
// edges, where off-by-one bugs hide. Table-driven; one validator, three inputs.
func TestCheck(t *testing.T) {
	pool := p("10.99.0.0/24")
	reserved := []netip.Prefix{p("192.168.255.0/24")}
	sites := []netip.Prefix{p("10.20.0.0/24"), p("10.21.0.0/24")}

	cases := []struct {
		name      string
		candidate string
		wantOK    bool
		wantClass OverlapClass
	}{
		// DISJOINT
		{"fully disjoint", "10.30.0.0/24", true, ""},
		// ADJACENCY: touching but disjoint MUST pass (10.20.0.0/24 ends at .0.255; 10.20.1.0/24 begins at .1.0).
		{"adjacent-above a site subnet → disjoint", "10.20.1.0/24", true, ""},
		{"adjacent-below a site subnet → disjoint", "10.19.255.0/24", true, ""},
		{"adjacent to the pool → disjoint", "10.99.1.0/24", true, ""},
		// EXACT-BOUNDARY overlaps MUST refuse.
		{"identical to a site subnet → overlap", "10.20.0.0/24", false, ClassSiteSubnet},
		{"subset of a site subnet (boundary) → overlap", "10.20.0.0/25", false, ClassSiteSubnet},
		{"superset containing a site subnet → overlap", "10.20.0.0/16", false, ClassSiteSubnet},
		{"single host inside a site subnet → overlap", "10.20.0.7/32", false, ClassSiteSubnet},
		// POOL
		{"overlaps the pool → pool class", "10.99.0.128/25", false, ClassPool},
		{"contains the pool → pool class", "10.99.0.0/16", false, ClassPool},
		// RESERVED
		{"overlaps a reserved range → reserved class", "192.168.255.64/26", false, ClassReserved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ov, ok := Check(p(c.candidate), sites, pool, reserved)
			if ok != c.wantOK {
				t.Fatalf("Check ok=%v, want %v (overlap=%+v)", ok, c.wantOK, ov)
			}
			if !ok && ov.Class != c.wantClass {
				t.Fatalf("overlap class=%q, want %q", ov.Class, c.wantClass)
			}
		})
	}
}

// TestCheckClassOrder: site subnets are checked BEFORE the pool BEFORE reserved, so a candidate that
// overlaps more than one input reports the site-subnet class first (stable, caller-meaningful).
func TestCheckClassOrder(t *testing.T) {
	// A candidate that contains everything overlaps a site subnet first.
	ov, ok := Check(p("10.0.0.0/8"), []netip.Prefix{p("10.20.0.0/24")}, p("10.99.0.0/24"), []netip.Prefix{p("10.5.0.0/24")})
	if ok || ov.Class != ClassSiteSubnet {
		t.Fatalf("first overlap must be the site-subnet class, got ok=%v class=%q", ok, ov.Class)
	}
}

// TestCheckInvalidPoolSkipped: the pool-resize caller passes an INVALID pool (it is resizing the pool
// itself); the validator must skip it and still check site subnets + reserved.
func TestCheckInvalidPoolSkipped(t *testing.T) {
	var noPool netip.Prefix // invalid
	if _, ok := Check(p("10.30.0.0/24"), []netip.Prefix{p("10.20.0.0/24")}, noPool, nil); !ok {
		t.Fatal("a disjoint candidate with an invalid (skipped) pool must pass")
	}
	if ov, ok := Check(p("10.20.0.0/25"), []netip.Prefix{p("10.20.0.0/24")}, noPool, nil); ok || ov.Class != ClassSiteSubnet {
		t.Fatalf("a site-subnet overlap must still refuse even with no pool, got ok=%v class=%q", ok, ov.Class)
	}
}
