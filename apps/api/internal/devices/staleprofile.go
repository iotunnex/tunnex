package devices

import "encoding/json"

// RangesStale reports whether a STATIC profile's baked ranges snapshot differs from the org's CURRENT
// routed ranges — i.e. the exported profile is out of date and needs re-export (S9.1 Part-2 stale
// surface). Set comparison (order-independent): a subnet added or removed since export makes it stale.
// A nil/empty snapshot against non-empty current ranges is stale (the profile predates any route).
func RangesStale(snapshotJSON []byte, current []string) bool {
	var snap []string
	if len(snapshotJSON) > 0 {
		_ = json.Unmarshal(snapshotJSON, &snap)
	}
	if len(snap) != len(current) {
		return true
	}
	set := make(map[string]bool, len(snap))
	for _, s := range snap {
		set[s] = true
	}
	for _, c := range current {
		if !set[c] {
			return true
		}
	}
	return false
}
