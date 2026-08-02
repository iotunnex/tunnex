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

// ── THE ADDRESS-SPACE MAP ───────────────────────────────────────────────────────────────────────────────
//
// FOUNDER-OVERRIDDEN: this panel was CUT in the S14.7 commit-one and is now ruled back in. The cut had two
// reasons and BOTH ARE REAL DEFECTS IN THE WIREFRAME, so the panel is built with both closed rather than
// reproduced:
//
//   ① A /24 LIT A WHOLE /16 CELL. `alloc = { 20: 'pending' }` in the handoff marks 10.20.0.0/24 by its
//     SECOND OCTET, so a 256-address LAN paints a 65,536-address block. Our own data has three /24s. CLOSED
//     by a third cell state: `partial` renders INSET, so "some of this /16" cannot be read as "all of it".
//
//   ② THE GRID DOMAIN WAS HARD-CODED 10.0.0.0/8. A customer on 172.16/12 or 192.168/16 would get a map with
//     their ranges INVISIBLE — reassuring-empty on the panel whose entire job is showing what is routed. Our
//     seeded data is all 10.x, so this would have looked perfect and been wrong for someone else. CLOSED by
//     mapping each RFC1918 block that actually contains ranges, plus an explicit OFF-MAP list so a range can
//     never vanish by being outside the drawing.
//
// Everything here is pure arithmetic on the served `ranges` array. Nothing is invented.

/** A drawable address block. `cellPrefix` is what ONE cell represents. */
export type Block = {
  key: string;
  label: string;
  /** Network address, uint32. */
  base: number;
  prefix: number;
  cellPrefix: number;
  cells: number;
  cols: number;
};

// The three RFC1918 blocks, each with a cell size that keeps the grid readable: /8 and /12 divide into /16s,
// and 192.168/16 — which is only ONE /16 — divides into /24s or it would be a single cell.
export const BLOCKS: Block[] = [
  { key: "10", label: "10.0.0.0/8", base: 0x0a000000, prefix: 8, cellPrefix: 16, cells: 256, cols: 32 },
  { key: "172", label: "172.16.0.0/12", base: 0xac100000, prefix: 12, cellPrefix: 16, cells: 16, cols: 16 },
  { key: "192", label: "192.168.0.0/16", base: 0xc0a80000, prefix: 16, cellPrefix: 24, cells: 256, cols: 32 },
];

export type CellState = "free" | "partial" | "full";
export type CellStatus = "approved" | "pending";

export type Cell = {
  index: number;
  state: CellState;
  status: CellStatus;
  /** The CIDRs that lit this cell — the tooltip/label source, so a cell can always say why it is on. */
  cidrs: string[];
};

export type BlockMap = {
  block: Block;
  /** LIT cells only. The free ones are drawn from `block.cells`, not carried per-cell. */
  lit: Cell[];
  /** Exact fraction of the block's addresses covered by APPROVED ranges. Not a cell count. */
  utilised: number;
  approvedCount: number;
  pendingCount: number;
};

/** Parses a CIDR into a uint32 network address and prefix, or null. Reuses canonicalCidr's validation. */
export function parseCidr(raw: string): { addr: number; prefix: number } | null {
  const canonical = canonicalCidr(raw);
  if (canonical === null) return null;
  const [host, prefixText] = canonical.split("/");
  const b = host.split(".").map(Number);
  return {
    addr: ((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0,
    prefix: Number(prefixText),
  };
}

function inBlock(addr: number, prefix: number, block: Block): boolean {
  if (prefix < block.prefix) return false;
  const mask = block.prefix === 0 ? 0 : (0xffffffff << (32 - block.prefix)) >>> 0;
  return ((addr & mask) >>> 0) === block.base;
}

/**
 * mapAddressSpace lays approved and pending CIDRs onto the RFC1918 blocks they fall in.
 *
 * Only blocks that CONTAIN something are returned — an empty 172.16/12 grid on an org that has never used it
 * is 16 dark squares saying nothing. Anything outside all three comes back in `offMap`, which is the whole
 * point: a range must never disappear by being un-drawable.
 */
export function mapAddressSpace(
  approved: string[],
  pending: string[] = [],
): { blocks: BlockMap[]; offMap: string[]; unparseable: string[] } {
  const offMap: string[] = [];
  const unparseable: string[] = [];
  const acc = new Map<string, { cells: Map<number, Cell>; approvedAddrs: number; approvedCount: number; pendingCount: number }>();

  const place = (cidr: string, status: CellStatus) => {
    const parsed = parseCidr(cidr);
    if (parsed === null) {
      // NOT silently skipped. An unrenderable string is a fact about the data, and the panel says so.
      unparseable.push(cidr);
      return;
    }
    const block = BLOCKS.find((b) => inBlock(parsed.addr, parsed.prefix, b));
    if (block === undefined) {
      offMap.push(cidr);
      return;
    }
    let entry = acc.get(block.key);
    if (entry === undefined) {
      entry = { cells: new Map(), approvedAddrs: 0, approvedCount: 0, pendingCount: 0 };
      acc.set(block.key, entry);
    }
    if (status === "approved") {
      entry.approvedCount += 1;
      entry.approvedAddrs += Math.pow(2, 32 - parsed.prefix);
    } else entry.pendingCount += 1;

    const firstCell = Math.floor((parsed.addr - block.base) / Math.pow(2, 32 - block.cellPrefix));
    if (parsed.prefix > block.cellPrefix) {
      // ⛔ FINER THAN ONE CELL. This is defect ① — the case the handoff drew as a full cell.
      upsert(entry.cells, firstCell, "partial", status, cidr);
      return;
    }
    // Coarser than or equal to a cell: it fills several, wholly.
    const span = Math.pow(2, block.cellPrefix - parsed.prefix);
    for (let i = 0; i < span && firstCell + i < block.cells; i++)
      upsert(entry.cells, firstCell + i, "full", status, cidr);
  };

  approved.forEach((c) => place(c, "approved"));
  pending.forEach((c) => place(c, "pending"));

  const blocks: BlockMap[] = [];
  for (const block of BLOCKS) {
    const entry = acc.get(block.key);
    if (entry === undefined) continue;
    blocks.push({
      block,
      lit: [...entry.cells.values()].sort((a, b) => a.index - b.index),
      utilised: entry.approvedAddrs / Math.pow(2, 32 - block.prefix),
      approvedCount: entry.approvedCount,
      pendingCount: entry.pendingCount,
    });
  }
  return { blocks, offMap, unparseable };
}

function upsert(
  cells: Map<number, Cell>,
  index: number,
  state: CellState,
  status: CellStatus,
  cidr: string,
) {
  const existing = cells.get(index);
  if (existing === undefined) {
    cells.set(index, { index, state, status, cidrs: [cidr] });
    return;
  }
  existing.cidrs.push(cidr);
  // FULL beats PARTIAL, and APPROVED beats PENDING. Both for the same reason: the stronger claim is the one
  // a reader must not miss. Disjointness is server-enforced so this is defensive, but a cell that is both
  // must not render as the weaker of the two.
  if (state === "full") existing.state = "full";
  if (status === "approved") existing.status = "approved";
}

/** "0.8% of /8" — the handoff's phrasing, computed rather than fixed. */
export function utilisationLabel(m: BlockMap): string {
  const pct = m.utilised * 100;
  const shown = pct === 0 ? "0" : pct < 0.1 ? "<0.1" : pct.toFixed(1);
  return `${shown}% of /${m.block.prefix}`;
}

/** "4 approved · 1 pending" — the counting line under the bar. Singular/plural, and pending omitted at zero. */
export function allocationLabel(m: BlockMap): string {
  const approved = `${m.approvedCount} approved`;
  return m.pendingCount === 0
    ? approved
    : `${approved} · ${m.pendingCount} pending`;
}
