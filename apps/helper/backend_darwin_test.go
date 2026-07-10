//go:build darwin

package helper

import (
	"reflect"
	"strings"
	"testing"
)

// TestRouteTargets pins the RC2 split-default mapping: a full-tunnel default is
// installed as the WG-standard half-route PAIR (more specific than the physical
// default, so it takes precedence WITHOUT destroying it), while any non-default
// destination passes through unchanged. This is what makes teardown/crash recover
// the physical default automatically instead of stranding the host.
func TestRouteTargets(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"0.0.0.0/0", []string{"0.0.0.0/1", "128.0.0.0/1"}},
		{"::/0", []string{"::/1", "8000::/1"}},
		{"10.99.0.0/24", []string{"10.99.0.0/24"}}, // split-tunnel route untouched
		{"10.99.0.1/32", []string{"10.99.0.1/32"}},
	}
	for _, c := range cases {
		if got := routeTargets(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("routeTargets(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBuildPFRulesUsesPassQuickNotSetSkip guards the kill-switch data-plane fix:
// `set skip on <iface>` is SILENTLY IGNORED inside a pf anchor (set options are
// main-ruleset-only), which left every packet routed onto the tunnel falling through
// `block drop out all` — the tunnel handshook but carried no data. The rules MUST use
// `pass quick on <iface>` (honored in an anchor) so loopback + the tunnel interface
// bypass the block. This pins that regression without a live pf.
func TestBuildPFRulesUsesPassQuickNotSetSkip(t *testing.T) {
	rules := buildPFRules("40.65.63.141:51820", "utun4")

	// `set skip` must NEVER appear — pf drops it in an anchor, silently disarming the bypass.
	if strings.Contains(rules, "set skip") {
		t.Errorf("buildPFRules must not use `set skip` (ignored inside a pf anchor):\n%s", rules)
	}
	// Loopback + the tunnel interface must be passed quick ABOVE the block.
	wants := []string{
		"pass quick on lo0 all",
		"pass quick on utun4 all",
		"block drop out all",
		"pass out proto udp to 40.65.63.141 port 51820", // WG endpoint (handshake + data)
	}
	for _, w := range wants {
		if !strings.Contains(rules, w) {
			t.Errorf("buildPFRules missing %q\n---\n%s", w, rules)
		}
	}
	// The quick passes must come BEFORE `block drop out all` (quick = first-match-wins).
	if i, j := strings.Index(rules, "pass quick on utun4"), strings.Index(rules, "block drop out all"); i < 0 || i > j {
		t.Errorf("`pass quick on utun4` must precede `block drop out all`:\n%s", rules)
	}

	// Before the tunnel exists (ifname ""), only loopback is passed — no tunnel iface,
	// so the initial arm blocks everything except the endpoint (fail-closed).
	initial := buildPFRules("40.65.63.141:51820", "")
	if strings.Contains(initial, "pass quick on utun") {
		t.Errorf("no tunnel-iface pass should be emitted before the tunnel exists:\n%s", initial)
	}
	if !strings.Contains(initial, "pass quick on lo0 all") {
		t.Errorf("loopback must always be passed:\n%s", initial)
	}
}
