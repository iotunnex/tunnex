import { createTunnexClient, type components } from "@tunnex/shared";

// One typed client for the whole SPA (same-origin; the session cookie rides along).
export const api = createTunnexClient("/");

// The CSRF guard only requires this header to be PRESENT on state-changing
// requests that carry the session cookie — a value a cross-site form can't set.
export const CSRF = { "X-Tunnex-CSRF": "1" };

export type AuthUser = components["schemas"]["AuthUser"];
export type Meta = components["schemas"]["Meta"];
export type Org = components["schemas"]["Organization"];
export type Node = components["schemas"]["Node"];
export type Device = components["schemas"]["Device"];
export type OrgOverview = components["schemas"]["OrgOverview"];
export type Member = components["schemas"]["Member"];
export type Role = Member["role"];
export type SsoConfigView = components["schemas"]["SsoConfigView"];

// apiErrorMessage pulls the human message out of the standard error envelope.
export function apiErrorMessage(error: unknown, fallback: string): string {
  const e = error as { error?: { message?: string } } | undefined;
  return e?.error?.message ?? fallback;
}
