// Package authctx carries the authenticated principal and the authorized org
// through the request context.
//
// Two invariants this package exists to enforce:
//   - The org used for tenant scoping is set ONLY here (WithOrg), and only after
//     membership authorization. Handlers/services never take an org id from a
//     request body or query string for scoping — that is the classic IDOR.
//   - No principal in context means unauthenticated: callers fail closed.
package authctx

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Principal is the authenticated caller and the orgs they belong to (with role).
// It is populated by the auth layer (a session-backed AuthFunc from S2); tests
// inject one directly.
// Auth methods a principal can be minted with. Stamped ONCE at the credential/session mint seam
// and IMMUTABLE for that principal's lifetime (session fixation: the method a session was born with
// never changes). The S7.5.5 MFA-enrollment gate keys on this so D5's exemptions hold at the
// middleware (SSO + bearer are exempt by construction, not by route/header sniffing). An empty
// method = a legacy session minted before the marker existed; it is treated as NON-local (exempt),
// which aligns with D8 (enforcement governs new LOGINS, not live sessions — legacy sessions age out).
const (
	AuthLocalPassword = "local_password"
	AuthSSO           = "sso"
	AuthBearer        = "bearer"
	// AuthMachine (S10.2) — a NON-USER machine credential (the GitOps operator). Exempt from the
	// MFA-enrollment gate by construction (no human, no enrollment), like AuthBearer. A machine principal
	// has UserID == uuid.Nil and MachineID/MachineName set; its mutations attribute to a SYSTEM actor.
	AuthMachine = "machine"
)

type Principal struct {
	UserID        uuid.UUID
	SessionID     string // the session backing this principal (for logout)
	Email         string
	EmailVerified bool
	AuthMethod    string               // how this principal authenticated (AuthLocalPassword | AuthSSO | AuthBearer | AuthMachine | "")
	Roles         map[uuid.UUID]string // orgID -> role
	// MachineID / MachineName (S10.2) — set ONLY for a machine principal (AuthMachine); zero for a human.
	// A machine has NO UserID (kept out of the identity-binding subject space, D4). MachineName is the
	// operator-chosen credential label surfaced in audit as the system actor "operator:<name>".
	MachineID   uuid.UUID
	MachineName string
	// Cause (S10.2 Slice 4, D2) — a machine-only, per-request OVERRIDE for the audit cause: the CR that drove
	// the change (e.g. "tunnexcluster:default/prod"). Set ONLY from the X-Tunnex-Cause header on a machine
	// principal (a human's principal never carries it), sanitized. Empty → AuditActor falls back to the
	// credential identity. This is what makes a cascade delete name the CR, not just the operator (D2 cond 2).
	Cause string
}

// IsMachine reports whether this is a non-user machine principal (S10.2).
func (p *Principal) IsMachine() bool { return p != nil && p.MachineID != uuid.Nil }

// AuditActor returns the attribution for an audited mutation. For a HUMAN: (userID, "", "") → the row is
// actor_user_id-attributed (system + cause empty). For a MACHINE: (uuid.Nil, "operator:<name>",
// "machine_credential:<id>") → a SYSTEM-actor row (actor_system, migration 0027) whose cause names the
// machine credential. This is the ONE seam that keeps a GitOps change from masquerading as a human (D3 — a
// falsely-attributed row is worse than absent). If the machine set a per-request Cause (the CR that drove the
// change, via X-Tunnex-Cause, D2 Slice 4), that names the WHY; the credential identity is the honest default.
func (p *Principal) AuditActor() (actorUserID uuid.UUID, actorSystem, cause string) {
	if p.IsMachine() {
		cause = "machine_credential:" + p.MachineID.String()
		if p.Cause != "" {
			cause = p.Cause // the CR the operator names as the cause (D2 cond 2)
		}
		return uuid.Nil, "operator:" + p.MachineName, cause
	}
	return p.UserID, "", ""
}

// SanitizeCause bounds an operator-supplied audit cause (X-Tunnex-Cause): control characters stripped (no
// audit-log injection / newline forgery) and length capped. Untrusted machine input lands in the audit cause
// column, so it is cleaned at the seam, never trusted raw.
func SanitizeCause(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f { // drop control chars incl CR/LF/TAB
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 200 {
			break
		}
	}
	return b.String()
}

// RoleIn returns the principal's role in orgID and whether they are a member.
func (p *Principal) RoleIn(orgID uuid.UUID) (string, bool) {
	if p == nil {
		return "", false
	}
	r, ok := p.Roles[orgID]
	return r, ok
}

type ctxKey int

const (
	principalKey ctxKey = iota
	orgKey
)

// WithPrincipal attaches the authenticated principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the principal, or ok=false if unauthenticated.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	return p, ok && p != nil
}

// WithOrg records the AUTHORIZED org for tenant scoping. Call only after a
// membership check — never from client-supplied input.
func WithOrg(ctx context.Context, orgID uuid.UUID) context.Context {
	return context.WithValue(ctx, orgKey, orgID)
}

// OrgFrom returns the authorized org id set by the tenant authorization step.
func OrgFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(orgKey).(uuid.UUID)
	return id, ok
}
