import type { Role } from "./api";

// Client-side MIRROR of the server RBAC policy (apps/api/internal/rbac/rbac.go).
// This gates what CONTROLS render — it is UX, never enforcement. Every action is
// still authorized server-side; the UI simply must not offer what the server
// forbids (S4.4 watch-item a). Keep this table in lockstep with the Go one.
const grants: Record<Role, Set<string>> = {
  member: new Set(["org:view", "member:list"]),
  admin: new Set(["org:view", "member:list", "org:update", "member:invite", "member:manage"]),
  owner: new Set(["org:view", "member:list", "org:update", "org:delete", "member:invite", "member:manage"]),
};

export function can(role: Role | undefined, perm: string): boolean {
  return role ? grants[role].has(perm) : false;
}

// canManageMembership mirrors rbac.CanManageMembership: the actor needs
// member:manage, only an owner may manage another owner, and only an owner may
// promote someone TO owner. newRole "" means removal/deactivation (no promotion).
export function canManageMembership(actorRole: Role | undefined, targetRole: Role, newRole: Role | ""): boolean {
  if (!can(actorRole, "member:manage")) return false;
  if (targetRole === "owner" && actorRole !== "owner") return false;
  if (newRole === "owner" && actorRole !== "owner") return false;
  return true;
}
