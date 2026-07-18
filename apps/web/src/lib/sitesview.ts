import type { Node, Site, SiteSubnet } from "./api";
import { can } from "./rbac";
import type { Role } from "./api";
import { policyHealthBadge, type HealthBadge } from "./healthview";

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
}

export interface SiteCard {
  id: string;
  name: string;
  // A LIST, never a scalar (CH probe target): a site's gateways are all nodes bound to it. v1 binds one,
  // so this is usually length 1 (or 0 when no gateway is bound yet), but the shape does not foreclose HA.
  gateways: GatewayView[];
  subnets: SubnetView[];
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
      })),
    subnets: (subnetsBySite[s.id] ?? []).map((ss) => ({ id: ss.id, cidr: ss.cidr, status: ss.status })),
  }));
}
