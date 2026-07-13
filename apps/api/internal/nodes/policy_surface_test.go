package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

type fakeProvider struct {
	pol *policyspec.Compiled
	err error
}

func (f fakeProvider) CompiledForNode(context.Context, uuid.UUID, uuid.UUID) (*policyspec.Compiled, error) {
	return f.pol, f.err
}

func (f fakeProvider) CompiledHashesForNodes(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	h := ""
	if f.pol != nil {
		h = policyspec.CanonicalHash(*f.pol)
	}
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		out[id] = h
	}
	return out, nil
}

func capsJSON(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

// PolicyStatus surfaces both signals (finding #5/#7): synced from the restored
// pushed-vs-applied hash compare (catches SILENT staleness — never fetched), stale
// from failingSince (apply failing past the window). Pure — no DB.
func TestPolicyStatusSyncedAndStale(t *testing.T) {
	pol := &policyspec.Compiled{Version: 1, Mode: "enforcing", Mesh: false,
		Allow: []policyspec.AllowEntry{{SrcIP: "10.99.0.2", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoAny}}}
	pushed := policyspec.CanonicalHash(*pol)
	svc := &Service{policy: fakeProvider{pol: pol}}
	now := time.Now()

	// Applied hash == pushed -> synced, not stale.
	n := sqlc.Node{Capabilities: capsJSON(map[string]any{"policy_hash": pushed})}
	if stale, synced := svc.PolicyStatus(context.Background(), n, now); stale || !synced {
		t.Fatalf("in-sync node: want stale=false synced=true, got %v %v", stale, synced)
	}
	// Applied hash != pushed (never fetched the new policy) -> NOT synced (silent staleness).
	n2 := sqlc.Node{Capabilities: capsJSON(map[string]any{"policy_hash": "deadbeef0000"})}
	if _, synced := svc.PolicyStatus(context.Background(), n2, now); synced {
		t.Fatal("out-of-sync node must report synced=false (silent staleness catchable)")
	}
	// Apply failing past the window -> stale.
	n3 := sqlc.Node{Capabilities: capsJSON(map[string]any{
		"policy_hash": pushed, "policy_failing_since": now.Add(-2 * time.Minute).UTC().Format(time.RFC3339)})}
	if stale, _ := svc.PolicyStatus(context.Background(), n3, now); !stale {
		t.Fatal("a node failing apply for 2m must report stale")
	}
	// Open build (no provider) -> never stale, always synced.
	if stale, synced := (&Service{}).PolicyStatus(context.Background(), n2, now); stale || !synced {
		t.Fatalf("open build: want stale=false synced=true, got %v %v", stale, synced)
	}

	// Finding #4: a transient compile error must NOT render as out-of-sync. A healthy,
	// in-sync gateway (applied hash == pushed) whose compile momentarily errors must still
	// report synced=true — "couldn't determine" is not "desynced".
	errSvc := &Service{policy: fakeProvider{err: errors.New("db blip")}}
	if _, synced := errSvc.PolicyStatus(context.Background(), n, now); !synced {
		t.Fatal("a transient compile error must report synced=true, not a false out-of-sync alarm")
	}
}

// Finding #5: the batch PolicyStatusForNodes yields the same per-node signals as the
// single-node PolicyStatus (one org compile instead of N), and #4 holds in the batch —
// a compile error reports every node synced=true, never a false desync.
func TestPolicyStatusForNodesBatchParityAndErrorNotDesync(t *testing.T) {
	pol := &policyspec.Compiled{Version: 1, Mode: "enforcing", Mesh: false,
		Allow: []policyspec.AllowEntry{{SrcIP: "10.99.0.2", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoAny}}}
	pushed := policyspec.CanonicalHash(*pol)
	now := time.Now()
	inSync := sqlc.Node{ID: uuid.New(), Capabilities: capsJSON(map[string]any{"policy_hash": pushed})}
	desynced := sqlc.Node{ID: uuid.New(), Capabilities: capsJSON(map[string]any{"policy_hash": "deadbeef0000"})}

	svc := &Service{policy: fakeProvider{pol: pol}}
	stale, synced := svc.PolicyStatusForNodes(context.Background(), uuid.New(), []sqlc.Node{inSync, desynced}, now)
	if stale[inSync.ID] || !synced[inSync.ID] {
		t.Fatalf("in-sync node: want stale=false synced=true, got %v %v", stale[inSync.ID], synced[inSync.ID])
	}
	if synced[desynced.ID] {
		t.Fatal("desynced node must report synced=false in the batch")
	}

	// Error path: every node synced=true (unknown != desync).
	errSvc := &Service{policy: fakeProvider{err: errors.New("db blip")}}
	_, synced2 := errSvc.PolicyStatusForNodes(context.Background(), uuid.New(), []sqlc.Node{inSync, desynced}, now)
	if !synced2[inSync.ID] || !synced2[desynced.ID] {
		t.Fatal("a batch compile error must report all nodes synced=true, never a false desync")
	}
}

// Finding #C: a DEVICE-LESS off-mode node (CompiledForNode returns nil) must report
// synced=true even when its applied hash is non-empty — e.g. after a transient error
// where the fallback (older code) pushed a mesh artifact the agent applied. synced is
// meaningful ONLY under enforcing; off/mesh/nil has no boundary to diverge from. This is
// the exact case the earlier device-having-only test missed.
func TestPolicyStatusOffModeNeverDesyncs(t *testing.T) {
	now := time.Now()
	// Device-less off node: provider returns (nil, nil). Applied hash is some leftover mesh
	// hash. Must still be synced (no enforcement boundary).
	svcNil := &Service{policy: fakeProvider{pol: nil}}
	nDeviceless := sqlc.Node{ID: uuid.New(), Capabilities: capsJSON(map[string]any{"policy_hash": "leftover_mesh_hash"})}
	if _, synced := svcNil.PolicyStatus(context.Background(), nDeviceless, now); !synced {
		t.Fatal("device-less off node (pushed nil) must be synced=true regardless of applied hash")
	}
	// Device-having off node: provider returns a mesh artifact (Mesh=true). Also always synced.
	mesh := &policyspec.Compiled{Version: 1, Mode: "off", Mesh: true}
	svcMesh := &Service{policy: fakeProvider{pol: mesh}}
	nMesh := sqlc.Node{ID: uuid.New(), Capabilities: capsJSON(map[string]any{"policy_hash": "anything"})}
	if _, synced := svcMesh.PolicyStatus(context.Background(), nMesh, now); !synced {
		t.Fatal("off-mode mesh node must be synced=true (no enforcement boundary)")
	}
	// Batch: an off org (fake returns "" for every node via a nil pol) is all-synced even
	// with mismatched applied hashes.
	_, syncedB := svcNil.PolicyStatusForNodes(context.Background(), uuid.New(), []sqlc.Node{nDeviceless, nMesh}, now)
	if !syncedB[nDeviceless.ID] || !syncedB[nMesh.ID] {
		t.Fatal("batch: off-mode nodes must all be synced=true")
	}
}

// Finding #D guard: the DesiredState fail-closed/off fallbacks and the compiler stamp the
// policy artifact Version from TWO independent constants. They must stay equal, or a
// fallback artifact's canonical hash forks from the compiler's and false-alarms every
// enforcing gateway. This test fails CI loudly the moment a future bump breaks the tie.
func TestProtocolVersionConstantsAgree(t *testing.T) {
	if ProtocolVersion != policyspec.ProtocolVersion {
		t.Fatalf("nodes.ProtocolVersion (%d) != policyspec.ProtocolVersion (%d): the fail-closed "+
			"policy fallback would fork its hash from the compiler and false-alarm every enforcing "+
			"gateway. Reconcile them (or collapse to one constant).", ProtocolVersion, policyspec.ProtocolVersion)
	}
}

// Finding #3 + #2 (control-plane isolation, scoped): a policy-compile error must NOT fail
// the desired state — peers are still served (revocation converges within the <5s SLA,
// independent of the policy engine). The policy signal is SCOPED by the org's mode
// (finding #2): an ENFORCING org fails CLOSED (deny-all, never open mesh); a confirmed
// OFF org serves the mesh (a policy-subsystem blip must not blackhole an org that never
// opted into enforcement).
func TestDesiredStatePolicyErrorScopedByMode(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	cases := []struct {
		mode     string
		wantNil  bool // off => serve mesh via nil (agent decodes nil = blanket mesh)
		wantMode string
	}{
		{"off", true, ""},                 // nil policy = mesh; matches device-less steady state (#C)
		{"enforcing", false, "enforcing"}, // fail closed: explicit deny-all enforcing artifact
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			q := sqlc.New(tx)

			org, user, node, dev := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug,zero_trust_mode) VALUES ($1,$2,$3,$4)", org, "O", "n-"+org.String(), tc.mode); err != nil {
				t.Fatalf("org: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", user, "u@t", "U"); err != nil {
				t.Fatalf("user: %v", err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial) VALUES ($1,$2,$3,$4)", node, org, "gw", "serial-"+node.String()); err != nil {
				t.Fatalf("node: %v", err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO devices (id,org_id,user_id,node_id,name,public_key,assigned_ip) VALUES ($1,$2,$3,$4,$5,$6,$7)",
				dev, org, user, node, "laptop", "pubkey-a", "10.99.0.2"); err != nil {
				t.Fatalf("device: %v", err)
			}

			svc := &Service{q: q, policy: fakeProvider{err: errors.New("policy DB down")}}
			ds, err := svc.DesiredState(ctx, sqlc.Node{ID: node, OrgID: org, Name: "gw"})
			if err != nil {
				t.Fatalf("policy error must NOT fail the whole desired state: %v", err)
			}
			if len(ds.Peers) == 0 {
				t.Fatal("peers must still be served when the policy compile fails (revocation must converge)")
			}
			if tc.wantNil {
				// OFF: served as nil (= blanket mesh on the agent), matching the compiler's
				// device-less off output so PolicyStatus never false-alarms (#C).
				if ds.Policy != nil {
					t.Fatalf("off-mode policy error must serve nil (mesh), not an explicit artifact; got %+v", ds.Policy)
				}
				return
			}
			// ENFORCING: explicit deny-all, and the SAME policyspec.ProtocolVersion the
			// compiler stamps (#D) so its hash matches CompiledForNode's fallback.
			if ds.Policy == nil || ds.Policy.Mesh || ds.Policy.Mode != tc.wantMode {
				t.Fatalf("enforcing error must fail closed (deny-all enforcing); got %+v", ds.Policy)
			}
			if ds.Policy.Version != policyspec.ProtocolVersion {
				t.Fatalf("fail-closed artifact must use policyspec.ProtocolVersion (%d); got %d", policyspec.ProtocolVersion, ds.Policy.Version)
			}
			if len(ds.Policy.Allow) != 0 {
				t.Fatalf("enforcing fail-closed must be deny-all (no allows); got %+v", ds.Policy.Allow)
			}
		})
	}
}
