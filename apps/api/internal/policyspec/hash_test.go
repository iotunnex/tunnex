package policyspec_test

import (
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// CROSS-MODULE GOLDEN: apps/node/internal/nodepolicy has the IDENTICAL fixtures and
// expected hex (its own golden test). apps/api and apps/node are separate modules, so
// this twin-golden pair is the only guard pinning both CanonicalHash implementations
// (and both struct's field order/tags) to the same canonical bytes. If this test and
// nodepolicy's disagree, pushed-vs-applied staleness comparison is broken — fix the
// drifted struct, do NOT just update one golden.
func TestCanonicalHashGolden(t *testing.T) {
	enforcing := policyspec.Compiled{
		Version: 1, NodeID: "node-a", Mode: "enforcing", Mesh: false,
		Allow: []policyspec.AllowEntry{
			{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoTCP, PortLow: 5432, PortHigh: 5432},
			{SrcIP: "10.99.0.10", DstCIDR: "10.99.0.20/32", Protocol: policyspec.ProtoAny},
		},
	}
	if got := policyspec.CanonicalHash(enforcing); got != "1cd3184dcfa7" {
		t.Fatalf("enforcing golden = %q, want 1cd3184dcfa7 (nodepolicy twin must match)", got)
	}
	mesh := policyspec.Compiled{Version: 1, NodeID: "node-a", Mode: "off", Mesh: true}
	if got := policyspec.CanonicalHash(mesh); got != "a44457394212" {
		t.Fatalf("mesh golden = %q, want a44457394212 (nodepolicy twin must match)", got)
	}
}
