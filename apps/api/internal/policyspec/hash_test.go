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
		Version: 4, NodeID: "node-a", Mode: "enforcing", Mesh: false,
		Allow: []policyspec.AllowEntry{
			{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoTCP, PortLow: 5432, PortHigh: 5432},
			{SrcIP: "10.99.0.10", DstCIDR: "10.99.0.20/32", Protocol: policyspec.ProtoAny},
		},
	}
	if got := policyspec.CanonicalHash(enforcing); got != "56814207daee" {
		t.Fatalf("enforcing golden = %q, want 56814207daee (nodepolicy twin must match)", got)
	}
	mesh := policyspec.Compiled{Version: 4, NodeID: "node-a", Mode: "off", Mesh: true}
	if got := policyspec.CanonicalHash(mesh); got != "5696d2570ee8" {
		t.Fatalf("mesh golden = %q, want 5696d2570ee8 (nodepolicy twin must match)", got)
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

// TestCanonicalHashSrcDeviceIDIsObservabilityOnly (v3, S7.5.4): src_device_id, like
// rule_id, must be INVISIBLE to the hash — adding/changing it must NOT move the hash, so
// the v3 field bump never false-alarms an enforcing gateway.
func TestCanonicalHashSrcDeviceIDIsObservabilityOnly(t *testing.T) {
	base := policyspec.Compiled{Version: policyspec.ProtocolVersion, NodeID: "node-a", Mode: "enforcing"}
	mk := func(devID string) policyspec.Compiled {
		c := base
		c.Allow = []policyspec.AllowEntry{{SrcIP: "10.99.0.10", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoTCP, PortLow: 5432, PortHigh: 5432, RuleID: "r1", SrcDeviceID: devID}}
		return c
	}
	if policyspec.CanonicalHash(mk("")) != policyspec.CanonicalHash(mk("dev-1")) {
		t.Fatal("src_device_id present vs absent must NOT change the hash")
	}
	if policyspec.CanonicalHash(mk("dev-1")) != policyspec.CanonicalHash(mk("dev-2")) {
		t.Fatal("a DIFFERENT src_device_id must NOT change the hash")
	}
}
