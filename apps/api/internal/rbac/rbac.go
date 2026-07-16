// Package rbac is the single source of the authorization model: what each role
// may do. Call sites ask Can(role, permission) — never `role == "admin"` — so
// when a role is added or a permission moves, only this file changes.
package rbac

import "sort"

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
	// Zero Trust policy (S7.1, enterprise). PermPolicyView reads the model
	// (groups/resources/rules/mode); PermPolicyManage mutates it AND flips the
	// enforcement mode — disabling re-opens the mesh, so it is the same
	// (owner/admin) capability, deliberately not a members-level read.
	PermPolicyView   Permission = "policy:view"
	PermPolicyManage Permission = "policy:manage"
	// PermDeviceApprove governs device posture (S7.3, enterprise): approving/rejecting a
	// pending device AND flipping the org device-approval gate. A distinct capability from
	// policy:manage because device-trust is its own governance domain (an org may require
	// device approval without Zero Trust policy, or vice versa) — but at the SAME
	// owner/admin grain, since approving a device GRANTS network access (security-sensitive,
	// above org:update).
	PermDeviceApprove Permission = "device:approve"
	// PermDeviceHealthManage governs device HEALTH posture (S7.5.3, enterprise):
	// configuring the org's per-check posture requirements (warn/require). Named per
	// feature — deliberately NOT a reuse of PermDeviceApprove: approval (known-device)
	// and health (healthy-device) are orthogonal governance axes, and reusing the
	// approve perm would silently grant posture control to every existing approver.
	// Same owner/admin grain (a require-mode check can disconnect devices). The
	// self-REPORT endpoint carries no perm: it is device-owner-authed in the service.
	PermDeviceHealthManage Permission = "device_health:manage"
	// PermMfaManage governs ORG-LEVEL MFA (S7.5.5, enterprise): the enforce toggle + admin-reset
	// of a member's MFA. Named per feature (NOT a policy/member reuse) — MFA governance is its own
	// axis, and admin-reset is an account-takeover-adjacent power (disenroll-only, audited,
	// target-notified). Owner/admin grain (mandating MFA / resetting a factor is security-sensitive).
	// Self-service enrollment carries NO perm — it is user-owned (any authenticated user).
	PermMfaManage Permission = "mfa:manage"
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
// render). The server is authoritative; the client copy is UX only. This GRANT
// TABLE is now machine-synced: `make generate-rbac` serializes Policy() to
// apps/web/src/lib/rbac-policy.json (which rbac.ts consumes) and generate-check
// fails the build if they drift — so editing this table can't silently desync
// the client. NOTE: CanManageMembership's relational rules are logic, not data,
// so they are NOT covered by the guard and are still hand-mirrored in rbac.ts.
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
		PermMemberManage:  true,
		PermPolicyView:         true,
		PermPolicyManage:       true,
		PermDeviceApprove:      true,
		PermDeviceHealthManage: true,
		PermMfaManage:          true,
	},
	RoleOwner: {
		PermOrgView:       true,
		PermMemberList:    true,
		PermOrgUpdate:     true,
		PermOrgDelete:     true,
		PermMemberInvite:  true,
		PermMemberManage:  true,
		PermPolicyView:         true,
		PermPolicyManage:       true,
		PermDeviceApprove:      true,
		PermDeviceHealthManage: true,
		PermMfaManage:          true,
	},
}

// Can reports whether a role holds a permission.
func Can(role string, p Permission) bool {
	return rolePermissions[role][p]
}

// Policy returns the role→permissions grant table as sorted string slices — the
// serializable, authoritative form. `make generate-rbac` marshals this to
// apps/web/src/lib/rbac-policy.json, which the client RBAC mirror (lib/rbac.ts)
// consumes; `make generate-check` then fails the build if the committed JSON has
// drifted from this table. So this map is the ONE source of truth for grants and
// the client can no longer silently diverge. (canManageMembership's relational
// rules are logic, not data, and remain mirrored by hand.)
func Policy() map[string][]string {
	out := make(map[string][]string, len(rolePermissions))
	for role, perms := range rolePermissions {
		list := make([]string, 0, len(perms))
		for p := range perms {
			list = append(list, string(p))
		}
		sort.Strings(list)
		out[role] = list
	}
	return out
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
	case PermOrgView, PermMemberList, PermPolicyView:
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
