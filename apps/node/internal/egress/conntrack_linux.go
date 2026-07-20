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

// probeConntrack reactively checks CT-netlink capability by opening + closing a conntrack socket. An error
// (e.g. EPERM = CAP_NET_ADMIN absent) IS the capability signal (the reactive form, gap-2/[1] — no proactive
// CapEff read). Used to clear a latched conntrack_flush_unavailable on recovery.
func probeConntrack(_ context.Context) error {
	nfct, err := conntrack.Open(&conntrack.Config{})
	if err != nil {
		return err
	}
	return nfct.Close()
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
func flushTuples(ctx context.Context, tuples []flowTuple, emit func(ruleID string)) (int, error) {
	if len(tuples) == 0 {
		return 0, nil
	}
	nfct, err := conntrack.Open(&conntrack.Config{})
	if err != nil {
		return 0, fmt.Errorf("conntrack open (CAP_NET_ADMIN?): %w", err)
	}
	defer nfct.Close()

	killed := 0
	var errs []error
	// Dump ONLY the address families the removed tuples actually span ([17]) — an IPv4-only removal never
	// dumps IPv6, so it can't false-fail on a kernel with no v6 conntrack path ([11]).
	for _, fam := range familiesOf(tuples) {
		flows, derr := nfct.Dump(conntrack.Conntrack, fam)
		if derr != nil {
			errs = append(errs, fmt.Errorf("conntrack dump %v: %w", fam, derr)) // surfaced, but the OTHER family's kills stand ([11])
			continue
		}
		for i := range flows {
			c := flows[i]
			for _, t := range tuples {
				if matchesTuple(c, t) {
					if delErr := nfct.Delete(conntrack.Conntrack, fam, c); delErr != nil {
						// [7]: a per-flow delete failure is SURFACED, never silently skipped — the revoked flow
						// survives, so the caller must raise conntrack_flush_unavailable, not report healthy.
						errs = append(errs, fmt.Errorf("conntrack delete (rule %s): %w", t.ruleID, delErr))
					} else {
						killed++
						if emit != nil {
							emit(t.ruleID)
						}
					}
					break // one tuple match is enough; don't double-count/double-delete a flow
				}
			}
		}
	}
	return killed, errors.Join(errs...)
}

// familiesOf returns the conntrack address families the removed tuples span (IPv4 and/or IPv6). The flush
// dumps ONLY these — never a family with no matching tuple ([17]), so a v6-less kernel is never touched for
// an all-v4 removal ([11]).
func familiesOf(tuples []flowTuple) []conntrack.Family {
	var v4, v6 bool
	for _, t := range tuples {
		if a := t.src.Addr(); a.Is4() || a.Is4In6() {
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
