package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// THE MIXED-VERSION-FLEET SAFETY (S7.2 chunk-1 pin 2b), asserted not incidental:
// a DesiredState JSON with NO policy field — an open-build control plane, or an
// older control plane mid-upgrade — must decode to Policy == nil, which the whole
// enforcement path treats as LEGACY MESH. If a decode refactor ever flips this
// default (e.g. a non-pointer field defaulting to enforcing-empty = deny-all, or
// mesh-true-with-allows = open), an upgrade would silently change live traffic.
func TestAbsentPolicyDecodesToMesh(t *testing.T) {
	wire := `{"protocol_version":1,"node_id":"n1","interface_address":"10.99.0.1/24",
	          "mtu":1420,"listen_port":51820,"version":7,"peers":[]}`
	var ds DesiredState
	if err := json.Unmarshal([]byte(wire), &ds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ds.Policy != nil {
		t.Fatalf("absent policy field must decode to nil (legacy mesh), got %+v", ds.Policy)
	}

	// An explicit null is the same contract.
	var ds2 DesiredState
	if err := json.Unmarshal([]byte(`{"version":1,"peers":[],"policy":null}`), &ds2); err != nil {
		t.Fatalf("decode null: %v", err)
	}
	if ds2.Policy != nil {
		t.Fatal("explicit null policy must also decode to nil (legacy mesh)")
	}

	// And a PRESENT policy round-trips intact (the positive control).
	var ds3 DesiredState
	present := `{"version":1,"peers":[],"policy":{"version":3,"node_id":"n1","mode":"enforcing","mesh":false,"allow":[]}}`
	if err := json.Unmarshal([]byte(present), &ds3); err != nil {
		t.Fatalf("decode present: %v", err)
	}
	if ds3.Policy == nil || ds3.Policy.Version != 3 || ds3.Policy.Mesh {
		t.Fatalf("present policy mis-decoded: %+v", ds3.Policy)
	}
}

// runOnce delivers the policy to the sink on EVERY fetch — including nil (which
// must be able to UNSET a previous policy: the enforcing->off recovery path) —
// and does so even when the WG backend converge fails (enforcement is orthogonal
// to interface config; a revocation push must not wait on an unrelated failure).
func TestRunOnceDeliversPolicyToSink(t *testing.T) {
	be := &fakeBackend{}
	cl := &fakeClient{}
	r := New(be, "priv", "pub", slog.New(slog.NewTextHandler(io.Discard, nil)))

	var got []*nodepolicy.Compiled
	r.OnPolicy(func(p *nodepolicy.Compiled) { got = append(got, p) })

	// 1: a policy arrives.
	cl.set(DesiredState{Version: 1, Policy: &nodepolicy.Compiled{Version: 5, Mode: nodepolicy.ModeEnforcing}})
	if _, err := r.runOnce(context.Background(), cl); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if len(got) != 1 || got[0] == nil || got[0].Version != 5 {
		t.Fatalf("policy not delivered: %+v", got)
	}

	// 2: absent policy delivers nil (unsets — legacy mesh).
	cl.set(DesiredState{Version: 2})
	if _, err := r.runOnce(context.Background(), cl); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if len(got) != 2 || got[1] != nil {
		t.Fatalf("nil policy must be delivered to unset: %+v", got)
	}

	// 3: a backend Configure failure must NOT block policy delivery.
	be.setConfigErr(errors.New("operation not permitted"))
	cl.set(DesiredState{Version: 3, Policy: &nodepolicy.Compiled{Version: 6, Mode: nodepolicy.ModeEnforcing}})
	if _, err := r.runOnce(context.Background(), cl); err == nil {
		t.Fatal("runOnce should surface the backend error")
	}
	if len(got) != 3 || got[2] == nil || got[2].Version != 6 {
		t.Fatalf("policy must be delivered even when the backend converge fails: %+v", got)
	}
}
