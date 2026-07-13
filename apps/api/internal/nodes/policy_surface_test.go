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
	// Mirror the real CompiledHashesForNodes: only ENFORCING nodes get a non-empty pushed
	// hash; off / mesh -> "" (no enforcement boundary).
	h := ""
	if f.pol != nil && !f.pol.Mesh {
		h = policyspec.CanonicalHash(*f.pol)
	}
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		out[id] = h
	}
	return out, nil
}

func capsJSON(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

// PolicyDegraded is the collapsed single health signal (S7.2 design change). It is a
// CONSERVATIVE disjunction of three terms; the field may only err toward OVER-reporting.
// Pure — no DB (fakeProvider).
func TestPolicyDegraded(t *testing.T) {
	enf := &policyspec.Compiled{Version: 1, Mode: "enforcing", Mesh: false,
		Allow: []policyspec.AllowEntry{{SrcIP: "10.99.0.2", DstCIDR: "10.0.5.0/24", Protocol: policyspec.ProtoAny}}}
	pushed := policyspec.CanonicalHash(*enf)
	org := uuid.New()
	node := func(caps map[string]any) sqlc.Node { return sqlc.Node{ID: uuid.New(), Capabilities: capsJSON(caps)} }
	deg := func(s *Service, n sqlc.Node) bool {
		return s.PolicyDegradedForNodes(context.Background(), org, []sqlc.Node{n})[n.ID]
	}

	enfSvc := &Service{policy: fakeProvider{pol: enf}}
	// Healthy enforcing, in sync -> NOT degraded.
	if deg(enfSvc, node(map[string]any{"policy_hash": pushed})) {
		t.Fatal("healthy in-sync enforcing gateway must not be degraded")
	}
	// Term 3: enforcing, applied != pushed -> degraded (silent desync).
	if !deg(enfSvc, node(map[string]any{"policy_hash": "deadbeef0000"})) {
		t.Fatal("enforcing desync (applied != pushed) must be degraded")
	}
	// Term 2: failingSince set (any duration) -> degraded.
	if !deg(enfSvc, node(map[string]any{"policy_hash": pushed, "policy_failing_since": time.Now().UTC().Format(time.RFC3339)})) {
		t.Fatal("a failing enforcing apply (failingSince set) must be degraded")
	}
	// Term 1: applyErr set -> degraded even when otherwise in-sync.
	if !deg(enfSvc, node(map[string]any{"policy_hash": pushed, "policy_error": "nft: apply rejected"})) {
		t.Fatal("an apply error must be degraded")
	}

	// OFF org (provider returns "" pushed): a healthy off gateway is NOT degraded, whatever
	// its leftover applied hash — no enforcement boundary (kills #C's false alarm).
	offSvc := &Service{policy: fakeProvider{pol: nil}}
	if deg(offSvc, node(map[string]any{"policy_hash": "leftover_mesh_hash"})) {
		t.Fatal("healthy off-mode gateway must not be degraded")
	}

	// THE RED TEST (finding #1 — the gap state that survived passes 2, 3 AND 4): a
	// STUCK-ENFORCING gateway — off org, failed mesh apply so applyErr is set, failingSince
	// empty, and synced-would-be-true (off -> pushed "") — MUST be degraded via term 1. This
	// is the exact green-while-blackholing state the collapse exists to close.
	stuck := node(map[string]any{"policy_hash": "old_enforcing_hash", "policy_error": "nft: apply failed"})
	if !deg(offSvc, stuck) {
		t.Fatal("stuck-enforcing off gateway (applyErr set) MUST be degraded — the silent-blackhole class")
	}

	// A transient PROVIDER error (pushed unknown) must not manufacture a degraded signal on
	// an otherwise-clean node: term 3 is skipped, terms 1/2 are clean -> not degraded.
	errSvc := &Service{policy: fakeProvider{err: errors.New("db blip")}}
	if deg(errSvc, node(map[string]any{"policy_hash": "anything"})) {
		t.Fatal("a transient provider error alone must not degrade a clean gateway")
	}
	// ...but the agent-reported terms still apply through a provider error.
	if !deg(errSvc, node(map[string]any{"policy_error": "nft: apply failed"})) {
		t.Fatal("an agent-reported apply error must degrade even when the provider errored")
	}

	// Open build (no provider): nothing to compare, a clean node is not degraded.
	if (&Service{}).PolicyDegradedForNodes(context.Background(), org, []sqlc.Node{node(map[string]any{"policy_hash": "x"})}) == nil {
		t.Fatal("open build must return a map, not nil")
	}
	if deg(&Service{}, node(map[string]any{"policy_hash": "x"})) {
		t.Fatal("open build clean node must not be degraded")
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
