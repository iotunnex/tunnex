import type { Node } from "./api";
import { policyHealthBadge, type HealthBadge } from "./healthview";

// gatewaysview — PURE view-models for the Gateways screen (S14.6 slice 2). Electron-free, no React.
//
// ⛔ THE HEALTH GROUPING IS THE SCREEN'S CONTENT, and it exists because `Fleet risk` was CUT.
//
// The handoff's left column is a bubble plot: agent version × peer load, bubble size = peers, colour =
// health. It was ruled out at epic open — *"risk scoring is an unbuilt Tier-3 name in the competitive
// ledger. Replaced by a HEALTH-GROUPED GATEWAY LIST — same information, legible at a glance."*
//
// This is that replacement, and the ruling is right for a reason worth restating: a scatter plot answers
// "which gateway is an outlier"; an operator at 3am is asking "WHAT IS BROKEN". Grouping answers the second
// directly, scales to a 200-gateway fleet, and needs no axis nobody calibrated.

export type GatewayGroupKey = "degraded" | "healthy" | "revoked";

export interface GatewayRow {
  id: string;
  name: string;
  status: Node["status"];
  health: HealthBadge | null;
  /** The OpenVPN refuse-loudly kind — a DIFFERENT axis from policy health (S9.1 4d). */
  ovpnHealth: string | null;
  agentVersion: string;
  siteId: string | null;
  isHub: boolean;
  lastSeenAt: string | null;
}

export interface GatewayGroup {
  key: GatewayGroupKey;
  rows: GatewayRow[];
}

/**
 * Project nodes into rows.
 *
 * ⛔ A REVOKED GATEWAY CARRIES NO HEALTH BADGE. `revoked` IS its state, and a degradation badge beside it
 * would have the row asserting two things at once — the WF-S11-10 finding, preserved here rather than
 * re-derived. `policyHealthBadge` is only consulted for an active node.
 */
export function toGatewayRow(n: Node): GatewayRow {
  return {
    id: n.id,
    name: n.name,
    status: n.status,
    health: n.status === "revoked" ? null : policyHealthBadge(n),
    ovpnHealth: n.status === "revoked" ? null : (n.ovpn_health ?? null),
    agentVersion: n.agent_version,
    siteId: n.site_id ?? null,
    isHub: n.is_site_hub === true,
    lastSeenAt: n.last_seen_at ?? null,
  };
}

/**
 * Group into DEGRADED → HEALTHY → REVOKED, in that order.
 *
 * ⛔ DEGRADED FIRST, ALWAYS. The epic's own rule for an ACTING surface: *"the primary action and the thing
 * that is wrong come first."* A fleet list sorted by name makes an operator scan for the problem; a fleet
 * list that leads with the problem has already answered them.
 *
 * ⚠ AND A GATEWAY WITH AN OVPN FAULT COUNTS AS DEGRADED even when its POLICY health is clean. They are
 * different axes (S9.1 4d) and an opted-in gateway that is not serving must not sit in the healthy group
 * because the other axis happens to be fine. Reading only `health` here would put it there.
 */
export function groupGateways(nodes: Node[]): GatewayGroup[] {
  const rows = nodes.map(toGatewayRow);
  const degraded = rows.filter(
    (r) => r.status !== "revoked" && (r.health !== null || r.ovpnHealth !== null),
  );
  const healthy = rows.filter(
    (r) => r.status !== "revoked" && r.health === null && r.ovpnHealth === null,
  );
  const revoked = rows.filter((r) => r.status === "revoked");
  return [
    { key: "degraded", rows: degraded },
    { key: "healthy", rows: healthy },
    { key: "revoked", rows: revoked },
  ];
}

export type GatewayFilter = "all" | "healthy" | "degraded";

/**
 * The header's filter chips, with their counts.
 *
 * ⛔ THE COUNTS ARE DERIVED FROM THE SAME GROUPING THE TABLE RENDERS. The handoff shows `All (7)`,
 * `Healthy (3)`, `Degraded (4)` — three numbers that must add up, and would not if the chips counted one way
 * and the list grouped another. ONE derivation, two renderings.
 *
 * ⚠ `all` INCLUDES REVOKED and the other two do not, which is why `healthy + degraded` can be less than
 * `all`. That is honest — a revoked gateway is neither — but it looks like an arithmetic bug unless the
 * screen says so, so the chip row carries the revoked count separately when there is one.
 */
export function gatewayFilterCounts(nodes: Node[]): {
  all: number;
  healthy: number;
  degraded: number;
  revoked: number;
} {
  const g = groupGateways(nodes);
  const by = (k: GatewayGroupKey) =>
    g.find((x) => x.key === k)?.rows.length ?? 0;
  return {
    all: nodes.length,
    healthy: by("healthy"),
    degraded: by("degraded"),
    revoked: by("revoked"),
  };
}

/** Apply a filter chip. `all` keeps revoked rows; the other two never contain them by construction. */
export function applyGatewayFilter(
  groups: GatewayGroup[],
  filter: GatewayFilter,
): GatewayGroup[] {
  if (filter === "all") return groups;
  return groups.filter((g) => g.key === filter);
}
