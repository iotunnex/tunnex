package devices

import "testing"

func TestRangesStale(t *testing.T) {
	cur := []string{"10.0.0.0/16", "172.31.0.0/16"}
	// exact same set (any order) => fresh.
	if RangesStale([]byte(`["172.31.0.0/16","10.0.0.0/16"]`), cur) {
		t.Fatal("identical range sets must be fresh")
	}
	// a subnet ADDED after export => stale.
	if !RangesStale([]byte(`["10.0.0.0/16"]`), cur) {
		t.Fatal("a range added after export must be stale")
	}
	// a subnet REMOVED after export => stale.
	if !RangesStale([]byte(`["10.0.0.0/16","172.31.0.0/16","192.168.0.0/24"]`), cur) {
		t.Fatal("a range removed after export must be stale")
	}
	// empty snapshot vs current ranges => stale (profile predates the routes).
	if !RangesStale(nil, cur) {
		t.Fatal("an empty snapshot against current ranges must be stale")
	}
	// zero ranges both sides => fresh.
	if RangesStale([]byte(`[]`), nil) {
		t.Fatal("zero ranges both sides must be fresh")
	}
	// S10.3 fork-1: a K8s VIP range added to the org's routed set AFTER a static export must fire the
	// stale-profile badge. RangesStale is range-CLASS-agnostic — it compares whatever ListRoutedRanges
	// returns, which now carries VIP ranges — so this holds by construction, verified here explicitly.
	baked := []byte(`["10.20.0.0/24"]`)                       // exported when the org had only a site subnet
	afterCluster := []string{"10.20.0.0/24", "100.64.0.0/16"} // a cluster was registered → its VIP range joined
	if !RangesStale(baked, afterCluster) {
		t.Fatal("a K8s VIP range added after a static export must mark the profile stale (needs_reexport)")
	}
}
