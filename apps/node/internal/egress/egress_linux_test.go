//go:build linux

package egress

import (
	"strings"
	"testing"
)

// The tunnex ruleset must: masquerade tunnel→egress (any non-tunnel oif), forward
// spoke↔spoke + spoke↔egress under a DROP policy (so the ct-state return guard is real
// and the egress side can't initiate into spokes), and DROP IPv6 full-tunnel egress (no
// NAT66 → no leak). This pins the generated rules without a live kernel.
func TestRuleset(t *testing.T) {
	m := New("wg0")
	m.SetPolicy(nil) // received nil = legacy mesh (cold-start with no policy is now deny-all, finding #2)
	rs := m.ruleset("10.99.0.1/24")

	wants := []string{
		"table ip tunnex",
		"flush table ip tunnex", // atomic replace (idempotent reconcile / self-heal)
		"type nat hook postrouting",
		`ip saddr 10.99.0.1/24 oifname != "wg0" masquerade`, // SOURCE-scoped NAT, any non-tunnel iface
		"type filter hook forward priority filter; policy drop;", // DROP policy → guards are real
		"ct state established,related accept",
		`iifname "wg0" oifname "wg0" counter accept`,    // device-to-device (spoke↔spoke); counter=flow-log seam (S7.2)
		`iifname "wg0" oifname != "wg0" counter accept`, // full-tunnel egress out
		"table ip6 tunnex",                       // IPv6: forward DROP, no NAT
	}
	for _, w := range wants {
		if !strings.Contains(rs, w) {
			t.Errorf("ruleset missing %q\n---\n%s", w, rs)
		}
	}
	// No masquerade uses iifname (unreliable in postrouting) — scope is ip saddr only.
	if strings.Contains(rs, "iifname \"wg0\" oifname != \"wg0\" masquerade") {
		t.Error("masquerade must be source-scoped (ip saddr), not iifname (unreliable in postrouting)")
	}
	// The v6 table must NOT masquerade (no NAT66 yet — v6 full-tunnel is dropped, not leaked).
	v6 := rs[strings.Index(rs, "table ip6 tunnex"):]
	if strings.Contains(v6, "masquerade") {
		t.Errorf("ip6 table must not masquerade (would risk a v6 leak):\n%s", v6)
	}
	// Before wg0 is up (no subnet), the postrouting chain has NO masquerade rule.
	if strings.Contains(New("wg0").ruleset(""), "masquerade") {
		t.Error("no masquerade should be emitted when the pool subnet is unknown")
	}
}

func TestIfaceValidationRejectsInjection(t *testing.T) {
	if ifaceRE.MatchString(`wg0" ; drop table ip tunnex ; #`) {
		t.Fatal("iface regex must reject an injection payload")
	}
	if !ifaceRE.MatchString("wg0") {
		t.Fatal("iface regex must accept a normal name")
	}
}
