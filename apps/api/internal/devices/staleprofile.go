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

// ProfileStale reports whether a device's ISSUED CONFIG no longer matches reality — the one question
// `needs_reexport` answers, now with two causes rather than one (S13.1 Slice 6).
//
// TWO CAUSES, AND A FUTURE THIRD SHOULD ARRIVE HERE DELIBERATELY:
//
//  1. ROUTES — the baked site ranges no longer match the org's current routed ranges. STATIC ONLY: a managed
//     (desktop-client) device polls routes, so nothing baked can go stale.
//  2. ADDRESS — the tunnel address in the issued config is not the device's current address. EVERY MODE: every
//     config embeds an interface address, so a managed device is just as broken by a change, and recording the
//     snapshot only for static exports is what left those users to discover it by failing to connect.
//
// The operator-facing meaning is unchanged and cause-neutral — "your config is out of date, re-import it" — which
// is why one field with a widened cause set beats a second boolean on a mirror surface. That choice, and this
// sentence, exist because two findings this epic (WF-S11-7, WF-S11-10c) were surfaces added without censusing
// their consumers.
//
// UNKNOWN IS NOT STALE. An absent snapshot (a row predating its column) reports false: claiming staleness on absent
// evidence is the mirror of missing it, and a permanent false positive on a healthy fleet is what the 0055 ruling
// spent a condition avoiding.
func ProfileStale(mode string, snapshotJSON []byte, currentRanges []string, provisionedIP, assignedIP *string) bool {
	if mode == "static" && RangesStale(snapshotJSON, currentRanges) {
		return true
	}
	if provisionedIP == nil || assignedIP == nil {
		return false // nothing recorded, or no address assigned — unknown, not stale
	}
	return *provisionedIP != *assignedIP
}
