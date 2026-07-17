//go:build linux

package reconcile

import (
	"net/netip"
	"testing"
)

// TestParseRouteDstNormalizesHost — S8.2 review #3: `ip route show` prints a host route as a BARE address
// (no /32), so a desired "10.1.0.5/32" and the enumerated "10.1.0.5" MUST canonicalize equal — otherwise
// a /32 site route churns install→delete every reconcile tick and blackholes.
func TestParseRouteDstNormalizesHost(t *testing.T) {
	want, ok1 := parseRouteDst("10.1.0.5/32")
	got, ok2 := parseRouteDst("10.1.0.5") // the bare form `ip route show` prints
	if !ok1 || !ok2 || got != want {
		t.Fatalf("a bare host must canonicalize to its /32 (no churn): %v vs %v", got, want)
	}
	// A v6 host normalizes to /128 too (the dual-family prune, review #4).
	w6, _ := parseRouteDst("2001:db8::1/128")
	g6, ok := parseRouteDst("2001:db8::1")
	if !ok || g6 != w6 {
		t.Fatalf("a bare v6 host must canonicalize to /128: %v vs %v", g6, w6)
	}
}

// TestRoutesToPruneCanonicalCompare — the pure prune decision compares canonical prefixes, so a desired
// /32 (enumerated bare) is NOT pruned while a genuinely stale route IS. Stability is the proof (#3).
func TestRoutesToPruneCanonicalCompare(t *testing.T) {
	desired := map[netip.Prefix]bool{}
	p, _ := parseRouteDst("10.1.0.5/32")
	desired[p] = true
	q, _ := parseRouteDst("10.2.0.0/24")
	desired[q] = true
	// As `ip route show` prints: the /32 as a bare host, the /24 as-is, plus a stale route we own.
	del := routesToPrune([]string{"10.1.0.5", "10.2.0.0/24", "10.9.0.0/24"}, desired)
	if len(del) != 1 || del[0].String() != "10.9.0.0/24" {
		t.Fatalf("only the stale route must prune (the /32 must NOT churn): %v", del)
	}
}
