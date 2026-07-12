package nodepolicy_test

import (
	"encoding/json"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// CROSS-MODULE GOLDEN: apps/api/internal/policyspec has the IDENTICAL fixtures and
// expected hex (its own golden test). This twin-golden pair is the only guard pinning
// both CanonicalHash implementations (and both struct's field order/tags) to the same
// canonical bytes across the two modules. If this test and policyspec's disagree,
// pushed-vs-applied staleness comparison is broken — fix the drifted struct, do NOT
// just update one golden.
func TestCanonicalHashGolden(t *testing.T) {
	enforcing := &nodepolicy.Compiled{
		Version: 1, NodeID: "node-a", Mode: nodepolicy.ModeEnforcing, Mesh: false,
		Allow: []nodepolicy.AllowEntry{
			{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432},
			{SrcIP: "10.99.0.10", DstCIDR: "10.99.0.20/32", Protocol: "any"},
		},
	}
	if got := nodepolicy.CanonicalHash(enforcing); got != "1cd3184dcfa7" {
		t.Fatalf("enforcing golden = %q, want 1cd3184dcfa7 (policyspec twin must match)", got)
	}
	mesh := &nodepolicy.Compiled{Version: 1, NodeID: "node-a", Mode: nodepolicy.ModeOff, Mesh: true}
	if got := nodepolicy.CanonicalHash(mesh); got != "a44457394212" {
		t.Fatalf("mesh golden = %q, want a44457394212 (policyspec twin must match)", got)
	}
	if nodepolicy.CanonicalHash(nil) != "" {
		t.Fatal("nil policy must hash to empty (mesh/none)")
	}
}

// DECODE-THEN-REHASH round-trip: the hash the agent computes over what it DECODED from
// the wire must equal the hash the control plane computed over what it MARSHALED —
// i.e. decode(marshal(x)) re-marshals to the same canonical bytes. This is the
// property staleness comparison actually depends on.
func TestCanonicalHashSurvivesWireRoundTrip(t *testing.T) {
	wire := `{"version":1,"node_id":"node-a","mode":"enforcing","mesh":false,"allow":[{"src_ip":"10.99.0.10","dst_cidr":"10.0.5.0/24","protocol":"tcp","port_low":5432,"port_high":5432},{"src_ip":"10.99.0.10","dst_cidr":"10.99.0.20/32","protocol":"any"}]}`
	var c nodepolicy.Compiled
	if err := json.Unmarshal([]byte(wire), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := nodepolicy.CanonicalHash(&c); got != "1cd3184dcfa7" {
		t.Fatalf("round-trip hash = %q, want 1cd3184dcfa7", got)
	}
}
