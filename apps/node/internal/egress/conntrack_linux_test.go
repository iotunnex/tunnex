//go:build linux

package egress

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	conntrack "github.com/florianl/go-conntrack"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

func ipp(s string) *net.IP  { p := net.ParseIP(s); return &p }
func u8p(v uint8) *uint8    { return &v }
func u16p(v uint16) *uint16 { return &v }

func con(src, dst string, proto uint8, dport uint16) conntrack.Con {
	return conntrack.Con{Origin: &conntrack.IPTuple{
		Src: ipp(src), Dst: ipp(dst),
		Proto: &conntrack.ProtoTuple{Number: u8p(proto), DstPort: u16p(dport)},
	}}
}

// TestMatchesTupleScoped — the INNOCENT-NEIGHBOR centerpiece (S8.7 Slice 2): the flush filter matches the
// removed grant's EXACT tuple and nothing wider. A flow differing in ANY one dimension (src, dst, proto,
// dst-port) SURVIVES — proven by survival, not by the filter's appearance. One predicate too wide is a
// self-inflicted outage on the busiest gateway.
func TestMatchesTupleScoped(t *testing.T) {
	rt, ok := tupleFromAllow(nodepolicy.AllowEntry{SrcIP: "172.31.17.64/32", DstCIDR: "10.0.0.4/32", Protocol: "tcp", PortLow: 5432, PortHigh: 5432})
	if !ok {
		t.Fatal("tuple parse")
	}
	if !matchesTuple(con("172.31.17.64", "10.0.0.4", 6, 5432), rt) {
		t.Fatal("the EXACT removed tuple must match (get flushed)")
	}
	// Each neighbor differs in ONE dimension → must NOT match → survives the flush.
	survivors := []struct {
		name     string
		src, dst string
		proto    uint8
		dport    uint16
	}{
		{"different src", "172.31.17.65", "10.0.0.4", 6, 5432},
		{"different dst", "172.31.17.64", "10.0.0.5", 6, 5432},
		{"different proto", "172.31.17.64", "10.0.0.4", 17, 5432},
		{"different dst-port", "172.31.17.64", "10.0.0.4", 6, 5433},
		// GAP-3 ruling: orig-tuple-only matching is correct. A flow whose ORIGIN runs the OTHER way (B→A,
		// B-initiated) was authorized by a DIFFERENT grant and must SURVIVE the A→B rule's flush — matching
		// the reply tuple would over-delete, violating innocent-neighbor from the opposite side. (Deleting
		// the A→B flow's conntrack entry already kills BOTH its directions — one entry, orig+reply.)
		{"reply-direction (B-initiated, own grant)", "10.0.0.4", "172.31.17.64", 6, 5432},
	}
	for _, n := range survivors {
		if matchesTuple(con(n.src, n.dst, n.proto, n.dport), rt) {
			t.Fatalf("innocent neighbor (%s) must SURVIVE the scoped flush", n.name)
		}
	}
	// A proto-any / no-port grant (a site subnet source) matches every L4 within its src/dst — but still
	// scoped to THAT src/dst; a different dst survives.
	wide, _ := tupleFromAllow(nodepolicy.AllowEntry{SrcIP: "172.31.0.0/16", DstCIDR: "10.0.0.0/24", Protocol: "any"})
	if !matchesTuple(con("172.31.9.9", "10.0.0.7", 17, 53), wide) {
		t.Fatal("a proto-any grant must match any L4 within its src/dst")
	}
	if matchesTuple(con("172.31.9.9", "10.9.9.9", 17, 53), wide) {
		t.Fatal("a different dst must survive even a proto-any grant")
	}
}

// TestRemovedTuplesDiff — the diff finds grants that LEFT the allow set (expired/deleted), keeps the ones
// that stayed. The kept neighbor is never in the removed set.
func TestRemovedTuplesDiff(t *testing.T) {
	a := nodepolicy.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432, RuleID: "rA"}
	b := nodepolicy.AllowEntry{SrcIP: "172.31.17.64/32", DstCIDR: "10.0.0.4/32", Protocol: "any", RuleID: "rB"}
	removed := removedTuples([]nodepolicy.AllowEntry{a, b}, []nodepolicy.AllowEntry{a}) // b left
	if len(removed) != 1 || removed[0].ruleID != "rB" {
		t.Fatalf("only the removed grant (rB) must be flushed, got %+v", removed)
	}
	// nothing removed → empty (a steady-state reconcile flushes nothing).
	if r := removedTuples([]nodepolicy.AllowEntry{a, b}, []nodepolicy.AllowEntry{a, b}); len(r) != 0 {
		t.Fatalf("an unchanged allow set must flush nothing, got %+v", r)
	}
}

// TestFlushWiringOnRemoval — D5 one-function-two-triggers: a successful enforcing apply that DROPPED a grant
// flushes EXACTLY that grant's tuple (the kept grant is not flushed); the path is identical whether the
// grant left by expiry or by manual delete (the agent only sees an absent entry). Uses the injected ctFlush.
func TestFlushWiringOnRemoval(t *testing.T) {
	var flushed [][]flowTuple
	m := &Manager{
		apply:        func(context.Context, string) error { return nil }, // apply always succeeds
		now:          time.Now,
		startupSwept: true, // steady state: the restart obligation is already discharged; this red is about the ordinary diff flush
		ctFlush: func(_ context.Context, ts []flowTuple) (int, error) {
			flushed = append(flushed, ts)
			return len(ts), nil
		},
	}
	a := nodepolicy.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432, RuleID: "rA"}
	b := nodepolicy.AllowEntry{SrcIP: "172.31.17.64/32", DstCIDR: "10.0.0.4/32", Protocol: "any", RuleID: "rB"}
	enf := func(allow ...nodepolicy.AllowEntry) *nodepolicy.Compiled {
		return &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Allow: allow}
	}
	ctx := context.Background()

	// First apply: allow {A,B}. No prior applied set → nothing removed → no flush.
	if err := m.applyAndTrack(ctx, "ruleset", enf(a, b)); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	m.drainFlush(ctx)
	if len(flushed) != 0 {
		t.Fatalf("the first apply must flush nothing, got %+v", flushed)
	}
	// Second apply: allow {A} — B's grant LEFT (expired or deleted; indistinguishable). Flush EXACTLY B.
	if err := m.applyAndTrack(ctx, "ruleset", enf(a)); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	m.drainFlush(ctx)
	if len(flushed) != 1 || len(flushed[0]) != 1 || flushed[0][0].ruleID != "rB" {
		t.Fatalf("removing B must flush EXACTLY B's tuple (A survives), got %+v", flushed)
	}
}

// TestFlushFailureSurfacedNotSilent — a flush error (e.g. CAP_NET_ADMIN absent, netlink fault) is recorded
// in flushErr (surfaced) and does NOT fail the apply — the rule removal already succeeded; lingering flows
// are degraded-not-broken, never silent.
func TestFlushFailureSurfacedNotSilent(t *testing.T) {
	boom := errors.New("conntrack open (CAP_NET_ADMIN?): operation not permitted")
	m := &Manager{
		apply:        func(context.Context, string) error { return nil },
		now:          time.Now,
		startupSwept: true, // steady state — this red is about an ordinary diff flush's error surfacing, not the restart sweep
		ctFlush:      func(context.Context, []flowTuple) (int, error) { return 0, boom },
	}
	a := nodepolicy.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432, RuleID: "rA"}
	ctx := context.Background()
	if err := m.applyAndTrack(ctx, "ruleset", &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Allow: []nodepolicy.AllowEntry{a}}); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	m.drainFlush(ctx)
	// Drop the grant → a flush is attempted → the flusher errors.
	if err := m.applyAndTrack(ctx, "ruleset", &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing}); err != nil {
		t.Fatalf("apply 2 (rule removal) must SUCCEED despite a flush failure, got %v", err)
	}
	m.drainFlush(ctx)
	m.mu.Lock()
	fe := m.flushErr
	m.mu.Unlock()
	if fe == nil {
		t.Fatal("a flush failure must be SURFACED in flushErr (never silent)")
	}
	// SURFACED on the health plane: the agent reports it via ConntrackFlushFailing → conntrack_flush_unavailable.
	if !m.ConntrackFlushFailing() {
		t.Fatal("a persistent flush failure must be reported via ConntrackFlushFailing (health-plane surface)")
	}
	// RECOVERY: the next successful flush clears it (CAP restored / netlink healthy) → the kind clears.
	m.ctFlush = func(context.Context, []flowTuple) (int, error) { return 1, nil }
	if err := m.applyAndTrack(ctx, "ruleset", &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Allow: []nodepolicy.AllowEntry{a}}); err != nil {
		t.Fatalf("re-add grant: %v", err)
	}
	m.drainFlush(ctx)
	if err := m.applyAndTrack(ctx, "ruleset", &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing}); err != nil {
		t.Fatalf("re-remove grant: %v", err)
	}
	m.drainFlush(ctx)
	if m.ConntrackFlushFailing() {
		t.Fatal("a successful flush must CLEAR the failing state (recovery → kind clears)")
	}
}

// TestFamiliesOf — [11]/[17]: the flush dumps ONLY the families the removed tuples span. An all-v4 removal
// never touches IPv6 (so a v6-less kernel can't false-fail).
func TestFamiliesOf(t *testing.T) {
	v4, _ := tupleFromAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1/32", DstCIDR: "10.0.0.2/32"})
	v6, _ := tupleFromAllow(nodepolicy.AllowEntry{SrcIP: "2001:db8::1/128", DstCIDR: "2001:db8::2/128"})
	if f := familiesOf([]flowTuple{v4}); len(f) != 1 || f[0] != conntrack.IPv4 {
		t.Fatalf("v4-only tuples → [IPv4] only, got %v", f)
	}
	if f := familiesOf([]flowTuple{v6}); len(f) != 1 || f[0] != conntrack.IPv6 {
		t.Fatalf("v6-only tuples → [IPv6] only, got %v", f)
	}
	if f := familiesOf([]flowTuple{v4, v6}); len(f) != 2 {
		t.Fatalf("mixed tuples → both families, got %v", f)
	}
}

// TestFlushLatchClearsOnProbe — [1]: a latched flush-failing state clears on a no-removal drain once the
// capability recovers (a reactive probe), without waiting for an unrelated grant removal.
func TestFlushLatchClearsOnProbe(t *testing.T) {
	var probeErr error = errors.New("still no CAP_NET_ADMIN")
	m := &Manager{now: time.Now, ctProbe: func(context.Context) error { return probeErr }}
	m.mu.Lock()
	m.flushErr = errors.New("a prior flush failed")
	m.mu.Unlock()

	m.drainFlush(context.Background()) // no pending tuples; probe still failing → stays failing
	if !m.ConntrackFlushFailing() {
		t.Fatal("a still-failing probe must KEEP the flush-failing state")
	}
	probeErr = nil // capability recovered
	m.drainFlush(context.Background())
	if m.ConntrackFlushFailing() {
		t.Fatal("[1]: a recovered capability must CLEAR the latched flush-failing state (no removal needed)")
	}
}

// TestRestartSweepPredicate — S8.7 [6] THE restart innocent-neighbor guard: the dump-and-reconcile sweep
// flushes ONLY a flow whose src is in our governed space AND the current policy no longer permits. A
// grant-covered flow survives (restart-with-live-legitimate-flows), a revoked-while-down flow dies, an
// unrelated flow (src outside the governed space) is never touched.
func TestRestartSweepPredicate(t *testing.T) {
	governed := []netip.Prefix{netip.MustParsePrefix("10.99.0.0/24"), netip.MustParsePrefix("172.31.0.0/16")} // wg pool + a local site subnet
	permit := []flowTuple{}
	if t0, ok := tupleFromAllow(nodepolicy.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "any"}); ok {
		permit = append(permit, t0)
	}
	// (1) live legit flow — governed src, still permitted → SURVIVES.
	if shouldReconcileFlush(con("10.99.0.10", "10.0.5.7", 6, 443), governed, permit) {
		t.Fatal("a grant-covered flow must SURVIVE the restart sweep (restart-with-live-legitimate-flows)")
	}
	// (2) revoked-while-down — governed src, NO current permit → FLUSHED.
	if !shouldReconcileFlush(con("10.99.0.11", "10.0.5.7", 6, 443), governed, permit) {
		t.Fatal("a governed flow the current policy denies must be FLUSHED (revoked-while-down)")
	}
	// (3) unrelated — src OUTSIDE the governed space (the gateway's own egress) → NEVER swept.
	if shouldReconcileFlush(con("203.0.113.5", "8.8.8.8", 6, 53), governed, permit) {
		t.Fatal("a flow outside the governed source space must NEVER be swept (the innocent-neighbor constraint)")
	}
}

// TestRestartSweepWiring — [6]: the FIRST enforcing apply after (re)start runs the restart RECONCILE sweep
// (no in-memory baseline), NOT the removed-tuple diff; every subsequent removal uses the normal flush.
func TestRestartSweepWiring(t *testing.T) {
	var reconciled, flushed int
	m := &Manager{
		apply: func(context.Context, string) error { return nil }, now: time.Now, wgIface: "wg0",
		poolSource:  func(context.Context) string { return "10.99.0.1/24" }, // wg0 up: the restart precondition (RE3) is met
		ctReconcile: func(context.Context, []netip.Prefix, []nodepolicy.AllowEntry) (int, error) { reconciled++; return 0, nil },
		ctFlush:     func(context.Context, []flowTuple) (int, error) { flushed++; return 0, nil },
	}
	a := nodepolicy.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "any", RuleID: "rA"}
	enf := func(allow ...nodepolicy.AllowEntry) *nodepolicy.Compiled {
		return &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Allow: allow}
	}
	ctx := context.Background()
	// First enforcing apply → RESTART SWEEP (reconcile), not the diff flush.
	if err := m.applyAndTrack(ctx, "rs", enf(a)); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	m.drainFlush(ctx)
	if reconciled != 1 || flushed != 0 {
		t.Fatalf("[6] the first enforcing apply must run the restart reconcile (not the diff), got reconciled=%d flushed=%d", reconciled, flushed)
	}
	// Subsequent removal → normal flush, reconcile NOT re-run.
	if err := m.applyAndTrack(ctx, "rs", enf()); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	m.drainFlush(ctx)
	if reconciled != 1 || flushed != 1 {
		t.Fatalf("after the restart sweep, a removal must use the normal flush, got reconciled=%d flushed=%d", reconciled, flushed)
	}
}

// TestRestartSweepStateMachine — RE1+RE3 the state model: the restart sweep is a PENDING OBLIGATION with two
// states (pending → discharged), no third. It waits while the precondition is unmet (wg0/pool down, RE3), it
// does NOT discharge on a FAILED sweep (the latch moves on SUCCESS only, RE1 — so a transient netlink fault
// re-attempts next pass instead of silently declaring the restart clean), and it discharges exactly once on a
// verified success. A false-declared-clean restart would leave revoked-while-down flows alive forever.
func TestRestartSweepStateMachine(t *testing.T) {
	var pool string                        // "" = wg0 down (precondition unmet); set = up
	var reconcileErr error                 // toggled per phase
	reconciled := 0
	m := &Manager{
		apply: func(context.Context, string) error { return nil }, now: time.Now,
		poolSource:  func(context.Context) string { return pool },
		ctReconcile: func(context.Context, []netip.Prefix, []nodepolicy.AllowEntry) (int, error) { reconciled++; return 0, reconcileErr },
	}
	enf := &nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Allow: []nodepolicy.AllowEntry{
		{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "any", RuleID: "rA"},
	}}
	ctx := context.Background()
	if err := m.applyAndTrack(ctx, "rs", enf); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Phase 1 — PRECONDITION UNMET (RE3): wg0 down (pool == ""). The obligation WAITS: the sweep must not run
	// blind against unknown governed space, and a brief startup wait must NOT raise health.
	m.drainFlush(ctx)
	if reconciled != 0 {
		t.Fatalf("RE3: a sweep must NOT run before wg0/pool is up (fail-open against unknown governed space), ran %d times", reconciled)
	}
	m.mu.Lock()
	swept, fe := m.startupSwept, m.flushErr
	m.mu.Unlock()
	if swept {
		t.Fatal("RE1: the obligation must stay PENDING while the precondition is unmet (no discharge without a sweep)")
	}
	if fe != nil {
		t.Fatal("RE3: a brief precondition wait must NOT raise health (only pending PAST the threshold degrades)")
	}

	// Phase 2 — PRECONDITION MET but the sweep FAILS (RE1): wg0 up, ctReconcile errors. The sweep runs, the
	// error surfaces, but the obligation must STAY PENDING (the latch moves on success only) so it retries.
	pool = "10.99.0.1/24"
	reconcileErr = errors.New("netlink dump: transient EINTR")
	m.drainFlush(ctx)
	if reconciled != 1 {
		t.Fatalf("RE3: the sweep must RUN once the precondition is met, ran %d times", reconciled)
	}
	m.mu.Lock()
	swept, fe = m.startupSwept, m.flushErr
	m.mu.Unlock()
	if swept {
		t.Fatal("RE1: a FAILED sweep must NOT discharge the obligation (the latch moves on VERIFIED SUCCESS only)")
	}
	if fe == nil {
		t.Fatal("a failed restart sweep must SURFACE its error (never silent)")
	}

	// Phase 3 — the retry SUCCEEDS (RE1): the obligation discharges exactly once, the error clears.
	reconcileErr = nil
	m.drainFlush(ctx)
	if reconciled != 2 {
		t.Fatalf("RE1: a still-pending obligation must RETRY next pass, ran %d times total", reconciled)
	}
	m.mu.Lock()
	swept, fe = m.startupSwept, m.flushErr
	m.mu.Unlock()
	if !swept {
		t.Fatal("RE1: a VERIFIED successful sweep must DISCHARGE the obligation")
	}
	if fe != nil {
		t.Fatal("a successful sweep must CLEAR the surfaced error")
	}

	// Phase 4 — DISCHARGED is terminal (two states, no third): a further drain must NOT re-sweep.
	m.drainFlush(ctx)
	if reconciled != 2 {
		t.Fatalf("discharged is terminal — no re-sweep, ran %d times total", reconciled)
	}
}

// TestFamiliesOfPrefixes — RE4 the SECOND caller: the restart reconcile sweep (governed by netip.Prefix, not
// flowTuple) dumps ONLY the families its governed space spans, exactly like the diff-flush caller (TestFamiliesOf).
// An all-v4 governed space never touches IPv6, so a v6-less kernel can't false-fail the restart sweep either —
// the ONE primitive's family scoping is proven through BOTH callers.
func TestFamiliesOfPrefixes(t *testing.T) {
	v4 := netip.MustParsePrefix("10.99.0.0/24")
	v6 := netip.MustParsePrefix("2001:db8::/64")
	if f := familiesOfPrefixes([]netip.Prefix{v4}); len(f) != 1 || f[0] != conntrack.IPv4 {
		t.Fatalf("v4-only governed space → [IPv4] only, got %v", f)
	}
	if f := familiesOfPrefixes([]netip.Prefix{v6}); len(f) != 1 || f[0] != conntrack.IPv6 {
		t.Fatalf("v6-only governed space → [IPv6] only, got %v", f)
	}
	if f := familiesOfPrefixes([]netip.Prefix{v4, v6}); len(f) != 2 {
		t.Fatalf("mixed governed space → both families, got %v", f)
	}
}
