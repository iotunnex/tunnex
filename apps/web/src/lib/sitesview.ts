import type { Node, Site, SiteSubnet } from "./api";
import { can } from "./rbac";
import type { Role } from "./api";
import { policyHealthBadge, type HealthBadge } from "./healthview";
import { relativeAge } from "./format";

// sitesview — PURE, electron-free view-models for the Sites page (S8.3 Slice 2). The page is a thin
// render over these; the render-floor law binds here — every field a card shows traces to a WIRE value
// (a real Node/Site/SiteSubnet property), nothing derived that the backend didn't produce, nothing
// animated. The hub designation is READ from node.is_site_hub (backend-derived, the D2 overrule — the UI
// never re-elects), health is READ from policyHealthBadge (the S7.4b/S8.2 kinds), and a site's gateways
// are a LIST (CH: many nodes → one site; the UI never assumes one-gateway-per-site).

// ── edition + RBAC gate ──────────────────────────────────────────────────────────────
// The Sites PAGE is enterprise-gated (D1/D5): site-to-site governance is the enterprise value. Within
// enterprise, ANY member sees the read-only topology (canView); mutating needs site:manage + a verified
// email (mirrors the server). A member (no site:manage) sees topology but NOT the pending queue (D5:
// the queue is an action surface — visible-but-inert is the B6 cousin).
export interface SiteGate {
  isEnterprise: boolean;
  canView: boolean; // enterprise member+ → read-only topology
  canManage: boolean; // owner/admin + verified → mutations + queue
}

export function siteGate(input: { role: Role | undefined; emailVerified: boolean; edition: string | undefined }): SiteGate {
  const isEnterprise = input.edition === "enterprise";
  return {
    isEnterprise,
    canView: isEnterprise, // any enterprise member sees the topology (read-only)
    canManage: isEnterprise && input.emailVerified && can(input.role, "site:manage"),
  };
}

// sitesView decides the page's top-level render. No "member_gate" — unlike Access, a member SEES the
// topology (D5 read-only), so the only non-body states are load/retry/upsell.
export type SitesViewState = "loading" | "load_retry" | "upsell" | "body";

export function sitesView(i: { editionReady: boolean; loadError: boolean; isEnterprise: boolean }): SitesViewState {
  if (i.loadError) return "load_retry";
  if (!i.editionReady) return "loading";
  if (!i.isEnterprise) return "upsell";
  return "body";
}

// ── topology assembly (the wire-truth join) ──────────────────────────────────────────
export interface SubnetView {
  id: string;
  cidr: string;
  status: SiteSubnet["status"]; // pending | approved — rendered as the real state, never assumed approved
}

export interface GatewayView {
  id: string;
  name: string;
  status: Node["status"]; // active | revoked
  isHub: boolean; // READ from node.is_site_hub (backend election), never recomputed here
  health: HealthBadge | null; // null = healthy (no badge); otherwise the S7.4b/S8.2 kind badge
  maxPolicyVersion: number | null; // reported max; null = never reported (below-ceiling — CW, Slice 3 uses it)
  agentVersion: string;
  lastSeenAt: string | null; // S8.4 rider (VERIFY-0): the freshness fact the Devices page already renders
}

// GATEWAY_OFFLINE_MS: past this staleness a gateway reads OFFLINE. ~3 missed status reports (30s cadence).
export const GATEWAY_OFFLINE_MS = 90_000;

// gatewayLiveness (S8.4 rider) renders the FACT (last-seen age) and INFERS offline from a threshold — closing
// VERIFY-0's dead-gateway-renders-healthy hole on the site surface. It reads the SAME node.last_seen_at the
// Devices page already shows; no new signal, no third health vocabulary — the offline flag styles via the
// existing badge system. PURE.
export function gatewayLiveness(lastSeenAt: string | null | undefined, nowMs: number): { lastSeen: string; offline: boolean } {
  if (!lastSeenAt) {
    return { lastSeen: "never connected", offline: true };
  }
  const t = Date.parse(lastSeenAt);
  if (Number.isNaN(t)) {
    return { lastSeen: "unknown", offline: true };
  }
  return { lastSeen: relativeAge(lastSeenAt), offline: nowMs - t > GATEWAY_OFFLINE_MS };
}

export interface SiteCard {
  id: string;
  name: string;
  // A LIST, never a scalar (CH probe target): a site's gateways are all nodes bound to it. v1 binds one,
  // so this is usually length 1 (or 0 when no gateway is bound yet), but the shape does not foreclose HA.
  gateways: GatewayView[];
  subnets: SubnetView[];
}

// ── mutation-surface decisions (Slice 3, all PURE) ───────────────────────────────────

// crossesMultiSiteThreshold — the CW confirm's ACTION-ORDERING gate. The cross-site upgrade warning fires
// at the ONE crossing: approving a subnet that takes the org from single-site-routable (≤1 site with an
// approved subnet, so NO routes compile) to multi-site-routable (≥2, so hub-and-spoke routes compile and
// the artifact bumps to v5). That happens iff THIS site has no approved subnet yet AND exactly ONE OTHER
// site already does (1 → 2). A first site's first approval (0 others) does not cross; a 3rd-site approval
// when already multi-site (≥2 others) does not newly cross (v5 already active). PURE.
export function crossesMultiSiteThreshold(approvingSiteId: string, approvedCountBySite: Record<string, number>): boolean {
  if ((approvedCountBySite[approvingSiteId] ?? 0) > 0) return false; // site already contributes routes
  const otherSitesWithApproved = Object.entries(approvedCountBySite).filter(([id, c]) => id !== approvingSiteId && c > 0).length;
  return otherSitesWithApproved === 1; // was single-site-routable, becomes multi-site — the crossing
}

// subCeilingGateways — the gateways the CW confirm NAMES: those whose reported max policy version is below
// the server ceiling. Absence (null — a pre-CW/pre-upgrade agent that never reported) counts as BELOW (the
// S7.5.3 absence-is-not-compliance rule; those are the very gateways the warning exists for). PURE.
export function subCeilingGateways(gateways: { id: string; name: string; maxPolicyVersion: number | null }[], ceiling: number): { id: string; name: string }[] {
  return gateways.filter((g) => (g.maxPolicyVersion ?? 0) < ceiling).map((g) => ({ id: g.id, name: g.name }));
}

// nameMatchesExactly — the delete-site name-typed ceremony (D4, the S4.5 one-time grain): the typed value
// must EQUAL the site's name exactly. The Delete button stays dead until this is true.
export function nameMatchesExactly(typed: string, siteName: string): boolean {
  return typed === siteName;
}

// disjointRefusal — the D3 VERBATIM refusal: on a `subnet_not_disjoint` 409, return the API's own message
// (it names the overlap_class + colliding range). Returns null for any other error so the caller shows its
// generic message. NO client-side disjointness re-computation (the comparison-set law's UI corollary — one
// validator, never a second copy in JS). PURE.
export function disjointRefusal(err: unknown): string | null {
  const e = err as { error?: { code?: string; message?: string } } | undefined;
  if (e?.error?.code === "subnet_not_disjoint") return e.error.message ?? "This subnet overlaps an existing range; approval refused.";
  return null;
}

// ── subnet-removal DNS preview (S8.4 F4, all PURE) ───────────────────────────────────────────────────
// ipv4ToInt parses a dotted-quad to a uint32, or null if it is not a valid IPv4 literal.
export function ipv4ToInt(ip: string): number | null {
  const parts = ip.trim().split(".");
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p)) return null;
    const b = Number(p);
    if (b > 255) return null;
    n = n * 256 + b;
  }
  return n >>> 0;
}

// forwardsInSubnet NAMES the DNS forwards whose resolver lives inside cidr — the ADVISORY preview for the
// subnet-removal confirm ("removing this also removes N forwards"). NOT an enforcement check: the server
// sweeps authoritatively in the same tx (RemoveSubnet); this only tells the admin what that sweep will do.
// Anything it can't parse as IPv4 is excluded (the server stays the truth). Site subnets are IPv4-only.
export function forwardsInSubnet(forwards: { domain: string; resolver_ip: string }[], cidr: string): string[] {
  const [base, bitsStr] = cidr.split("/");
  const bits = Number(bitsStr);
  const baseInt = ipv4ToInt(base ?? "");
  if (baseInt === null || !Number.isInteger(bits) || bits < 0 || bits > 32) return [];
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
  const net = (baseInt & mask) >>> 0;
  return forwards.filter((f) => {
    const ipInt = ipv4ToInt(f.resolver_ip);
    return ipInt !== null && ((ipInt & mask) >>> 0) === net;
  }).map((f) => f.domain);
}

// assembleTopology joins sites + their subnets + the nodes list into render-ready cards. PURE. A site's
// gateways = the nodes whose site_id is this site (the D2/CH join). Everything a card shows is a wire
// field; the only computation is the join + the health-badge projection (itself pure).
export function assembleTopology(sites: Site[], subnetsBySite: Record<string, SiteSubnet[]>, nodes: Node[]): SiteCard[] {
  return sites.map((s) => ({
    id: s.id,
    name: s.name,
    gateways: nodes
      .filter((n) => n.site_id === s.id)
      .map((n) => ({
        id: n.id,
        name: n.name,
        status: n.status,
        isHub: n.is_site_hub === true,
        health: policyHealthBadge(n),
        maxPolicyVersion: n.max_policy_version ?? null,
        agentVersion: n.agent_version,
        lastSeenAt: n.last_seen_at ?? null,
      })),
    subnets: (subnetsBySite[s.id] ?? []).map((ss) => ({ id: ss.id, cidr: ss.cidr, status: ss.status })),
  }));
}
