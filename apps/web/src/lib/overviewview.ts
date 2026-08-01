import type { Loaded } from "./api";
import type { NavCount } from "./navcounts";
import { FAILED, LOADING, ok } from "./navcounts";

// S14.4 — the Overview's PURE decisions, extracted so the tier tests them without a DOM and the screen has
// nothing to decide beyond rendering.

/**
 * A stat card's state, reusing the nav-count union deliberately.
 *
 * SAME PROBLEM, SAME TYPE: "we have not learned this yet", "we failed to learn it", and "the answer is a
 * number" are the three states everywhere in this product, and `number | null` collapses the first two into a
 * value the caller can `?? 0` away in one keystroke.
 */
export type StatState = NavCount;

/** Lift a `Loaded<T>` plus a projection into a stat state. `null` load = still loading. */
export function statFrom<T>(
  res: Loaded<T> | null,
  project: (t: T) => number,
): StatState {
  if (res === null) return LOADING;
  return res.ok ? ok(project(res.data)) : FAILED;
}

/** What a stat card renders. `null` = render the unavailable/loading treatment, NEVER a number. */
export function statText(s: StatState): string | null {
  return s.state === "ok" ? String(s.value) : null;
}

/**
 * ⛔ IS THIS A FRESH ORG, OR DID WE FAIL TO FIND OUT?
 *
 * The get-started empty state is only honest when we KNOW the org is empty. Showing it because a fetch failed
 * would tell a founder with a working fleet that they have nothing — the reassuring-empty defect wearing an
 * onboarding hat, and considerably more alarming than a blank panel.
 */
export function isFreshOrg(
  nodes: StatState,
  devices: StatState,
  members: StatState,
): boolean {
  return (
    nodes.state === "ok" &&
    devices.state === "ok" &&
    members.state === "ok" &&
    nodes.value === 0 &&
    devices.value === 0
  );
}

export interface GatewayRow {
  id: string;
  name: string;
  /** The badge label from the ONE health interpreter — never a second copy of the vocabulary. */
  label: string;
  tone: "ok" | "warn" | "danger" | "neutral";
}

/**
 * Sort order for the gateway health list: UNHEALTHY FIRST, then by name.
 *
 * The list shows ALL gateways, not only unhealthy ones (ruled). "Nothing is wrong" and "we have no gateways"
 * must not render identically — and a list that hides healthy rows makes the empty case ambiguous, which is
 * the same defect one level up from the one this screen is built to avoid.
 */
export function sortGateways(rows: GatewayRow[]): GatewayRow[] {
  const rank = { danger: 0, warn: 1, neutral: 2, ok: 3 } as const;
  return [...rows].sort(
    (a, b) => rank[a.tone] - rank[b.tone] || a.name.localeCompare(b.name),
  );
}
