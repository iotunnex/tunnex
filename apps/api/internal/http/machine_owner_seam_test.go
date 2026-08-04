package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/machineauth"
)

// ⛔ THE OWED RED FROM S15.1a, AND IT IS OWED FOR A REASON.
//
// 1a shipped the seam refusal at machine_bearer.go and mutation-testing reported honestly that REMOVING IT
// STILL PASSED — the constructor caught the case. The backstop worked, which meant the seam arm was a line
// nobody had shown could fail.
//
// > **A GUARD THAT HAS ONLY EVER PASSED IS INDISTINGUISHABLE FROM ONE THAT DOES NOTHING.**
//
// This exercises the seam ITSELF against a row whose owner is NULL, so the arm has its own red. The two are
// not redundant: the seam check makes the ROW impossible to use, the constructor makes the PRINCIPAL
// impossible to build wrong. Each needs its own proof.

type stubMachineQ struct {
	cred    sqlc.MachineCredential
	touched *int
}

func (s stubMachineQ) GetMachineCredentialByHash(_ context.Context, _ []byte) (sqlc.MachineCredential, error) {
	return s.cred, nil
}
func (s stubMachineQ) TouchMachineCredentialUsed(_ context.Context, _ uuid.UUID) error {
	if s.touched != nil {
		*s.touched++
	}
	return nil
}

func req(tok string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/whatever", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

func TestSeamRefusesAnUnassignedMachineCredential(t *testing.T) {
	tok := machineauth.TokenPrefix + "abc"
	base := sqlc.MachineCredential{ID: uuid.New(), OrgID: uuid.New(), Name: "gitops", Role: "operator"}

	// RED — owner NULL. The seam must refuse, with the same nil,nil shape as unknown/revoked (no oracle).
	unassigned := base
	unassigned.UserID = pgtype.UUID{Valid: false}
	var touched int
	if p, err := MachineAuth(stubMachineQ{unassigned, &touched})(req(tok)); p != nil || err != nil {
		t.Fatalf("an UNASSIGNED machine credential authenticated: principal=%+v err=%v", p, err)
	}

	// ⛔ THE ASSERTION THAT BELONGS TO THE SEAM ARM ALONE — the reason this test exists.
	//
	// Refusing to BUILD the principal is the constructor'''s job, and it happens with the arm deleted; that is
	// exactly what S15.1a'''s third mutation showed. The arm'''s own observable effect is that it returns BEFORE
	// the telemetry write, so an unassigned credential is never recorded as USED. Without this line the arm
	// has no independent red and the two guards are one assertion wearing two hats.
	if touched != 0 {
		t.Fatalf("an unassigned credential was stamped as used %d time(s) — the seam arm did not short-circuit", touched)
	}

	// AND THE OTHER DIRECTION — an owned credential still authenticates. A seam that refuses everything
	// passes the red above and breaks every machine principal in the product.
	owned := base
	owned.UserID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	var touchedOwned int
	p, err := MachineAuth(stubMachineQ{owned, &touchedOwned})(req(tok))
	if err != nil || p == nil {
		t.Fatalf("an OWNED machine credential was refused at the seam: err=%v", err)
	}
	if p.OwnerUserID == uuid.Nil {
		t.Fatal("the seam built a principal without carrying the owner")
	}
	// And an OWNED credential IS stamped — otherwise "not stamped" would be trivially true for everything.
	if touchedOwned != 1 {
		t.Fatalf("an owned credential was stamped %d time(s), want 1", touchedOwned)
	}
}
