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

	"github.com/google/uuid"
)

// Principal is the authenticated caller and the orgs they belong to (with role).
// It is populated by the auth layer (a session-backed AuthFunc from S2); tests
// inject one directly.
type Principal struct {
	UserID        uuid.UUID
	SessionID     string // the session backing this principal (for logout)
	Email         string
	EmailVerified bool
	Roles         map[uuid.UUID]string // orgID -> role
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
