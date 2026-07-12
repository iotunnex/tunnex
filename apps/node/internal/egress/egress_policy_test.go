//go:build linux

package egress

import (
	"strings"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

const blanketV4 = `iifname "wg0" oifname "wg0" accept`

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
		if !strings.Contains(rs, `iifname "wg0" oifname != "wg0" accept`) {
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
	if !strings.Contains(rs, "ip saddr 10.99.0.10 ip daddr 10.0.5.0/24 tcp dport 5432 accept") {
		t.Fatalf("missing the resource allow; got:\n%s", rs)
	}
	if !strings.Contains(rs, "ip saddr 10.99.0.10 ip daddr 10.99.0.20/32 accept") {
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
	if !ok || !strings.Contains(line, "ip saddr 10.99.0.7 ip daddr 0.0.0.0/0 accept") {
		t.Fatalf("valid egress grant mis-rendered: %q", line)
	}
	// Host bits in dst are masked (defense-in-depth; the service already canonicalizes).
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.5/24", Protocol: "any"})
	if !strings.Contains(line, "ip daddr 10.0.5.0/24") {
		t.Fatalf("dst not canonicalized: %q", line)
	}
	// tcp with no ports -> ip protocol clause.
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp"})
	if !strings.Contains(line, "ip protocol tcp accept") {
		t.Fatalf("tcp-no-ports mis-rendered: %q", line)
	}
	// port range.
	line, _ = renderAllow(nodepolicy.AllowEntry{SrcIP: "10.0.0.1", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 8000, PortHigh: 9000})
	if !strings.Contains(line, "tcp dport 8000-9000 accept") {
		t.Fatalf("port range mis-rendered: %q", line)
	}
}
