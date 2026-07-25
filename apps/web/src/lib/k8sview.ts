import type { K8sCluster, K8sService, Role } from "./api";
import { can } from "./rbac";

// k8sview — PURE, electron-free view-models for the Kubernetes page (S10.3). The page is a thin render over
// these. K8s cluster/Service exposure is CONNECTIVITY, so unlike sites it is CORE (all editions) — there is
// NO enterprise gate here; only the GRANT that reaches a Service (Access page) is enterprise. Every rendered
// field traces to a wire value (a real K8sCluster/K8sService property); the FQDN is READ from the server
// (service.fqdn — "copy, don't construct"), never assembled in the client.

// ── RBAC gate ──────────────────────────────────────────────────────────────────────
// canView: any member reads the connectivity surface (org:view — the member-read gate, like ListSites).
// canManage: register/expose/remove need k8s:manage + a verified email (mirrors the server). No edition bit.
export interface K8sGate {
  canView: boolean;
  canManage: boolean;
}

export function k8sGate(input: { role: Role | undefined; emailVerified: boolean }): K8sGate {
  return {
    canView: can(input.role, "org:view"),
    canManage: input.emailVerified && can(input.role, "k8s:manage"),
  };
}

// ── cluster + service assembly (the wire-truth join) ─────────────────────────────────
export interface ServiceRow {
  id: string;
  name: string;
  namespace: string;
  protocol: K8sService["protocol"];
  ports: string; // "any" | "80" | "8000–8100" — a display projection of the wire port_low/port_high
  vip: string;
  fqdn: string; // READ from the server (never constructed here)
}

export interface ClusterCard {
  id: string;
  siteId: string;
  name: string;
  vipRange: string;
  serviceCidr: string;
  dnsZone: string;
  dnsVip: string | null;
  services: ServiceRow[]; // the cluster's LIVE exposed Services
}

// portLabel projects the wire port_low/port_high onto a human range. null/absent both = "any".
export function portLabel(portLow: number | null | undefined, portHigh: number | null | undefined): string {
  if (portLow == null && portHigh == null) return "any";
  if (portLow != null && (portHigh == null || portHigh === portLow)) return String(portLow);
  if (portLow == null) return String(portHigh);
  return `${portLow}–${portHigh}`; // en-dash range
}

function serviceRow(s: K8sService): ServiceRow {
  return {
    id: s.id,
    name: s.name,
    namespace: s.namespace,
    protocol: s.protocol,
    ports: portLabel(s.port_low, s.port_high),
    vip: s.vip,
    fqdn: s.fqdn,
  };
}

// assembleClusters joins the clusters with their org-wide Services (grouped by cluster_id). PURE — the only
// computation is the group-by + the port projection.
export function assembleClusters(clusters: K8sCluster[], services: K8sService[]): ClusterCard[] {
  const byCluster: Record<string, ServiceRow[]> = {};
  for (const s of services) {
    (byCluster[s.cluster_id] ??= []).push(serviceRow(s));
  }
  return clusters.map((c) => ({
    id: c.id,
    siteId: c.site_id,
    name: c.name,
    vipRange: c.vip_range,
    serviceCidr: c.service_cidr,
    dnsZone: c.dns_zone,
    dnsVip: c.dns_vip ?? null,
    services: byCluster[c.id] ?? [],
  }));
}

// serviceFqdnById is the grant-picker's label source: id -> fqdn for a live Service (or null if absent).
export function serviceFqdnById(services: K8sService[], id: string): string | null {
  return services.find((s) => s.id === id)?.fqdn ?? null;
}
