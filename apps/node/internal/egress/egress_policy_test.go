//go:build linux

package egress

import (
	"context"
	"errors"
	"strings"
	"time"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// Fail-closed + staleness (decisions 4b / #1): a FAILED apply must NOT advance the
// applied status — the kernel keeps the previous ruleset in force, so applied stays at
// the last-good version while desired moves ahead (STALE, and surfaced via applyErr).
// The applier is injected so this is provable without a real nft/kernel; the kernel's
// transactional rollback itself is a box proof (runbook #7).
func TestApplyFailureLeavesAppliedStale(t *testing.T) {
	m := New("wg0")
	m.apply = func(context.Context, string) error { return nil } // inject: nft not present in CI unit env

	// First: a good apply of policy version 1 records applied=1 + the CANONICAL policy
	// hash (nodepolicy.CanonicalHash — the same bytes the control plane hashes; NEVER
	// the ruleset text, which carries node-local subnet state).
	v1 := &nodepolicy.Compiled{Version: 1, Mode: nodepolicy.ModeEnforcing, Mesh: false}
	m.SetPolicy(v1)
	if err := m.applyAndTrack(context.Background(), m.ruleset(""), v1); err != nil {
		t.Fatalf("good apply: %v", err)
	}
	if v, h, fs, e := m.AppliedStatus(); v != 1 || h != nodepolicy.CanonicalHash(v1) || e != nil || !fs.IsZero() {
		t.Fatalf("after good apply want v=1, canonical hash, nil err; got v=%d h=%q e=%v", v, h, e)
	}
	goodHash := nodepolicy.CanonicalHash(v1)

	// Now: desired advances to version 2, but the apply FAILS (bad ruleset / no nft).
	boom := errors.New("nft apply: rejected")
	m.apply = func(context.Context, string) error { return boom }
	v2 := &nodepolicy.Compiled{Version: 2, Mode: nodepolicy.ModeEnforcing, Mesh: false}
	m.SetPolicy(v2)
	if err := m.applyAndTrack(context.Background(), m.ruleset(""), v2); !errors.Is(err, boom) {
		t.Fatalf("failed apply must return the error; got %v", err)
	}
	v, h, fs, e := m.AppliedStatus()
	_ = fs
	if v != 1 || h != goodHash {
		t.Fatalf("applied must stay at the last-good (v=1) on failure; got v=%d h=%q", v, h)
	}
	if !errors.Is(e, boom) {
		t.Fatalf("apply error must be surfaced for staleness; got %v", e)
	}
	// desired (2) != applied (1) => STALE, visible to the control plane.
	if m.desiredVersion() == v {
		t.Fatal("desired should have advanced past applied (stale not detectable)")
	}
}

const blanketV4 = `iifname "wg0" oifname "wg0" counter accept`

// Mesh / nil policy renders the LEGACY blanket mesh — no behavior change when Zero
// Trust is off (or in the open build, which never sets a policy).
func TestRulesetMeshIsBlanket(t *testing.T) {
	for _, pol := range []*nodepolicy.Compiled{nil, {Mode: nodepolicy.ModeOff, Mesh: true}} {
		m := New("wg0")
		m.SetPolicy(pol)
		rs := m.ruleset("10.99.0.1/24")
		if !strings.Contains(rs, blanketV4) {
			t.Fatalf("mesh must keep the wg0<->wg0 blanket accept; got:\n%s", rs)
		}
		if !strings.Contains(rs, `iifname "wg0" oifname != "wg0" counter accept`) {
			t.Fatalf("mesh must keep the egress blanket accept; got:\n%s", rs)
		}
	}
}

// Enforcing renders DEFAULT-DENY: the compiled allows, in order, and CRUCIALLY no
// wg0<->wg0 blanket accept anywhere (the S7.1 structural guard, now on the wire — 3c).
func TestRulesetEnforcingDefaultDenyNoBlanket(t *testing.T) {
	m := New("wg0")
	m.SetPolicy(&nodepolicy.Compiled{
		Mode: nodepolicy.ModeEnforcing, Mesh: false,
		Allow: []nodepolicy.AllowEntry{
			{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432},
			{SrcIP: "10.99.0.10", DstCIDR: "10.99.0.20/32", Protocol: "any"},
		},
	})
	rs := m.ruleset("10.99.0.1/24")

	if strings.Contains(rs, blanketV4) {
		t.Fatalf("enforcing must NOT emit the wg0<->wg0 blanket accept; got:\n%s", rs)
	}
	if !strings.Contains(rs, "policy drop") {
		t.Fatal("enforcing must keep the forward policy drop base")
	}
	if !strings.Contains(rs, "ip saddr 10.99.0.10 ip daddr 10.0.5.0/24 tcp dport 5432 counter accept") {
		t.Fatalf("missing the resource allow; got:\n%s", rs)
	}
	if !strings.Contains(rs, "ip saddr 10.99.0.10 ip daddr 10.99.0.20/32 counter accept") {
		t.Fatalf("missing the device-to-device allow; got:\n%s", rs)
	}
	// The masquerade still NATs allowed egress.
	if !strings.Contains(rs, "masquerade") {
		t.Fatal("masquerade must remain for allowed egress")
	}
}

// Enforcing with ZERO allows = deny-all: drop base, no allows, no blanket (empty !=
// permissive, on the wire).
func TestRulesetEnforcingEmptyIsDenyAll(t *testing.T) {
	m := New("wg0")
	m.SetPolicy(&nodepolicy.Compiled{Mode: nodepolicy.ModeEnforcing, Mesh: false})
	rs := m.ruleset("10.99.0.1/24")
	if strings.Contains(rs, blanketV4) {
		t.Fatal("empty enforcing must not be permissive (no blanket)")
	}
	if !strings.Contains(rs, "policy drop") {
		t.Fatal("empty enforcing must still drop by default")
	}
	if strings.Contains(rs, "ip daddr") { // allow rules carry `ip daddr`; the masquerade (ip saddr) does not
		t.Fatalf("empty enforcing must emit no allow rules; got:\n%s", rs)
	}
}

// Idempotence (4c): the same Compiled renders byte-identical ruleset text, so a
// steady-state reconcile is a no-op.
func TestRulesetDeterministic(t *testing.T) {
	pol := &nodepolicy.Compiled{
		Mode: nodepolicy.ModeEnforcing, Mesh: false,
		Allow: []nodepolicy.AllowEntry{
			{SrcIP: "10.99.0.10", DstCIDR: "0.0.0.0/0", Protocol: "any"},
			{SrcIP: "10.99.0.11", DstCIDR: "10.0.5.0/24", Protocol: "udp", PortLow: 53, PortHigh: 53},
		},
	}
	m := New("wg0")
	m.SetPolicy(pol)
	a := m.ruleset("10.99.0.1/24")
	b := m.ruleset("10.99.0.1/24")
	if a != b {
		t.Fatal("ruleset must be deterministic for equal input")
	}
	// Order is preserved from the (already-sorted) compiler output.
	iA := strings.Index(a, "10.99.0.10")
	iB := strings.Index(a, "10.99.0.11")
	if iA < 0 || iB < 0 || iA > iB {
		t.Fatalf("allow order not preserved:\n%s", a)
	}
}

// renderAllow re-emits every field through netip (canonical numeric) so nothing can
// inject nft statements, and skips what it can't safely render.
func TestRenderAllowSanitizesAndSkips(t *testing.T) {
	// Injection attempt in SrcIP -> skipped (not parseable as an IP).
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1; add rule ip tunnex forward accept", DstCIDR: "10.0.0.0/24"}); ok {
		t.Fatal("a non-IP SrcIP (injection) must be skipped")
	}
	// v6 destination -> skipped (v4 spokes; v6 stays default-deny).
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "2001:db8::/32"}); ok {
		t.Fatal("a v6 destination must be skipped")
	}
	// Bad CIDR -> skipped.
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "not-a-cidr"}); ok {
		t.Fatal("a malformed CIDR must be skipped")
	}
	// Valid any-proto egress grant.
	line, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.99.0.7", DstCIDR: "0.0.0.0/0", Protocol: "any"})
	if !ok || !strings.Contains(line, "ip saddr 10.99.0.7 ip daddr 0.0.0.0/0 counter accept") {
		t.Fatalf("valid egress grant mis-rendered: %q", line)
	}
	// Host bits in dst are masked (defense-in-depth; the service already canonicalizes).
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.5/24", Protocol: "any"})
	if !strings.Contains(line, "ip daddr 10.0.5.0/24") {
		t.Fatalf("dst not canonicalized: %q", line)
	}
	// tcp with no ports -> ip protocol clause.
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp"})
	if !strings.Contains(line, "ip protocol tcp counter accept") {
		t.Fatalf("tcp-no-ports mis-rendered: %q", line)
	}
	// port range.
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 8000, PortHigh: 9000})
	if !strings.Contains(line, "tcp dport 8000-9000 counter accept") {
		t.Fatalf("port range mis-rendered: %q", line)
	}
}

// failingSince (finding #3) is the mismatch ONSET: zero while apply is healthy, set
// on the FIRST failure, cleared on the next success. This is what the control plane's
// stale window measures — so a normal push that applies never registers stale.
func TestFailingSinceMismatchOnset(t *testing.T) {
	m := New("wg0")
	tick := time.Unix(1000, 0)
	m.now = func() time.Time { return tick }

	// Healthy apply -> no failingSince (a normal push is never stale).
	m.apply = func(context.Context, string) error { return nil }
	m.SetPolicy(&nodepolicy.Compiled{Version: 1, Mode: nodepolicy.ModeEnforcing})
	if err := m.applyAndTrack(context.Background(), m.ruleset(""), m.policy.Load()); err != nil {
		t.Fatal(err)
	}
	if _, _, fs, _ := m.AppliedStatus(); !fs.IsZero() {
		t.Fatalf("healthy apply must leave failingSince zero, got %v", fs)
	}

	// First failure stamps the onset.
	m.apply = func(context.Context, string) error { return errFail }
	tick = time.Unix(2000, 0)
	m.SetPolicy(&nodepolicy.Compiled{Version: 2, Mode: nodepolicy.ModeEnforcing})
	_ = m.applyAndTrack(context.Background(), m.ruleset(""), m.policy.Load())
	_, _, fs1, _ := m.AppliedStatus()
	if !fs1.Equal(time.Unix(2000, 0)) {
		t.Fatalf("first failure must stamp onset=2000, got %v", fs1)
	}
	// A SECOND failure does NOT move the onset (measures duration from first fail).
	tick = time.Unix(2050, 0)
	_ = m.applyAndTrack(context.Background(), m.ruleset(""), m.policy.Load())
	if _, _, fs2, _ := m.AppliedStatus(); !fs2.Equal(time.Unix(2000, 0)) {
		t.Fatalf("onset must not advance on repeated failure, got %v", fs2)
	}
	// Recovery clears it.
	m.apply = func(context.Context, string) error { return nil }
	if err := m.applyAndTrack(context.Background(), m.ruleset(""), m.policy.Load()); err != nil {
		t.Fatal(err)
	}
	if _, _, fs3, e := m.AppliedStatus(); !fs3.IsZero() || e != nil {
		t.Fatalf("recovery must clear failingSince, got fs=%v err=%v", fs3, e)
	}
}

var errFail = fmtErr("nft apply: rejected")

type fmtErr string

func (e fmtErr) Error() string { return string(e) }

// Finding #2 (three states): a Manager that has NEVER received a policy renders
// DENY-ALL (drop + ct only), NOT the blanket mesh — fail-closed cold start. This is
// distinct from nil-in-received (= mesh, asserted by TestRulesetMeshIsBlanket, which
// calls SetPolicy). The absent=Mesh contract is thus SPLIT: nil-in-received=mesh,
// never-received=deny.
func TestNeverReceivedIsDenyAllNotMesh(t *testing.T) {
	m := New("wg0") // no SetPolicy call -> policyReceived is false
	rs := m.ruleset("10.99.0.1/24")
	if strings.Contains(rs, blanketV4) {
		t.Fatalf("cold start (no policy received) must NOT render the blanket mesh; got:\n%s", rs)
	}
	if !strings.Contains(rs, "policy drop") || !strings.Contains(rs, "tunnex_default_drop") {
		t.Fatal("cold start must be deny-all (policy drop + default_drop counter)")
	}
	if strings.Contains(rs, "ip daddr") {
		t.Fatal("cold start must emit no allow rules")
	}
	// Once a nil policy is RECEIVED (an off org's first fetch), it becomes the mesh.
	m.SetPolicy(nil)
	if !strings.Contains(m.ruleset("10.99.0.1/24"), blanketV4) {
		t.Fatal("nil-in-received must render the blanket mesh")
	}
}

// Finding #1: a half-set / inverted port range fails CLOSED (rule skipped), never
// widening to all-ports.
func TestRenderAllowHalfSetPortRangeFailsClosed(t *testing.T) {
	// only port_high set (port_low == 0) -> the old bug emitted `ip protocol tcp` (all ports).
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortHigh: 443}); ok {
		t.Fatal("port_high without port_low must be SKIPPED (fail-closed), not widened to all-ports")
	}
	// only port_low set -> also skipped.
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 443}); ok {
		t.Fatal("port_low without port_high must be skipped")
	}
	// inverted range -> skipped.
	if _, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 500, PortHigh: 400}); ok {
		t.Fatal("inverted range must be skipped")
	}
	// both unset -> any-port (valid).
	if line, ok := renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp"}); !ok || !strings.Contains(line, "ip protocol tcp") {
		t.Fatalf("both-unset must be any-port, got %q ok=%v", line, ok)
	}
}

// Finding #6: an apply failure while the policy is NOT enforcing (mesh/nil/off/open)
// is an egress-arm problem, NOT policy staleness -> must not set failingSince/applyErr.
func TestNonEnforcingApplyFailureIsNotPolicyStale(t *testing.T) {
	m := New("wg0")
	m.apply = func(context.Context, string) error { return errFail }
	// mesh policy (off org) whose nft apply fails:
	mesh := &nodepolicy.Compiled{Mode: nodepolicy.ModeOff, Mesh: true}
	m.SetPolicy(mesh)
	_ = m.applyAndTrack(context.Background(), m.ruleset(""), mesh)
	if _, _, fs, e := m.AppliedStatus(); !fs.IsZero() || e != nil {
		t.Fatalf("non-enforcing apply failure must NOT set policy failingSince/err; got fs=%v e=%v", fs, e)
	}
	// nil policy (open build) apply failure: same.
	m.SetPolicy(nil)
	_ = m.applyAndTrack(context.Background(), m.ruleset(""), nil)
	if _, _, fs, e := m.AppliedStatus(); !fs.IsZero() || e != nil {
		t.Fatalf("nil-policy apply failure must NOT set policy failingSince/err; got fs=%v e=%v", fs, e)
	}
}
