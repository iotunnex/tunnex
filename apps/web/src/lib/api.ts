import { createTunnexClient, type components } from "@tunnex/shared";

// One typed client for the whole SPA (same-origin; the session cookie rides along).
export const api = createTunnexClient("/");

// The X-Tunnex-CSRF header is attached to every unsafe-method request by
// createTunnexClient itself (see packages/shared) — no per-call plumbing.

export type AuthUser = components["schemas"]["AuthUser"];
export type Meta = components["schemas"]["Meta"];
export type Org = components["schemas"]["Organization"];
export type Node = components["schemas"]["Node"];
export type Device = components["schemas"]["Device"];
export type OrgOverview = components["schemas"]["OrgOverview"];
export type Member = components["schemas"]["Member"];
export type Role = Member["role"];
export type SsoConfigView = components["schemas"]["SsoConfigView"];
export type ResizeConflict = components["schemas"]["ResizeConflict"];
export type AuditLogEntry = components["schemas"]["AuditLogEntry"];

// apiErrorMessage pulls the human message out of the standard error envelope.
export function apiErrorMessage(error: unknown, fallback: string): string {
  const e = error as { error?: { message?: string } } | undefined;
  return e?.error?.message ?? fallback;
}

// apiErrorCode pulls the stable machine-readable code out of the error envelope
// (e.g. "org_limit_reached") so callers can branch on it instead of matching prose.
export function apiErrorCode(error: unknown): string | undefined {
  const e = error as { error?: { code?: string } } | undefined;
  return e?.error?.code;
}
