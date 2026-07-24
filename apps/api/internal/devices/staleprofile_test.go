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
}
