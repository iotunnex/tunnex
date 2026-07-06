// Package rbac is the single source of the authorization model: what each role
// may do. Call sites ask Can(role, permission) — never `role == "admin"` — so
// when a role is added or a permission moves, only this file changes.
package rbac

// Permission is a capability a role may hold.
type Permission string

const (
	PermOrgView      Permission = "org:view"
	PermOrgUpdate    Permission = "org:update"
	PermOrgDelete    Permission = "org:delete"
	PermMemberList   Permission = "member:list"
	PermMemberInvite Permission = "member:invite"
	// PermMemberManage is the base capability to change roles / remove members.
	// Relational limits (who may touch whom) are applied by CanManageMembership.
	PermMemberManage Permission = "member:manage"
)

// Roles.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// rolePermissions is the role -> permission grant table. This map IS the policy.
//
// MIRRORED CLIENT-SIDE in apps/web/src/lib/rbac.ts (to gate which controls
// render). The server is authoritative; the client copy is UX only. If you
// change this table or CanManageMembership, update rbac.ts in the same commit —
// there is no codegen keeping them in sync yet (tracked as a follow-up).
var rolePermissions = map[string]map[Permission]bool{
	RoleMember: {
		PermOrgView:    true,
		PermMemberList: true,
	},
	RoleAdmin: {
		PermOrgView:      true,
		PermMemberList:   true,
		PermOrgUpdate:    true,
		PermMemberInvite: true,
		PermMemberManage: true,
	},
	RoleOwner: {
		PermOrgView:      true,
		PermMemberList:   true,
		PermOrgUpdate:    true,
		PermOrgDelete:    true,
		PermMemberInvite: true,
		PermMemberManage: true,
	},
}

// Can reports whether a role holds a permission.
func Can(role string, p Permission) bool {
	return rolePermissions[role][p]
}

// IsMutating reports whether a permission changes state. Mutating actions are
// gated on a verified email (S2.2); read permissions are not.
func IsMutating(p Permission) bool {
	// Deliberately an ALLOWLIST OF READS: only the read permissions are
	// non-mutating; everything else (including any future permission) is treated
	// as mutating and therefore gated on a verified email. This is the
	// fail-closed polarity — an unclassified new permission gets the gate by
	// default, so the worst case is an unverified user 403ing on a read, never an
	// unverified user slipping through a mutation. Do NOT invert this into a
	// mutating-allowlist.
	switch p {
	case PermOrgView, PermMemberList:
		return false
	default:
		return true
	}
}

// ValidRole reports whether role is a known role.
func ValidRole(role string) bool {
	_, ok := rolePermissions[role]
	return ok
}

// CanManageMembership reports whether an actor may set target's role to newRole
// (newRole == "" means removal). It layers relational rules on PermMemberManage:
//   - only an owner may manage an existing owner;
//   - only an owner may grant the owner role (no privilege escalation by admins).
//
// The last-owner invariant (an org must keep >= 1 owner) is enforced separately
// at the service layer, since it requires counting current owners.
func CanManageMembership(actorRole, targetRole, newRole string) bool {
	if !Can(actorRole, PermMemberManage) {
		return false
	}
	if targetRole == RoleOwner && actorRole != RoleOwner {
		return false
	}
	if newRole == RoleOwner && actorRole != RoleOwner {
		return false
	}
	return true
}
