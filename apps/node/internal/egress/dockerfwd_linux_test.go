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
	chainAbsent  bool              // list chain DOCKER-USER errors (bare-metal / non-Docker host)
	forwardDrop  bool              // `list chain ip filter FORWARD` reports policy drop
	insertErr    bool              // inserts fail (can't place the accept → forwardBlocked path)
	listErr      bool              // the `-a list` enumeration errors (transient nft busy/lock)
	rules        map[string]string // daddr (as nft PRINTS it) -> handle (the agent's tunnex-marked rules)
	nextHandle   int
	inserts      []string // daddr order of inserts (assert scoping)
	deletes      []string // handles deleted
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
		for daddr, h := range f.rules {
			fmt.Fprintf(&b, "    iifname != \"wg0\" oifname \"wg0\" ip daddr %s counter accept comment \"%s\" # handle %s\n", daddr, dockerUserComment, h)
		}
		b.WriteString("  }\n}\n")
		return b.String(), nil
	case len(args) >= 4 && args[0] == "insert" && args[1] == "rule":
		if f.insertErr {
			return "", errors.New("insert denied")
		}
		daddr := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "daddr" {
				daddr = args[i+1]
			}
		}
		daddr = strings.TrimSuffix(daddr, "/32") // model nft: a host daddr is stored/printed BARE
		f.nextHandle++
		f.rules[daddr] = fmt.Sprint(f.nextHandle)
		f.inserts = append(f.inserts, daddr)
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
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24", "172.31.0.0/16"})
	if len(f.rules) != 2 || f.rules["10.0.0.0/24"] == "" || f.rules["172.31.0.0/16"] == "" {
		t.Fatalf("expected one scoped accept per route, got %v", f.rules)
	}
}

// TestDockerForwardIdempotent — D-WF4-a: a second reconcile with the same routes inserts NOTHING
// (list → insert-only-missing), so a per-tick loop doesn't churn.
func TestDockerForwardIdempotent(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	routes := []string{"10.0.0.0/24"}
	m.reconcileDockerForward(context.Background(), routes)
	n := len(f.inserts)
	m.reconcileDockerForward(context.Background(), routes)
	if len(f.inserts) != n {
		t.Fatalf("second reconcile must insert nothing (idempotent); inserts went %d -> %d", n, len(f.inserts))
	}
}

// TestDockerForwardFullSweep — D-WF4-b: a route withdrawn removes its comment-marked DOCKER-USER
// rule (by handle), never leaving a stale foreign-chain accept.
func TestDockerForwardFullSweep(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24", "172.31.0.0/16"})
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}) // 172.31 withdrawn
	if _, still := f.rules["172.31.0.0/16"]; still {
		t.Fatalf("a withdrawn route's DOCKER-USER rule must be swept, still present: %v", f.rules)
	}
	if _, kept := f.rules["10.0.0.0/24"]; !kept {
		t.Fatalf("the surviving route's rule must stay, got %v", f.rules)
	}
	if len(f.deletes) != 1 {
		t.Fatalf("exactly the stale rule must be deleted by handle, deletes=%v", f.deletes)
	}
}

// TestDockerForwardHostRouteIdempotent — re-review #1: a /32 route must NOT thrash. nft prints a host
// daddr BARE (no /32), so keying on Masked() "x/32" would never match the listed "x" → perpetual
// insert+delete. canonDaddr keys both sides bare, so a second reconcile inserts nothing.
func TestDockerForwardHostRouteIdempotent(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	routes := []string{"10.0.0.5/32"}
	m.reconcileDockerForward(context.Background(), routes)
	n := len(f.inserts)
	if n != 1 {
		t.Fatalf("first reconcile inserts the /32 accept once, got %d", n)
	}
	m.reconcileDockerForward(context.Background(), routes)
	if len(f.inserts) != n || len(f.deletes) != 0 {
		t.Fatalf("a /32 route must be idempotent (no churn); inserts %d→%d, deletes %d", n, len(f.inserts), len(f.deletes))
	}
}

// TestDockerForwardListErrorSkips — re-review #2: a transient `-a list` failure must NOT blind-insert
// (which duplicates accepts the sweep can't reap). On a list error the reconcile skips add/sweep.
func TestDockerForwardListErrorSkips(t *testing.T) {
	f := newFakeNft()
	m := mgrWithNft(f)
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}) // places one
	before := len(f.inserts)
	f.listErr = true
	m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}) // list fails → must NOT re-insert
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
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}); blocked {
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
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}); !blocked {
		t.Fatal("Docker FORWARD-drop + unplaceable accept + routes present → must report forwardBlocked")
	}
	if !m.ForwardBlocked() {
		t.Fatal("ForwardBlocked() must be true when the forward is Docker-blocked")
	}
	// Recovery: inserts succeed → not blocked.
	f.insertErr = false
	if blocked := m.reconcileDockerForward(context.Background(), []string{"10.0.0.0/24"}); blocked {
		t.Fatal("once the accept is placed, forwardBlocked must clear")
	}
}
