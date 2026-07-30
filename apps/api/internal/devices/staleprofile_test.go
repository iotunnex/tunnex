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

func ipp(s string) *string { return &s }

// TestProfileStaleAddressHalfAppliesToEVERYMode is the Slice 6 red, and it is written as a MODE MATRIX because
// the defect it pins was an asymmetry, not an absence: `provisioned_ranges` existed and was compared, the address
// was neither recorded nor compared, and the read-time gate excluded managed devices from the whole signal. A
// managed device whose address changed rendered identically to one that never moved.
func TestProfileStaleAddressHalfAppliesToEVERYMode(t *testing.T) {
	cur := []string{"10.0.0.0/16"}
	baked := []byte(`["10.0.0.0/16"]`) // ranges fresh throughout, so only the ADDRESS can move the verdict

	for _, mode := range []string{"static", "managed"} {
		if ProfileStale(mode, baked, cur, ipp("100.64.0.7"), ipp("100.64.0.7")) {
			t.Fatalf("%s: an unchanged address with fresh ranges must be fresh", mode)
		}
		if !ProfileStale(mode, baked, cur, ipp("100.64.0.7"), ipp("100.64.0.9")) {
			t.Fatalf("%s: a device whose issued config bakes 100.64.0.7 while it now holds 100.64.0.9 CANNOT "+
				"connect until re-imported — and this is exactly the cascade-restore re-addressing case (Slice 5), "+
				"which the audit log recorded and the device surface could not show", mode)
		}
	}
}

// TestProfileStaleRangesHalfStaysStaticOnly — the OTHER half of the asymmetry, asserted so a later "make it
// symmetric" tidy-up cannot quietly widen it. A managed device POLLS routes, so baked ranges are not a thing that
// can go stale for it; reporting them would be a permanent false positive on every managed device in an org that
// ever changed a site range.
func TestProfileStaleRangesHalfStaysStaticOnly(t *testing.T) {
	cur := []string{"10.0.0.0/16", "172.31.0.0/16"}
	behind := []byte(`["10.0.0.0/16"]`) // a range added after issuance
	ip := ipp("100.64.0.7")

	if !ProfileStale("static", behind, cur, ip, ip) {
		t.Fatal("static: baked ranges behind the org's current set must be stale (the S9.1 behavior, unchanged)")
	}
	if ProfileStale("managed", behind, cur, ip, ip) {
		t.Fatal("managed: routes are POLLED, so a range added after issuance must NOT be reported stale — it " +
			"would fire on every managed device forever")
	}
}

// TestProfileStaleTreatsUNKNOWNAsFresh — the floor. A row with no recorded snapshot predates the column; there is
// no evidence its address moved, and claiming staleness on absent evidence is the same error as missing it in the
// other direction. Same reasoning as the 0055 backfill bound: a permanent false positive across a healthy fleet
// trains operators to ignore the surface, which is worse than the surface not existing.
func TestProfileStaleTreatsUNKNOWNAsFresh(t *testing.T) {
	baked := []byte(`["10.0.0.0/16"]`)
	cur := []string{"10.0.0.0/16"}

	if ProfileStale("managed", baked, cur, nil, ipp("100.64.0.7")) {
		t.Fatal("no recorded snapshot (row predates 0060) must report fresh, not stale")
	}
	if ProfileStale("managed", baked, cur, ipp("100.64.0.7"), nil) {
		t.Fatal("a device with no assigned address (revoked/pending) has nothing to compare — must report fresh")
	}
	// But an absent snapshot must NOT suppress the ranges half for a static device: that evidence IS present.
	if !ProfileStale("static", []byte(`["10.0.0.0/16"]`), []string{"172.31.0.0/16"}, nil, ipp("100.64.0.7")) {
		t.Fatal("an unknown ADDRESS must not mask a KNOWN stale ranges snapshot — the two causes are independent")
	}
}
