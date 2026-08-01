// S14.4 — THE LIVE NAV COUNTS. THE STRICTEST DATA SURFACE IN THE PRODUCT, AND HERE IS WHY.
//
//   A WRONG COUNT ON A PAGE THE USER *OPENED* IS A BUG THEY MIGHT NOTICE.
//   A WRONG COUNT IN *PERMANENT CHROME* IS FURNITURE.
//
// The user did not navigate to the nav, did not ask it a question, and has no moment of expectation against
// which to check its answer. It is simply always there, and always believed — on every screen, all day. That
// is why absent-never-zero is STRICTER here than anywhere else in the app.
//
//   ABSENT UNTIL LOADED. ABSENT ON FAILURE. NEVER `0`. NEVER A REMEMBERED NUMBER.
//
// A nav badge reading `0 DOWN` because a fetch failed is the reassuring-empty defect in the one place every
// user looks.

/**
 * A count that may be UNKNOWN, and the type makes the unknown unavoidable.
 *
 * `number | null` would let a caller write `count ?? 0` — the exact defect — in one keystroke, and the result
 * would typecheck and look reasonable. A tagged state forces the caller to say what it renders when the answer
 * is not a number, which is the decision this whole surface is about.
 */
export type NavCount =
  { state: "loading" } | { state: "failed" } | { state: "ok"; value: number };

export const LOADING: NavCount = { state: "loading" };
export const FAILED: NavCount = { state: "failed" };
export const ok = (value: number): NavCount => ({ state: "ok", value });

/**
 * What a nav badge renders for a count. `null` means RENDER NOTHING — not "render an empty string", not
 * "render a dash", and certainly not zero.
 *
 * The DESTINATION is never affected: S14.2's rule still binds, so the link and its label are always present.
 * Only the badge is conditional.
 */
export function badgeText(c: NavCount): string | null {
  return c.state === "ok" ? String(c.value) : null;
}

/**
 * The gateway badge — `3/7` — needs BOTH numbers, so it is the case where a partial answer is most tempting.
 *
 * ⛔ IF EITHER SIDE IS UNKNOWN THE WHOLE BADGE IS ABSENT. Rendering `?/7` or `3/?` would be worse than nothing:
 * it asserts one half as fact while implying the other is momentarily missing, when in truth the reader has no
 * way to know which half is real.
 */
export function gatewayBadgeText(
  online: NavCount,
  total: NavCount,
): string | null {
  if (online.state !== "ok" || total.state !== "ok") return null;
  return `${online.value}/${total.value}`;
}

/**
 * ⛔ THE Loaded<T> -> NavCount MAPPING, EXTRACTED SO IT CAN BE TESTED.
 *
 * FOUND BY A MUTATION THAT PASSED. The pure `badgeText`/`gatewayBadgeText` layer was gated and the WIRING was
 * not: rewriting `sites.ok ? ok(len) : FAILED` into `ok(sites.ok ? len : 0)` — the classic route to a false
 * count — went green, because no test looked at the hook that performs the mapping.
 *
 * The three-layer shape this project already uses says the DECISION must be pure and unit-tested, and the
 * component must only render it. This mapping WAS the decision, and it was living in a `useEffect`.
 */
export function countFrom<T>(
  res: { ok: true; data: T } | { ok: false },
  project: (t: T) => number,
): NavCount {
  // `.data` is unreachable without narrowing `.ok`, so there is no branch in which a failure can produce a
  // number — the Loaded<T> contract doing its job one layer further out.
  return res.ok ? ok(project(res.data)) : FAILED;
}

/** Every count starts unknown. There is no zero-valued initial state to leak. */
export interface NavCounts {
  gatewaysOnline: NavCount;
  gatewaysTotal: NavCount;
  sites: NavCount;
  devices: NavCount;
}

export const INITIAL_NAV_COUNTS: NavCounts = {
  gatewaysOnline: LOADING,
  gatewaysTotal: LOADING,
  sites: LOADING,
  devices: LOADING,
};

/**
 * How often the counts refresh, in ms.
 *
 * Ruled: refresh on ROUTE CHANGE plus a SLOW interval. A static count goes stale the moment anything changes
 * and becomes a remembered number wearing a live badge; a fast poll is four requests on a timer for a number
 * the user glances at. Sixty seconds is the compromise, and route-change covers the case that actually matters
 * — the user just did something and navigated.
 */
export const NAV_COUNT_REFRESH_MS = 60_000;
