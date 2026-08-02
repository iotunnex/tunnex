import type { DNSForward, Site, SiteSubnet } from "./api";

// ── S14.7 — ROUTED RANGES, THE VIEW MODEL ───────────────────────────────────────────────────────────────
//
// `/routed-ranges` serves a DEVICE-FACING projection: approved CIDRs, canonical + sorted, and the DNS
// forwards reachable through them. It does NOT serve `site_id`. Attribution is therefore JOINED CLIENT-SIDE
// from a per-site `listSiteSubnets` fan-out — see `attributionState` for what that costs and when it stops
// being acceptable.
//
// Everything here is pure. The screen does the fetching; this file decides what the screen is allowed to
// claim, and — more to the point — what it must NOT claim while the answer is still in flight.

// ⛔ THE IN-FLIGHT STATE IS A FIRST-CLASS VALUE, NOT A FALSY DEFAULT.
//
// The ranges table renders IMMEDIATELY from one request; the SITE column fills in as the N-site fan-out
// lands. If in-flight rendered as blank it would be indistinguishable from "we looked and found no site" —
// the reassuring-empty shape, at row level, on the column the screen exists to add.
//
// So the union has four arms and NONE of them is the absence of the others.
export type Attribution =
  | { kind: "site"; siteId: string; siteName: string }
  | { kind: "loading" }
  // The fan-out failed for the site that would have owned this row. NOT "no site" — "we could not ask".
  | { kind: "unknown" }
  // We asked every site, every answer came back, and no approved subnet matches this range.
  | { kind: "unmatched" };

export type RangeRow = {
  /** The canonical CIDR exactly as the API sorted and emitted it — the string a device receives. */
  range: string;
  attribution: Attribution;
};

// canonicalCidr masks host bits off an IPv4 CIDR, or returns null if it is not one.
//
// ⛔ WHY THIS EXISTS WHEN THE JOIN IS ALREADY SAFE. `site_subnets.cidr` is the POSTGRES `cidr` type, which
// REJECTS host bits at the column — measured in S14.7 §2 with a real INSERT into the real table, not with a
// cast. So both sides are already canonical and a naive string join would work today.
//
// It exists because the REASON it works lives in a column type two layers away, in a different language, in
// a different repository directory. A migration to `text` would break the attribution join SILENTLY: rows
// would render `unmatched`, which is a legible-looking state that means something else entirely. Normalising
// here makes the join depend on nothing but itself.
//
// `routedranges.go:211` already sets this precedent server-side (`ss.Cidr.Masked() == cidr.Masked()`).
export function canonicalCidr(raw: string): string | null {
  const trimmed = raw.trim();
  const slash = trimmed.lastIndexOf("/");
  if (slash < 0) return null;
  const host = trimmed.slice(0, slash);
  const prefixText = trimmed.slice(slash + 1);
  if (!/^\d{1,2}$/.test(prefixText)) return null;
  const prefix = Number(prefixText);
  if (prefix > 32) return null;

  const octets = host.split(".");
  if (octets.length !== 4) return null;
  const bytes: number[] = [];
  for (const octet of octets) {
    if (!/^\d{1,3}$/.test(octet)) return null;
    const value = Number(octet);
    if (value > 255) return null;
    bytes.push(value);
  }

  // Mask as a 32-bit unsigned value. `>>> 0` because JS bitwise ops yield SIGNED int32, so a /0 or /1 mask
  // on 10.x would otherwise come back negative and every octet would be wrong.
  const addr =
    ((bytes[0] << 24) | (bytes[1] << 16) | (bytes[2] << 8) | bytes[3]) >>> 0;
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  const masked = (addr & mask) >>> 0;
  return `${(masked >>> 24) & 255}.${(masked >>> 16) & 255}.${(masked >>> 8) & 255}.${masked & 255}/${prefix}`;
}

/** One site's fan-out result. `ok:false` is why `unknown` exists as an attribution. */
export type SubnetFetch =
  | { ok: true; siteId: string; subnets: SiteSubnet[] }
  | { ok: false; siteId: string };

/**
 * attributeRanges joins served ranges to the sites that advertise them.
 *
 * `fanOut === null` means the fan-out has not resolved yet — every row is `loading`. That is distinct from
 * `fanOut === []`, which means there are no sites at all, and every row is genuinely `unmatched`.
 */
export function attributeRanges(
  ranges: string[],
  sites: Site[],
  fanOut: SubnetFetch[] | null,
): RangeRow[] {
  if (fanOut === null)
    return ranges.map((range) => ({ range, attribution: { kind: "loading" } }));

  const siteName = new Map(sites.map((s) => [s.id, s.name]));

  // ⛔ ANY failed site poisons the whole NEGATIVE answer, not just its own rows.
  //
  // A range's owner is discovered by finding it in some site's subnet list. If ANY site's list is missing, a
  // range we did not match might be owned by exactly that site — so "no match" is not knowable. `unmatched`
  // is a claim about every site having been asked; degrade to `unknown` if even one was not.
  const anyFailed = fanOut.some((f) => !f.ok);

  const owner = new Map<string, string>();
  for (const fetched of fanOut) {
    if (!fetched.ok) continue;
    for (const subnet of fetched.subnets) {
      // Pending subnets are NOT routed. `/routed-ranges` is approved-only, so a pending subnet can never be
      // the owner of a served range — and attributing one would claim traffic flows where it does not.
      if (subnet.status !== "approved") continue;
      const key = canonicalCidr(subnet.cidr) ?? subnet.cidr;
      // First writer wins; a duplicate CIDR across sites is refused by the disjointness validator at both
      // seams (S8.1 #1), so this is defensive rather than a real branch.
      if (!owner.has(key)) owner.set(key, subnet.site_id);
    }
  }

  return ranges.map((range) => {
    const key = canonicalCidr(range) ?? range;
    const siteId = owner.get(key);
    if (siteId !== undefined)
      return {
        range,
        attribution: {
          kind: "site",
          siteId,
          // A site the fan-out reached but that `listSites` did not return is a real, if unlikely, race.
          // Rendering the id is honest; rendering "Unknown site" would look like the `unknown` arm.
          siteName: siteName.get(siteId) ?? siteId,
        },
      };
    return { range, attribution: { kind: anyFailed ? "unknown" : "unmatched" } };
  });
}

/** The SITE cell's text. Exported so the test asserts the STRING, not a class name. */
export function attributionLabel(a: Attribution): string {
  switch (a.kind) {
    case "site":
      return a.siteName;
    case "loading":
      return "Loading…";
    case "unknown":
      return "Could not load";
    case "unmatched":
      return "No site advertises this";
  }
}

/** Recessive styling for every non-answer, so a row with real attribution is the one that reads loudest. */
export function attributionClass(a: Attribution): string {
  return a.kind === "site" ? "text-ink-body" : "text-slate-400 italic";
}

// ⛔ THE FAN-OUT TRIPWIRE. N sites = N+1 requests, parallel, once per visit. Fine at 20. At ~50 it is nine
// sequential waves against the browser's ~6-per-origin cap — noticeable, still one page load. At 200 it is
// not acceptable.
//
// THE REAL FIX IS `site_id` ON `RoutedRange`, AND IT IS NOT THIS SCREEN'S TO MAKE: `/routed-ranges` is a
// device-facing projection, and adding an org-structure field to it needs a ruling on whether a device
// should learn site topology. Deferred with a named trigger (docs/DEFERRAL-REGISTER.md).
//
// So the threshold is exported and ASSERTED, rather than living in a comment the next reader has to find.
export const FANOUT_TRIPWIRE = 50;

export function fanOutExceedsTripwire(siteCount: number): boolean {
  return siteCount > FANOUT_TRIPWIRE;
}

// ── DNS FORWARDS: THE GATED EMPTY STATE ─────────────────────────────────────────────────────────────────
//
// ⛔ `forwards` IS GATED, AND ITS EMPTINESS MEANS SOMETHING NARROWER THAN IT LOOKS. A forward is returned
// only when its `resolver_ip` falls INSIDE a routed range — the control plane never hands a device a
// resolver it cannot reach (S8.4: "never a SERVFAIL generator").
//
// So empty means "none REACHABLE". It does NOT mean "none configured", and copy that says the latter would
// send an admin to configure a forward that already exists.
export function forwardsEmptyCopy(rangeCount: number): string {
  return rangeCount === 0
    ? "No forwarded zones are reachable, because no ranges are routed yet."
    : "No forwarded zones are currently reachable from a routed range. Zones may well be configured: a resolver that sits outside every routed range is withheld rather than handed over as a dead lookup.";
}

/** Sorted for a stable render; the API does not promise an order on `forwards`. */
export function sortForwards(forwards: DNSForward[]): DNSForward[] {
  return [...forwards].sort(
    (a, b) =>
      a.domain.localeCompare(b.domain) ||
      a.resolver_ip.localeCompare(b.resolver_ip),
  );
}
