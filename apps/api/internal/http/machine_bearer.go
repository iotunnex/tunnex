package http

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/machineauth"
)

// MachineAuth resolves an Authorization: Bearer MACHINE credential (S10.2, `tnxm_` prefix) into a NON-USER
// machine principal. Mirrors the CLI bearer's NO-ORACLE hygiene: unknown/revoked are BOTH (nil,nil) → a
// generic 401 downstream, indistinguishable at the wire. Revocation severs on the very next request (the
// row is re-read every time — no session cache). The machine principal carries {orgID: role} so the
// EXISTING authorize()/RoleIn plumbing applies unchanged; it has NO UserID (a non-human is out of the
// identity-binding subject space, D4), and its downstream mutations attribute to a SYSTEM actor
// (authctx.Principal.AuditActor).
// machineCredStore is the narrow slice of the query layer this seam actually uses.
//
// ⛔ NARROWED SO THE SEAM CAN BE PROVED. It took *sqlc.Queries — a concrete type — which made the refusal arm
// untestable by construction, and THAT is why S15.1a's third mutation could remove the arm and still pass:
// the only reachable proof was the constructor's. A guard that cannot be exercised on its own is a guard
// nobody has shown can fail.
type machineCredStore interface {
	GetMachineCredentialByHash(ctx context.Context, tokenHash []byte) (sqlc.MachineCredential, error)
	TouchMachineCredentialUsed(ctx context.Context, id uuid.UUID) error
	// D23: is the owner still accountable? See the arm in MachineAuth.
	GetMachineOwnerStanding(ctx context.Context, arg sqlc.GetMachineOwnerStandingParams) (sqlc.GetMachineOwnerStandingRow, error)
}

func MachineAuth(q machineCredStore) BearerAuthFunc {
	return func(r *http.Request) (*authctx.Principal, error) {
		raw, ok := machineToken(r)
		if !ok {
			return nil, nil
		}
		h := sha256.Sum256([]byte(raw))
		cred, err := q.GetMachineCredentialByHash(r.Context(), h[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // unknown token — generic 401
		}
		if err != nil {
			return nil, nil // fail closed, no oracle
		}
		if cred.RevokedAt.Valid {
			return nil, nil // revoked — severs now, indistinguishable from unknown (no oracle)
		}
		// ⛔ S15.1 (D14/D19 step 3) — A NULL OWNER IS REFUSED AT USE, NOT MERELY UN-SET AT REST.
		//
		// `user_id` is nullable for the length of the expand/contract migration, and a nullable owner IS the
		// grandfather clause unless something refuses it. This is that something, and it sits beside the four
		// fail-closed arms above (unknown token, DB error, revoked, no-oracle) rather than being restated in
		// any handler: a guard made the caller's responsibility is inherited by every new caller.
		//
		// ⚠ SAME `nil, nil` AS THE OTHERS — a generic 401 with no oracle. An unassigned credential must not be
		// distinguishable on the wire from an unknown or revoked one.
		//
		// ⚠ AND THIS ARM RETIRES AT STEP 4, when the column contracts to NOT NULL. It cannot be removed before
		// then: assignment is an operator action with no code date.
		if !cred.UserID.Valid {
			return nil, nil
		}
		// ⛔ D23 — AN OWNER WHO IS NO LONGER ACCOUNTABLE CANNOT LEND THEIR ACCOUNTABILITY TO A CREDENTIAL.
		//
		// The binding was checked AT REST (the column is set) and never AT USE, so a credential outlived its
		// owner's deactivation, soft-deletion, or removal from the org — indefinitely. D14 bound credentials
		// to humans SO THAT accountability exists; without this arm the binding is a column, not a control.
		//
		// ⚠ THE OPERATIONAL COST IS REAL AND IS NOT MITIGATED HERE: deactivating a departing employee now
		// stops every GitOps operator they owned, AT THAT MOMENT, with no warning anywhere. Neither the
		// deactivation path nor its UI mentions machine credentials, and nothing in the product lists the
		// credentials a given person owns. That is the S13.1 class — a live thing stopping during ordinary
		// maintenance — and the honest statement is that the warning does not exist yet, not that this is
		// safe because the refusal is correct. Registered; re-assignment before deactivation is the remedy
		// and there is no screen for it.
		//
		// ⛔ REFUSED THE SAME WAY AS EVERY OTHER ARM: `nil, nil` → a generic 401. "Your owner was
		// deactivated" would be an oracle about a person to whoever holds a stolen token.
		//
		// ⚠ EMAIL VERIFICATION IS NOT PART OF THIS. A machine is exempt from the human email gate by
		// construction, and the bootstrap admin is pre-verified by design — refusing on it would contradict
		// a ruling rather than enforce one.
		standing, err := q.GetMachineOwnerStanding(r.Context(), sqlc.GetMachineOwnerStandingParams{
			ID: cred.UserID.Bytes, OrgID: cred.OrgID,
		})
		if err != nil {
			// Includes ErrNoRows: the owner is soft-deleted. Fail closed, no oracle.
			return nil, nil
		}
		if !standing.Active || !standing.InOrg {
			return nil, nil
		}
		_ = q.TouchMachineCredentialUsed(r.Context(), cred.ID) // best-effort telemetry
		// The constructor REFUSES to build a machine principal without an owner (authctx.NewMachinePrincipal).
		// The check above and the constructor are not redundant: the check makes the ROW impossible to use,
		// the constructor makes the PRINCIPAL impossible to build wrong.
		return authctx.NewMachinePrincipal(
			cred.UserID.Bytes, cred.ID, cred.OrgID, cred.Name, cred.Role,
			// D2 (Slice 4): the operator may name the CR that drove this change as the audit cause. Honored
			// ONLY here (a machine principal); a human's principal never carries it. Sanitized at the seam.
			authctx.SanitizeCause(r.Header.Get("X-Tunnex-Cause")),
		), nil
	}
}

// machineToken extracts a `tnxm_`-prefixed bearer token, if present. Distinct prefix from the CLI's `tnx_`,
// so the two credential kinds never collide on the same header.
func machineToken(r *http.Request) (string, bool) {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, scheme) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, scheme))
	if !strings.HasPrefix(tok, machineauth.TokenPrefix) {
		return "", false
	}
	return tok, true
}
