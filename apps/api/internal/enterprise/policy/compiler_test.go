//go:build enterprise

package policy_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// Fixed IDs so tests read clearly and output is stable.
var (
	nodeA = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	nodeB = uuid.MustParse("00000000-0000-0000-0000-0000000000b1")

	uAlice = uuid.MustParse("00000000-0000-0000-0000-00000000a11c")
	uBob   = uuid.MustParse("00000000-0000-0000-0000-00000000b0b0")
	uCarol = uuid.MustParse("00000000-0000-0000-0000-0000000ca401")

	gAdmins  = uuid.MustParse("00000000-0000-0000-0000-00000000ad00")
	gServers = uuid.MustParse("00000000-0000-0000-0000-0000005e4e40")

	rDB = uuid.MustParse("00000000-0000-0000-0000-0000000db000")
)

// allowsFor returns node A's allow entries (nil if the node is absent).
func allowsFor(m map[uuid.UUID]policyspec.Compiled, n uuid.UUID) []policyspec.AllowEntry {
	return m[n].Allow
}

func hasAllow(entries []policyspec.AllowEntry, src, dst string) bool {
	for _, e := range entries {
		if e.SrcIP == src && e.DstCIDR == dst {
			return true
		}
	}
	return false
}

// B1 (deliberate-red): enforcing + ZERO grants proves the BASE, not a rule —
// the compiled allow-set is EMPTY, and empty must not read as permissive
// (Mesh=false). If this ever compiles to a non-empty or mesh output, default-deny
// is broken.
func TestEnforcingZeroGrantsIsEmptyNotPermissive(t *testing.T) {
	snap := policy.Snapshot{
		Mode: policy.ModeEnforcing,
		Devices: []policy.Device{
			{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
			{UserID: uBob, NodeID: nodeA, AssignedIP: "10.99.0.11"},
		},
		// no groups, no memberships, no rules
	}
	out := policy.Compile(snap)
	c, ok := out[nodeA]
	if !ok {
		t.Fatal("node with active devices must appear in output even under deny-all")
	}
	if c.Mesh {
		t.Fatal("enforcing mode must NEVER set Mesh=true (that is the blanket mesh)")
	}
	if len(c.Allow) != 0 {
		t.Fatalf("zero grants must compile to an EMPTY allow-set, got %d entries: %+v", len(c.Allow), c.Allow)
	}
	if c.Mode != policy.ModeEnforcing {
		t.Fatalf("mode = %q, want enforcing", c.Mode)
	}
}

// B2 (structural guard): the enforcing path can NEVER emit Mesh=true, no matter
// the inputs. Drive a rich enforcing snapshot and assert every node is Mesh=false.
func TestEnforcingNeverEmitsBlanketMesh(t *testing.T) {
	out := policy.Compile(richSnapshot(policy.ModeEnforcing))
	if len(out) == 0 {
		t.Fatal("expected node output")
	}
	for id, c := range out {
		if c.Mesh {
			t.Fatalf("node %s: enforcing must not emit Mesh=true", id)
		}
	}
}

// C4: off mode is the legacy blanket mesh (Mesh=true, no allows); flipping to
// enforcing recompiles to a grant set. Same devices, different mode => different
// output (proves mode is a compiler input, and the transition recompiles).
func TestModeOffIsMeshEnforcingIsGrants(t *testing.T) {
	off := policy.Compile(richSnapshot(policy.ModeOff))
	if c := off[nodeA]; !c.Mesh || len(c.Allow) != 0 || c.Mode != policy.ModeOff {
		t.Fatalf("off mode: want mesh+no-allows+off, got %+v", c)
	}
	enf := policy.Compile(richSnapshot(policy.ModeEnforcing))
	if c := enf[nodeA]; c.Mesh || c.Mode != policy.ModeEnforcing {
		t.Fatalf("enforcing mode: want !mesh+enforcing, got %+v", c)
	}
	if len(enf[nodeA].Allow) == 0 {
		t.Fatal("enforcing rich snapshot should yield grants on node A")
	}
}

// C4 fail-closed: an UNKNOWN mode must be treated as enforcing (deny), never as
// the permissive mesh.
func TestUnknownModeFailsClosed(t *testing.T) {
	snap := richSnapshot("garbage")
	out := policy.Compile(snap)
	for id, c := range out {
		if c.Mesh {
			t.Fatalf("node %s: unknown mode must fail closed (no mesh)", id)
		}
	}
}

// A2 (deliberate-red): revoking a group-member device removes its /32 from the
// compiled output as BOTH a source (no allows keyed on it) and a destination (no
// peer may reach it) — so a reused address can't inherit the old device's grants.
func TestRevokedDeviceLeavesCompiledOutput(t *testing.T) {
	// Admins may reach the servers group (device-to-device). Alice is an admin,
	// Bob + Carol are servers, all on node A.
	base := policy.Snapshot{
		Mode: policy.ModeEnforcing,
		Rules: []policy.Rule{
			{SrcGroupID: gAdmins, DstKind: "group", DstGroupID: gServers},
		},
		Memberships: []policy.Membership{
			{GroupID: gAdmins, UserID: uAlice},
			{GroupID: gServers, UserID: uBob},
			{GroupID: gServers, UserID: uCarol},
		},
		Devices: []policy.Device{
			{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
			{UserID: uBob, NodeID: nodeA, AssignedIP: "10.99.0.20"},
			{UserID: uCarol, NodeID: nodeA, AssignedIP: "10.99.0.21"},
		},
	}
	before := allowsFor(policy.Compile(base), nodeA)
	if !hasAllow(before, "10.99.0.10", "10.99.0.20/32") {
		t.Fatalf("baseline: admin should reach Bob's server, got %+v", before)
	}

	// Revoke Bob's device == remove it from the active-device snapshot.
	revoked := base
	revoked.Devices = []policy.Device{
		{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
		{UserID: uCarol, NodeID: nodeA, AssignedIP: "10.99.0.21"},
	}
	after := allowsFor(policy.Compile(revoked), nodeA)

	// Bob's /32 must be gone as a DESTINATION...
	if hasAllow(after, "10.99.0.10", "10.99.0.20/32") {
		t.Fatal("revoked device's /32 still present as a destination — inherited grant on IP reuse")
	}
	// ...and nowhere at all (also not as a source, though Bob was never a source here).
	for _, e := range after {
		if e.SrcIP == "10.99.0.20" || e.DstCIDR == "10.99.0.20/32" {
			t.Fatalf("revoked /32 10.99.0.20 still referenced: %+v", e)
		}
	}
	// Carol's server is still reachable — revocation was surgical.
	if !hasAllow(after, "10.99.0.10", "10.99.0.21/32") {
		t.Fatal("Carol's server should still be reachable after Bob's revoke")
	}
}

// Identity binding: access derives ONLY from the owner's group memberships. A
// device whose owner is in no group gets no grants even when rules exist.
func TestNoGrantsWithoutOwnerMembership(t *testing.T) {
	snap := policy.Snapshot{
		Mode: policy.ModeEnforcing,
		Resources: []policy.Resource{
			{ID: rDB, CIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432},
		},
		Rules: []policy.Rule{
			{SrcGroupID: gAdmins, DstKind: "resource", DstResourceID: rDB},
		},
		// Alice is NOT a member of gAdmins (no memberships at all).
		Devices: []policy.Device{
			{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
		},
	}
	if a := allowsFor(policy.Compile(snap), nodeA); len(a) != 0 {
		t.Fatalf("no membership => no grants, got %+v", a)
	}
}

// Resource destination compiles to src /32 -> cidr with the L4 scope.
func TestResourceDestinationScope(t *testing.T) {
	snap := policy.Snapshot{
		Mode: policy.ModeEnforcing,
		Resources: []policy.Resource{
			{ID: rDB, CIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432},
		},
		Rules:       []policy.Rule{{SrcGroupID: gAdmins, DstKind: "resource", DstResourceID: rDB}},
		Memberships: []policy.Membership{{GroupID: gAdmins, UserID: uAlice}},
		Devices:     []policy.Device{{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"}},
	}
	a := allowsFor(policy.Compile(snap), nodeA)
	if len(a) != 1 {
		t.Fatalf("want 1 entry, got %+v", a)
	}
	e := a[0]
	if e.SrcIP != "10.99.0.10" || e.DstCIDR != "10.0.5.0/24" || e.Protocol != policyspec.ProtoTCP || e.PortLow != 5432 || e.PortHigh != 5432 {
		t.Fatalf("resource scope wrong: %+v", e)
	}
}

// Duplicate grants (two rules resolving to the same entry) collapse to one.
func TestDuplicateGrantsDeduped(t *testing.T) {
	snap := policy.Snapshot{
		Mode: policy.ModeEnforcing,
		Resources: []policy.Resource{
			{ID: rDB, CIDR: "10.0.5.0/24", Protocol: "any"},
		},
		Rules: []policy.Rule{
			{SrcGroupID: gAdmins, DstKind: "resource", DstResourceID: rDB},
			{SrcGroupID: gServers, DstKind: "resource", DstResourceID: rDB},
		},
		Memberships: []policy.Membership{
			{GroupID: gAdmins, UserID: uAlice},
			{GroupID: gServers, UserID: uAlice}, // Alice in both groups => same entry twice
		},
		Devices: []policy.Device{{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"}},
	}
	if a := allowsFor(policy.Compile(snap), nodeA); len(a) != 1 {
		t.Fatalf("duplicate grants must dedup to 1, got %d: %+v", len(a), a)
	}
}

// Determinism: equal input compiles to byte-identical output (the reconcile
// no-op contract). Compile the same rich snapshot twice and compare JSON.
func TestCompileDeterministic(t *testing.T) {
	a := policy.Compile(richSnapshot(policy.ModeEnforcing))
	b := policy.Compile(richSnapshot(policy.ModeEnforcing))
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("non-deterministic output:\n a=%s\n b=%s", ja, jb)
	}
	// And the per-node allow slices are internally sorted.
	for _, c := range a {
		for i := 1; i < len(c.Allow); i++ {
			if !leAllow(c.Allow[i-1], c.Allow[i]) {
				t.Fatalf("allow entries not sorted: %+v", c.Allow)
			}
		}
	}
}

// Per-node isolation: a grant only lands on the node the SOURCE device lives on.
func TestGrantsPartitionedByNode(t *testing.T) {
	snap := policy.Snapshot{
		Mode:      policy.ModeEnforcing,
		Resources: []policy.Resource{{ID: rDB, CIDR: "0.0.0.0/0", Protocol: "any"}},
		Rules:     []policy.Rule{{SrcGroupID: gAdmins, DstKind: "resource", DstResourceID: rDB}},
		Memberships: []policy.Membership{
			{GroupID: gAdmins, UserID: uAlice},
			{GroupID: gAdmins, UserID: uBob},
		},
		Devices: []policy.Device{
			{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
			{UserID: uBob, NodeID: nodeB, AssignedIP: "10.99.0.11"},
		},
	}
	out := policy.Compile(snap)
	if !hasAllow(out[nodeA].Allow, "10.99.0.10", "0.0.0.0/0") {
		t.Fatal("node A should carry Alice's egress grant")
	}
	if !hasAllow(out[nodeB].Allow, "10.99.0.11", "0.0.0.0/0") {
		t.Fatal("node B should carry Bob's egress grant")
	}
	if hasAllow(out[nodeA].Allow, "10.99.0.11", "0.0.0.0/0") {
		t.Fatal("Bob's grant leaked onto node A")
	}
}

func leAllow(a, b policyspec.AllowEntry) bool {
	if a.SrcIP != b.SrcIP {
		return a.SrcIP <= b.SrcIP
	}
	return a.DstCIDR <= b.DstCIDR
}

// richSnapshot: a non-trivial org with a resource grant + a device-to-device grant
// across two nodes, parameterized by mode.
func richSnapshot(mode string) policy.Snapshot {
	return policy.Snapshot{
		Mode: mode,
		Resources: []policy.Resource{
			{ID: rDB, CIDR: "10.0.5.0/24", Protocol: "tcp", PortLow: 5432, PortHigh: 5432},
		},
		Rules: []policy.Rule{
			{SrcGroupID: gAdmins, DstKind: "resource", DstResourceID: rDB},
			{SrcGroupID: gAdmins, DstKind: "group", DstGroupID: gServers},
		},
		Memberships: []policy.Membership{
			{GroupID: gAdmins, UserID: uAlice},
			{GroupID: gServers, UserID: uBob},
		},
		Devices: []policy.Device{
			{UserID: uAlice, NodeID: nodeA, AssignedIP: "10.99.0.10"},
			{UserID: uBob, NodeID: nodeA, AssignedIP: "10.99.0.20"},
		},
	}
}
