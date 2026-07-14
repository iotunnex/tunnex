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

// TestCanonicalHashRuleIDIsObservabilityOnly is the A-1/A-3 red pair: rule_id
// (observability) must be INVISIBLE to the hash in BOTH directions, and an
// enforcement-field change MUST move the hash. This is the "observability, never
// semantics" law proven at the hash layer — and the death of the delete+recreate
// desync flap (a rule re-created with a new uuid but identical grant hashes the same).
func TestCanonicalHashRuleIDIsObservabilityOnly(t *testing.T) {
	entry := func(dst, ruleID string) policyspec.AllowEntry {
		return policyspec.AllowEntry{SrcIP: "10.99.0.10", DstCIDR: dst, Protocol: policyspec.ProtoTCP, PortLow: 5432, PortHigh: 5432, RuleID: ruleID}
	}
	base := policyspec.Compiled{Version: policyspec.ProtocolVersion, NodeID: "node-a", Mode: "enforcing"}

	noRule := base
	noRule.Allow = []policyspec.AllowEntry{entry("10.0.5.0/24", "")}
	withRule := base
	withRule.Allow = []policyspec.AllowEntry{entry("10.0.5.0/24", "rule-uuid-1")}
	otherRule := base
	otherRule.Allow = []policyspec.AllowEntry{entry("10.0.5.0/24", "rule-uuid-2")}
	enfChanged := base
	enfChanged.Allow = []policyspec.AllowEntry{entry("10.0.6.0/24", "rule-uuid-1")}

	// Observability direction: rule_id present vs absent, and one uuid vs another,
	// must NOT change the hash.
	if policyspec.CanonicalHash(noRule) != policyspec.CanonicalHash(withRule) {
		t.Fatal("rule_id present vs absent must NOT change the hash")
	}
	if policyspec.CanonicalHash(withRule) != policyspec.CanonicalHash(otherRule) {
		t.Fatal("a DIFFERENT rule_id must NOT change the hash (delete+recreate = no flap)")
	}
	// Enforcement direction: a real grant change MUST change the hash.
	if policyspec.CanonicalHash(withRule) == policyspec.CanonicalHash(enfChanged) {
		t.Fatal("changing an enforcement field (DstCIDR) MUST change the hash")
	}
}
