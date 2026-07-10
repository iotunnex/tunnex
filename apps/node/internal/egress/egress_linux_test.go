//go:build linux

package egress

import (
	"strings"
	"testing"
)

// The tunnex nft ruleset must masquerade tunnel→egress and forward spoke↔spoke +
// spoke↔egress. This pins the generated rules (a wrong ruleset = a leak or a broken
// gateway) without needing a live kernel.
func TestRuleset(t *testing.T) {
	m := New("wg0")
	rs := m.ruleset("eth0")

	wants := []string{
		"table inet tunnex",
		"flush table inet tunnex",                       // atomic replace (idempotent reconcile)
		"type nat hook postrouting",                     // NAT
		`iifname "wg0" oifname "eth0" masquerade`,        // full-tunnel egress source-NAT
		"type filter hook forward",                       // forwarding
		`iifname "wg0" oifname "wg0" accept`,             // device-to-device (spoke↔spoke)
		`iifname "wg0" oifname "eth0" accept`,            // full-tunnel out
		`iifname "eth0" oifname "wg0" ct state established,related accept`, // return path
	}
	for _, w := range wants {
		if !strings.Contains(rs, w) {
			t.Errorf("ruleset missing %q\n---\n%s", w, rs)
		}
	}
}
