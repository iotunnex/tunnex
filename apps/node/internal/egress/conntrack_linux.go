//go:build linux

package egress

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	conntrack "github.com/florianl/go-conntrack"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// S8.7 Slice 2 — the expired/revoked-grant conntrack flush. Removing a grant's ACCEPT rule denies
// RE-establishment but does NOT kill an already-established flow: `ct established,related accept` honors an
// open flow indefinitely (a chatty sender refreshes its own conntrack entry). So when the applied Allow set
// LOSES an entry, the agent flushes the conntrack entries for that entry's EXACT tuple — via CT NETLINK, not
// a shelled `conntrack -D` (the gateway image has no conntrack-tools; a shelled flush would silently no-op —
// D4 ratified). THE SECURITY INVARIANT: the flush is scoped to the removed tuple and ONLY it. One predicate
// too wide tears down innocent neighbors — a self-inflicted outage on the busiest gateway. "Scoped" is
// proven by the neighbor's SURVIVAL, not the filter's appearance (the innocent-neighbor red).

// governedSpace is the source address space THIS gateway's policy governs (S8.7 [6]): the WG pool (every
// device source is a /32 inside it) + this gateway's local site subnets (a site-src grant originates from the
// local LAN). A conntrack flow whose ORIGIN src is inside this space is "ours to reconcile" at restart; a
// flow outside it (the gateway's own SSH, an unrelated host) is NEVER swept — the innocent-neighbor
// constraint, which binds HARDEST at restart.
func governedSpace(poolCIDR string, localSubnets []string) []netip.Prefix {
	var out []netip.Prefix
	if p, ok := loosePrefix(poolCIDR); ok {
		out = append(out, p)
	}
	for _, s := range localSubnets {
		if p, ok := loosePrefix(s); ok {
			out = append(out, p)
		}
	}
	return out
}

// shouldReconcileFlush is the RESTART-sweep predicate (S8.7 [6], THE innocent-neighbor guard at restart): a
// flow is swept IFF its origin src is inside our governed space AND the CURRENT policy permits it via NO
// Allow entry. So a grant-covered flow SURVIVES (restart-with-live-legitimate-flows), a flow of a grant
// revoked-while-down DIES (governed but no longer permitted), and an unrelated flow (src outside the governed
// space) is NEVER touched. Pure — the whole safety of the restart sweep lives here.
func shouldReconcileFlush(c conntrack.Con, governed []netip.Prefix, permit []flowTuple) bool {
	src, ok := originSrc(c)
	if !ok || !inGoverned(src, governed) {
		return false // outside our governed source space → an innocent flow, never swept
	}
	for _, t := range permit {
		if matchesTuple(c, t) {
			return false // the current policy still permits it → survives
		}
	}
	return true // governed source, not currently permitted → a leftover from a revoked-while-down grant
}

func originSrc(c conntrack.Con) (netip.Addr, bool) {
	if c.Origin == nil || c.Origin.Src == nil {
		return netip.Addr{}, false
	}
	a, ok := netip.AddrFromSlice(*c.Origin.Src)
	if !ok {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func inGoverned(src netip.Addr, governed []netip.Prefix) bool {
	for _, p := range governed {
		if p.Contains(src) {
			return true
		}
	}
	return false
}

// reconcileFlush is the restart dump-and-reconcile (S8.7 [6]): with no in-memory baseline after a restart,
// dump conntrack and flush every flow shouldReconcileFlush selects — scoped to our governed space, sparing
// grant-covered + unrelated flows. Returns the count killed; per-flow/dump errors surface (never silent).
func reconcileFlush(ctx context.Context, governed []netip.Prefix, allow []nodepolicy.AllowEntry) (int, error) {
	permit := make([]flowTuple, 0, len(allow))
	for _, e := range allow {
		if t, ok := tupleFromAllow(e); ok {
			permit = append(permit, t)
		}
	}
	// Dump only the families our GOVERNED space spans (RE4: a v4-only pool+subnets never dumps IPv6, so a
	// v6-less kernel can't false-degrade the restart sweep — the same scoping flushTuples has).
	return sweepConntrack(ctx, familiesOfPrefixes(governed), func(c conntrack.Con) bool {
		return shouldReconcileFlush(c, governed, permit)
	})
}

// probeConntrack reactively checks CT-netlink capability by opening + closing a conntrack socket. An error
// (e.g. EPERM = CAP_NET_ADMIN absent) IS the capability signal (the reactive form, gap-2/[1] — no proactive
// CapEff read). Used to clear a latched conntrack_flush_unavailable on recovery.
func probeConntrack(_ context.Context) error {
	nfct, err := conntrack.Open(&conntrack.Config{})
	if err != nil {
		return err
	}
	defer nfct.Close()
	// RE2: a REAL capability check — the flush needs Dump, so the probe must EXERCISE it. Open-only was the
	// false-green (a socket opens even when seccomp/ENOBUFS blocks Dump/Delete). One IPv4 dump is cheap enough
	// for the reconcile cadence but fails exactly when the flush would.
	_, err = nfct.Dump(conntrack.Conntrack, conntrack.IPv4)
	return err
}

// flowTuple is a removed grant's EXACT conntrack match spec. A conntrack entry is flushed iff its ORIGIN
// tuple falls inside src AND dst AND (proto unset or equal) AND (ports unset or in range) — never wider.
type flowTuple struct {
	src, dst netip.Prefix
	proto    uint8  // 0 = any L4 (the grant is protocol-agnostic — a site subnet)
	portLow  uint16 // 0 = any port (proto-any or an L3 grant)
	portHigh uint16
	ruleID   string // the revoked grant identity, stamped on the VerdictTerminated event
}

// tupleFromAllow builds the flush spec for a removed AllowEntry. ok=false for a malformed src/dst — NEVER
// flush on a tuple we can't parse (a wide/wrong match is worse than a missed flow).
func tupleFromAllow(e nodepolicy.AllowEntry) (flowTuple, bool) {
	src, ok := loosePrefix(e.SrcIP)
	if !ok {
		return flowTuple{}, false
	}
	dst, ok := loosePrefix(e.DstCIDR)
	if !ok {
		return flowTuple{}, false
	}
	t := flowTuple{src: src, dst: dst, ruleID: e.RuleID}
	switch e.Protocol {
	case "tcp":
		t.proto = 6
	case "udp":
		t.proto = 17
	} // "any" / "" → 0 (match every L4 for this src/dst)
	if e.PortLow > 0 {
		t.portLow, t.portHigh = uint16(e.PortLow), uint16(e.PortHigh)
	}
	return t, true
}

// loosePrefix parses a CIDR ("10.0.0.0/24") or a bare address ("10.99.0.10" → /32, v6 → /128). ok=false on
// garbage.
func loosePrefix(s string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

// matchesTuple reports whether a conntrack entry's ORIGIN tuple falls inside the removed grant's spec — the
// scoped predicate. Every clause must hold; a nil field on the entry fails the match (never flush on missing
// data). This is THE innocent-neighbor guard: a flow with a different src, dst, proto, or dst-port survives.
func matchesTuple(c conntrack.Con, t flowTuple) bool {
	if c.Origin == nil || c.Origin.Src == nil || c.Origin.Dst == nil {
		return false
	}
	src, ok := netip.AddrFromSlice(*c.Origin.Src)
	if !ok {
		return false
	}
	dst, ok := netip.AddrFromSlice(*c.Origin.Dst)
	if !ok {
		return false
	}
	if !t.src.Contains(src.Unmap()) || !t.dst.Contains(dst.Unmap()) {
		return false
	}
	if t.proto != 0 {
		if c.Origin.Proto == nil || c.Origin.Proto.Number == nil || *c.Origin.Proto.Number != t.proto {
			return false
		}
	}
	if t.portHigh != 0 {
		if c.Origin.Proto == nil || c.Origin.Proto.DstPort == nil {
			return false
		}
		if dp := *c.Origin.Proto.DstPort; dp < t.portLow || dp > t.portHigh {
			return false
		}
	}
	return true
}

// flushTuples deletes the conntrack entries matching ANY of the removed tuples, scoped exactly. Returns the
// count killed. A netlink open failure (e.g. CAP_NET_ADMIN absent in some deployment shape) is returned so
// the caller LOGS + SURFACES it (never silent) — the rule removal already succeeded; the lingering flows are
// the pre-existing degraded-not-broken behavior. `emit` is the READY hook for the reserved flow-log seam
// (flowlog.VerdictTerminated + the revoked grant's RuleID); the default caller passes nil — the flush's
// surface is the structured log + Manager.flushErr, and pushing the VerdictTerminated event into the
// flow-log STREAM rides the flowlog-sink threading (S8.7 rider, the buffer is pump-owned, not Manager-held).
// sweepConntrack is THE ONE flush primitive (S8.7 RE4+RE5 reduce): open a CT socket, dump each of `families`,
// and delete every flow `selector` picks — counting kills, SURFACING per-flow delete + per-family dump errors
// via errors.Join (never a silent skip that reports healthy, [7]), and a dump failure for ONE family never
// discards ANOTHER family's kills ([11]). flushTuples (removed-tuple selector) and reconcileFlush (restart
// governed+unmatched selector) are the two callers — the family-scoping/error-handling drift that caused RE4
// is now impossible (one function, one fix surface).
func sweepConntrack(ctx context.Context, families []conntrack.Family, selector func(conntrack.Con) bool) (int, error) {
	if len(families) == 0 {
		return 0, nil
	}
	nfct, err := conntrack.Open(&conntrack.Config{})
	if err != nil {
		return 0, fmt.Errorf("conntrack open (CAP_NET_ADMIN?): %w", err)
	}
	defer nfct.Close()
	killed := 0
	var errs []error
	for _, fam := range families {
		flows, derr := nfct.Dump(conntrack.Conntrack, fam)
		if derr != nil {
			errs = append(errs, fmt.Errorf("conntrack dump %v: %w", fam, derr)) // surfaced; other family's kills stand
			continue
		}
		for i := range flows {
			c := flows[i]
			if !selector(c) {
				continue
			}
			if delErr := nfct.Delete(conntrack.Conntrack, fam, c); delErr != nil {
				errs = append(errs, fmt.Errorf("conntrack delete: %w", delErr)) // [7]: surfaced, never silently skipped
			} else {
				killed++
			}
		}
	}
	return killed, errors.Join(errs...)
}

// flushTuples deletes the conntrack entries matching ANY removed-grant tuple, scoped exactly — a selector over
// the ONE primitive (S8.7 Slice 2). Dumps only the families the tuples span ([11]/[17]).
func flushTuples(ctx context.Context, tuples []flowTuple) (int, error) {
	if len(tuples) == 0 {
		return 0, nil
	}
	return sweepConntrack(ctx, familiesOf(tuples), func(c conntrack.Con) bool {
		for _, t := range tuples {
			if matchesTuple(c, t) {
				return true
			}
		}
		return false
	})
}

// familiesOf returns the conntrack address families the removed tuples span (IPv4 and/or IPv6). The flush
// dumps ONLY these — never a family with no matching tuple ([17]), so a v6-less kernel is never touched for
// an all-v4 removal ([11]).
func familiesOf(tuples []flowTuple) []conntrack.Family {
	ps := make([]netip.Prefix, len(tuples))
	for i, t := range tuples {
		ps[i] = t.src
	}
	return familiesOfPrefixes(ps)
}

// familiesOfPrefixes returns the conntrack families a set of prefixes spans — the ONE family-scoping
// function both the removed-tuple flush and the restart sweep use ([11]/[17]/RE4).
func familiesOfPrefixes(ps []netip.Prefix) []conntrack.Family {
	var v4, v6 bool
	for _, p := range ps {
		if a := p.Addr(); a.Is4() || a.Is4In6() {
			v4 = true
		} else {
			v6 = true
		}
	}
	out := make([]conntrack.Family, 0, 2)
	if v4 {
		out = append(out, conntrack.IPv4)
	}
	if v6 {
		out = append(out, conntrack.IPv6)
	}
	return out
}
