//go:build linux

package egress

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeNft models the DOCKER-USER + FORWARD chains for the WF-4 reconcile. It tracks the
// agent's comment-marked accept rules (daddr -> handle) so idempotence + full-sweep are testable.
type fakeNft struct {
	chainAbsent bool              // list chain DOCKER-USER errors (bare-metal / non-Docker host)
	forwardDrop bool              // `list chain ip filter FORWARD` reports policy drop
	insertErr   bool              // inserts fail (can't place the accept → forwardBlocked path)
	listErr     bool              // the `-a list` enumeration errors (transient nft busy/lock)
	rules       map[string]string // daddr (as nft PRINTS it) -> handle (the agent's tunnex-marked rules)
	nextHandle  int
	inserts     []string   // daddr order of inserts (assert scoping)
	insertArgs  [][]string // full arg vector per insert (assert iif/oif ORIENTATION — WF-4-local)
	deletes     []string   // handles deleted
}

func newFakeNft() *fakeNft { return &fakeNft{rules: map[string]string{}, nextHandle: 10} }

func (f *fakeNft) run(_ context.Context, args ...string) (string, error) {
	cmd := strings.Join(args, " ")
	switch {
	case cmd == "list chain ip filter DOCKER-USER":
		if f.chainAbsent {
			return "", errors.New("No such file or directory")
		}
		return "table ip filter { chain DOCKER-USER { } }", nil
	case cmd == "list chain ip filter FORWARD":
		if f.forwardDrop {
			return "chain FORWARD { type filter hook forward priority filter; policy drop; }", nil
		}
		return "chain FORWARD { type filter hook forward priority filter; policy accept; }", nil
	case cmd == "-a list chain ip filter DOCKER-USER":
		if f.listErr {
			return "", errors.New("nft busy: resource temporarily unavailable")
		}
		var b strings.Builder
		b.WriteString("table ip filter {\n  chain DOCKER-USER {\n")
		for key, h := range f.rules { // key = "d:addr" (forward) or "s:addr" (return)
			addr := key[2:]
			if key[0] == 'd' {
				fmt.Fprintf(&b, "    iifname != \"wg0\" oifname \"wg0\" ip daddr %s counter accept comment \"%s\" # handle %s\n", addr, dockerUserComment, h)
			} else {
				fmt.Fprintf(&b, "    iifname \"wg0\" oifname != \"wg0\" ip saddr %s counter accept comment \"%s\" # handle %s\n", addr, dockerUserComment, h)
			}
		}
		b.WriteString("  }\n}\n")
		return b.String(), nil
	case len(args) >= 4 && args[0] == "insert" && args[1] == "rule":
		if f.insertErr {
			return "", errors.New("insert denied")
		}
		dir, addr := "", ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "daddr" {
				dir, addr = "d", args[i+1]
			} else if args[i] == "saddr" {
				dir, addr = "s", args[i+1]
			}
		}
		addr = strings.TrimSuffix(addr, "/32") // model nft: a host addr is stored/printed BARE
		key := dir + ":" + addr
		f.nextHandle++
		f.rules[key] = fmt.Sprint(f.nextHandle)
		f.inserts = append(f.inserts, key)
		f.insertArgs = append(f.insertArgs, append([]string(nil), args...))
		return "", nil
	case len(args) >= 2 && args[0] == "delete" && args[1] == "rule":
		handle := args[len(args)-1]
		for daddr, h := range f.rules {
			if h == handle {
				delete(f.rules, daddr)
			}
		}
		f.deletes = append(f.deletes, handle)
		return "", nil
	}
	return "", nil
}

func mgrWithNft(f *fakeNft) *Manager {
	m := New("wg0")
	m.nftRun = f.run
	return m
}

// TestDockerForwardScopedInsert — WF-4 D-WF4-b: on a Docker host, the agent inserts a Routes-SCOPED
// accept into DOCKER-USER (one per v4 route, comment-marked), never a blanket accept.
func TestDockerForwardScopedInsert(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24", "172.31.0.0/16"}, nil)
	// TWO Route-scoped accepts per route: forward (d:) + return (s:) — the return path is why the re-walk
	// forward-ping passed but the reply dropped.
	for _, k := range []string{"d:10.0.0.0/24", "s:10.0.0.0/24", "d:172.31.0.0/16", "s:172.31.0.0/16"} {
		if f.rules[k] == "" {
			t.Fatalf("missing scoped accept %s; got %v", k, f.rules)
		}
	}
	if len(f.rules) != 4 {
		t.Fatalf("expected 4 rules (fwd+ret per route), got %v", f.rules)
	}
}

// TestDockerForwardIdempotent — D-WF4-a: a second reconcile with the same routes inserts NOTHING
// (list → insert-only-missing), so a per-tick loop doesn't churn.
func TestDockerForwardIdempotent(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	routes := []string{"10.0.0.0/24"}
	m.reconcileDockerForward(context.Background(), routes, nil)
	n := len(f.inserts)
	m.reconcileDockerForward(context.Background(), routes, nil)
	if len(f.inserts) != n {
		t.Fatalf("second reconcile must insert nothing (idempotent); inserts went %d -> %d", n, len(f.inserts))
	}
}

// TestDockerForwardFullSweep — D-WF4-b: a route withdrawn removes its comment-marked DOCKER-USER
// rule (by handle), never leaving a stale foreign-chain accept.
func TestDockerForwardFullSweep(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24", "172.31.0.0/16"}, nil)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil) // 172.31 withdrawn
	for _, k := range []string{"d:172.31.0.0/16", "s:172.31.0.0/16"} {
		if _, still := f.rules[k]; still {
			t.Fatalf("a withdrawn route's rule %s must be swept, still present: %v", k, f.rules)
		}
	}
	for _, k := range []string{"d:10.0.0.0/24", "s:10.0.0.0/24"} {
		if _, kept := f.rules[k]; !kept {
			t.Fatalf("the surviving route's rule %s must stay, got %v", k, f.rules)
		}
	}
	if len(f.deletes) != 2 { // both directions of the withdrawn route
		t.Fatalf("exactly the stale route's two rules must be deleted, deletes=%v", f.deletes)
	}
}

// TestDockerForwardHostRouteIdempotent — re-review #1: a /32 route must NOT thrash. nft prints a host
// daddr BARE (no /32), so keying on Masked() "x/32" would never match the listed "x" → perpetual
// insert+delete. canonDaddr keys both sides bare, so a second reconcile inserts nothing.
func TestDockerForwardHostRouteIdempotent(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	routes := []string{"10.0.0.5/32"}
	m.reconcileDockerForward(context.Background(), routes, nil)
	n := len(f.inserts)
	if n != 2 { // fwd + ret for the one /32
		t.Fatalf("first reconcile inserts the /32 fwd+ret accepts, got %d", n)
	}
	m.reconcileDockerForward(context.Background(), routes, nil)
	if len(f.inserts) != n || len(f.deletes) != 0 {
		t.Fatalf("a /32 route must be idempotent (no churn); inserts %d→%d, deletes %d", n, len(f.inserts), len(f.deletes))
	}
}

// TestDockerForwardListErrorSkips — re-review #2: a transient `-a list` failure must NOT blind-insert
// (which duplicates accepts the sweep can't reap). On a list error the reconcile skips add/sweep.
func TestDockerForwardListErrorSkips(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil) // places one
	before := len(f.inserts)
	f.listErr = true
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil) // list fails → must NOT re-insert
	if len(f.inserts) != before {
		t.Fatalf("a transient list error must skip inserts (no duplicates); inserts %d→%d", before, len(f.inserts))
	}
}

// TestDockerForwardBareMetalNoOp — D-WF4-c: no DOCKER-USER chain (bare metal / non-Docker) → no-op,
// no error, forwardBlocked stays false (forwarding rides the host's own FORWARD).
func TestDockerForwardBareMetalNoOp(t *testing.T) {
	f := newFakeNft()
	f.chainAbsent = true
	m := mgrWithNft(f)
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil); blocked {
		t.Fatal("bare-metal (no DOCKER-USER) must not report forwardBlocked")
	}
	if len(f.inserts) != 0 {
		t.Fatalf("bare-metal must not touch any foreign chain, inserts=%v", f.inserts)
	}
	if m.ForwardBlocked() {
		t.Fatal("ForwardBlocked() must be false on a non-Docker host")
	}
}

// TestDockerForwardBlockedSignal — D-WF4-d: Docker host + FORWARD policy-drop + routes to carry +
// the accept CAN'T be placed → forwardBlocked TRUE (surfaced as site_subnet_unreachable, never green).
func TestDockerForwardBlockedSignal(t *testing.T) {
	f := newFakeNft()
	f.forwardDrop = true
	f.insertErr = true
	m := mgrWithNft(f)
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil); !blocked {
		t.Fatal("Docker FORWARD-drop + unplaceable accept + routes present → must report forwardBlocked")
	}
	if !m.ForwardBlocked() {
		t.Fatal("ForwardBlocked() must be true when the forward is Docker-blocked")
	}
	// Recovery: inserts succeed → not blocked.
	f.insertErr = false
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, nil); blocked {
		t.Fatal("once the accept is placed, forwardBlocked must clear")
	}
}

// hasArgSeq reports whether args contains seq as a contiguous subsequence.
func hasArgSeq(args, seq []string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j := range seq {
			if args[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// findInsertWith returns the first recorded insert arg-vector whose (dirTok, addr) pair matches.
func findInsertWith(f *fakeNft, dirTok, addr string) []string {
	for _, a := range f.insertArgs {
		for i := 0; i+1 < len(a); i++ {
			if a[i] == dirTok && a[i+1] == addr {
				return a
			}
		}
	}
	return nil
}

// TestDockerForwardLocalSubnetMirrored — WF-4-LOCAL (S8.5), the walk fixture as a red: a split-tunnel device
// reaching the LAN BEHIND its own gateway is forwarded wg0→eth0; Docker's FORWARD DROP swallowed it even
// though the ZT chain accepted it (wire-proven). The fix opens a DOCKER-USER accept for the gateway's OWN
// advertised subnets too — but in the MIRRORED orientation vs a remote route. A remote route is a behind-LAN
// host initiating OUT to the site-link (iif!=wg0 → oif=wg0, daddr); a local subnet is a DEVICE initiating IN
// to the local LAN (iif=wg0 → oif!=wg0, daddr) — the mirror. A wrong (route) orientation would leave the
// device→own-LAN forward dropped exactly as before the fix. BOTH faces asserted: (a) Docker's structural drop
// opened in the RIGHT direction; (b) the ZT enforcement chain (`ip tunnex`) is NEVER touched here — this lifts
// only Docker's isolation, so the grant still adjudicates.
func TestDockerForwardLocalSubnetMirrored(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	// a REMOTE route 10.0.0.0/24 (site-to-site) + this gateway's OWN advertised subnet 172.31.0.0/16.
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}, []string{"172.31.0.0/16"})

	// Both get fwd (d:) + ret (s:) accepts — 4 rules, disjoint addrs, no key collision across orientations.
	for _, k := range []string{"d:10.0.0.0/24", "s:10.0.0.0/24", "d:172.31.0.0/16", "s:172.31.0.0/16"} {
		if f.rules[k] == "" {
			t.Fatalf("missing accept %s; got %v", k, f.rules)
		}
	}

	// ORIENTATION — the crux. Route FORWARD (daddr=route) = LAN→remote: iif!=wg0 → oif=wg0.
	if fwd := findInsertWith(f, "daddr", "10.0.0.0/24"); !hasArgSeq(fwd, []string{"iifname", "!=", "wg0", "oifname", "wg0"}) {
		t.Fatalf("route forward must be iif!=wg0 → oif=wg0 (LAN→remote), got %v", fwd)
	}
	// LOCAL-SUBNET FORWARD (daddr=localsubnet) = device→own-LAN: the MIRROR iif=wg0 → oif!=wg0.
	if fwd := findInsertWith(f, "daddr", "172.31.0.0/16"); !hasArgSeq(fwd, []string{"iifname", "wg0", "oifname", "!=", "wg0"}) {
		t.Fatalf("WF-4-local: local-subnet forward must be MIRRORED iif=wg0 → oif!=wg0 (device→own-LAN), got %v", fwd)
	}
	// LOCAL-SUBNET RETURN (saddr=localsubnet) = own-LAN→device: iif!=wg0 → oif=wg0.
	if ret := findInsertWith(f, "saddr", "172.31.0.0/16"); !hasArgSeq(ret, []string{"iifname", "!=", "wg0", "oifname", "wg0"}) {
		t.Fatalf("WF-4-local: local-subnet return must be iif!=wg0 → oif=wg0 (own-LAN→device), got %v", ret)
	}

	// SECOND FACE: this reconcile touches ONLY DOCKER-USER (Docker's structural drop), NEVER the `ip tunnex`
	// ZT enforcement chain — the grant still adjudicates. No insert may target the tunnex table.
	for _, a := range f.insertArgs {
		if hasArgSeq(a, []string{"ip", "tunnex"}) {
			t.Fatalf("reconcileDockerForward must not touch the ZT enforcement chain, got %v", a)
		}
	}
}
