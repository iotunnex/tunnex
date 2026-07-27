package authctx

import (
	"testing"

	"github.com/google/uuid"
)

// TestPrincipalAuditActor — S10.2 D3: a machine principal attributes to a SYSTEM actor (operator:<name>)
// with the credential as cause and NO user id; a human attributes to its user id with no system actor.
func TestPrincipalAuditActor(t *testing.T) {
	mid := uuid.New()
	m := &Principal{MachineID: mid, MachineName: "gitops", AuthMethod: AuthMachine}
	if !m.IsMachine() {
		t.Fatal("a principal with a MachineID must be a machine")
	}
	uid, sys, cause := m.AuditActor()
	if uid != uuid.Nil {
		t.Fatalf("a machine must have NO user id, got %v", uid)
	}
	if sys != "operator:gitops" {
		t.Fatalf("machine actor_system must be operator:<name>, got %q", sys)
	}
	if cause != "machine_credential:"+mid.String() {
		t.Fatalf("machine cause must name the credential, got %q", cause)
	}

	h := &Principal{UserID: uuid.New(), AuthMethod: AuthLocalPassword}
	if h.IsMachine() {
		t.Fatal("a user principal must not be a machine")
	}
	huid, hsys, hcause := h.AuditActor()
	if huid != h.UserID || hsys != "" || hcause != "" {
		t.Fatalf("a human must attribute (userID, \"\", \"\"), got (%v,%q,%q)", huid, hsys, hcause)
	}
}
