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
}

// Finding #3 (control-plane isolation — the twin of the node-side data-plane isolation
// test): a policy-compile error must NOT fail the desired state. Peers are still served
// (so revocation converges within the <5s SLA, independent of the policy engine) and the
// policy fails CLOSED (deny-all enforcing), never nil = open mesh.
func TestDesiredStatePolicyErrorServesPeersFailsClosed(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	org, user, node, dev := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", org, "O", "n-"+org.String()); err != nil {
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
	if ds.Policy == nil || ds.Policy.Mode != "enforcing" || ds.Policy.Mesh {
		t.Fatalf("policy must FAIL CLOSED (enforcing deny-all), never nil/mesh; got %+v", ds.Policy)
	}
	if len(ds.Policy.Allow) != 0 {
		t.Fatalf("fail-closed policy must be deny-all (no allows); got %+v", ds.Policy.Allow)
	}
}
