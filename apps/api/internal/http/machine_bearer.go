package http

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"

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
func MachineAuth(q *sqlc.Queries) BearerAuthFunc {
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
