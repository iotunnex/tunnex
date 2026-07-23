import { describe, it, expect } from "vitest";
import {
  assembleTopology,
  crossesMultiSiteThreshold,
  disjointRefusal,
  gatewayOnline,
  nameMatchesExactly,
  siteGate,
  sitesView,
  subCeilingGateways,
} from "../src/lib/sitesview";
import type { HealthBadge } from "../src/lib/healthview";
import type { Node, Site, SiteSubnet } from "../src/lib/api";

const site = (id: string, name: string): Site => ({ id, name, link_transport: "wireguard", created_at: "2026-01-01T00:00:00Z" });
const node = (over: Partial<Node>): Node =>
  ({ id: "n", name: "gw", status: "active", agent_version: "0.1.0", enrolled_at: "2026-01-01T00:00:00Z", ...over }) as Node;
const subnet = (id: string, site_id: string, cidr: string, status: SiteSubnet["status"]): SiteSubnet => ({ id, site_id, cidr, status });

describe("siteGate — enterprise page, member sees topology, manage needs site:manage + verified", () => {
  it("non-enterprise → not viewable (upsell)", () => {
    const g = siteGate({ role: "owner", emailVerified: true, edition: "open" });
    expect(g.isEnterprise).toBe(false);
    expect(g.canView).toBe(false);
    expect(g.canManage).toBe(false);
  });
  it("enterprise member → sees topology (canView) but cannot manage", () => {
    const g = siteGate({ role: "member", emailVerified: true, edition: "enterprise" });
    expect(g.canView).toBe(true);
    expect(g.canManage).toBe(false); // member lacks site:manage
  });
  it("enterprise admin + verified → can manage", () => {
    expect(siteGate({ role: "admin", emailVerified: true, edition: "enterprise" }).canManage).toBe(true);
  });
  it("manage requires a verified email (mirrors the server)", () => {
    expect(siteGate({ role: "owner", emailVerified: false, edition: "enterprise" }).canManage).toBe(false);
  });
});

describe("sitesView — no member_gate (a member SEES the topology)", () => {
  it("load error → retry (never a reassuring empty topology)", () => {
    expect(sitesView({ editionReady: true, loadError: true, isEnterprise: true })).toBe("load_retry");
  });
  it("not ready → loading; non-enterprise → upsell; else body", () => {
    expect(sitesView({ editionReady: false, loadError: false, isEnterprise: false })).toBe("loading");
    expect(sitesView({ editionReady: true, loadError: false, isEnterprise: false })).toBe("upsell");
    expect(sitesView({ editionReady: true, loadError: false, isEnterprise: true })).toBe("body");
  });
});

describe("assembleTopology — the wire-truth join (CH list-of-one, backend hub, real states)", () => {
  const sA = site("sa", "HQ");
  const sB = site("sb", "Branch");

  it("a site's gateways are the nodes with its site_id — as a LIST, never a scalar", () => {
    const nodes = [node({ id: "g1", site_id: "sa", is_site_hub: true }), node({ id: "g2", site_id: "sb" })];
    const cards = assembleTopology([sA, sB], {}, nodes);
    expect(Array.isArray(cards[0].gateways)).toBe(true);
    expect(cards[0].gateways.map((g) => g.id)).toEqual(["g1"]);
    // A future HA site with TWO gateways renders both (the shape does not foreclose it — CH).
    const ha = assembleTopology([sA], {}, [node({ id: "g1", site_id: "sa" }), node({ id: "g3", site_id: "sa" })]);
    expect(ha[0].gateways.map((g) => g.id)).toEqual(["g1", "g3"]);
  });

  it("hub is READ from node.is_site_hub (backend election), never recomputed", () => {
    const cards = assembleTopology([sA], {}, [node({ id: "g1", site_id: "sa", is_site_hub: true })]);
    expect(cards[0].gateways[0].isHub).toBe(true);
    // Absent/false is_site_hub → not a hub (no client-side election guessing).
    const nohub = assembleTopology([sA], {}, [node({ id: "g1", site_id: "sa" })]);
    expect(nohub[0].gateways[0].isHub).toBe(false);
  });

  it("health is the real badge; a healthy gateway shows null (no badge), a site_link_down shows danger", () => {
    const cards = assembleTopology([sA], {}, [
      node({ id: "g1", site_id: "sa", policy_degraded: false }),
      node({ id: "g2", site_id: "sa", policy_degraded: true, policy_degraded_kind: "site_link_down" }),
    ]);
    expect(cards[0].gateways[0].health).toBeNull();
    expect(cards[0].gateways[1].health?.tone).toBe("danger");
  });

  it("WF-B: the subordinate site-link note rides the join, INDEPENDENT of the headline (the walk's state)", () => {
    // A healthy-headline gateway that still carries a demoted-dead-peer note — both truths distinct.
    const cards = assembleTopology([sA], {}, [
      node({ id: "g1", site_id: "sa", policy_degraded: false, site_link_note_peer: "aws-gw-1", site_link_note_demoted: true }),
      node({ id: "g2", site_id: "sa", policy_degraded: false }),
    ]);
    expect(cards[0].gateways[0].health).toBeNull(); // headline healthy
    expect(cards[0].gateways[0].siteLinkNote).toEqual({ peer: "aws-gw-1", demoted: true }); // + subordinate line
    expect(cards[0].gateways[1].siteLinkNote).toBeNull(); // no note when the field is absent
  });

  it("subnets render their REAL status (pending is never shown as approved)", () => {
    const cards = assembleTopology([sA], { sa: [subnet("s1", "sa", "10.1.0.0/24", "approved"), subnet("s2", "sa", "10.2.0.0/24", "pending")] }, []);
    expect(cards[0].subnets).toEqual([
      { id: "s1", cidr: "10.1.0.0/24", status: "approved" },
      { id: "s2", cidr: "10.2.0.0/24", status: "pending" },
    ]);
  });

  it("max_policy_version absence → null (below-ceiling; the CW signal, consumed in Slice 3)", () => {
    const cards = assembleTopology([sA], {}, [
      node({ id: "g1", site_id: "sa" }), // no max reported
      node({ id: "g2", site_id: "sa", max_policy_version: 4 }),
    ]);
    expect(cards[0].gateways[0].maxPolicyVersion).toBeNull();
    expect(cards[0].gateways[1].maxPolicyVersion).toBe(4);
  });

  it("a site with no bound gateway renders an empty gateway list (stated, not hidden)", () => {
    const cards = assembleTopology([sA], {}, []);
    expect(cards[0].gateways).toEqual([]);
  });
});

// ── Slice 3: mutation-surface decisions ──────────────────────────────────────────────

describe("crossesMultiSiteThreshold — the CW confirm's action-ordering gate", () => {
  it("a FIRST site's approval never crosses (no other site has approved subnets yet)", () => {
    expect(crossesMultiSiteThreshold("sa", {})).toBe(false); // no approved anywhere
    expect(crossesMultiSiteThreshold("sa", { sa: 0 })).toBe(false);
  });
  it("first-approval-on-the-SECOND-site crosses (1 other site approved → becomes 2)", () => {
    expect(crossesMultiSiteThreshold("sb", { sa: 2, sb: 0 })).toBe(true);
  });
  it("adding a 2nd subnet to an already-approved site does NOT cross (site count unchanged)", () => {
    expect(crossesMultiSiteThreshold("sa", { sa: 1, sb: 3 })).toBe(false);
  });
  it("a 3rd site's approval when already multi-site does NOT newly cross (v5 already active)", () => {
    expect(crossesMultiSiteThreshold("sc", { sa: 1, sb: 1, sc: 0 })).toBe(false);
  });
});

describe("subCeilingGateways — names the gateways below the server ceiling (absence = below)", () => {
  const gws = [
    { id: "g1", name: "hub", maxPolicyVersion: 5 },
    { id: "g2", name: "old", maxPolicyVersion: 4 },
    { id: "g3", name: "never-reported", maxPolicyVersion: null },
  ];
  it("all-fleet-at-ceiling → EMPTY (clean confirm, no gateway list)", () => {
    expect(subCeilingGateways([{ id: "g1", name: "a", maxPolicyVersion: 5 }], 5)).toEqual([]);
  });
  it("mixed → names the sub-ceiling gateways; a never-reported agent counts as below", () => {
    expect(subCeilingGateways(gws, 5)).toEqual([
      { id: "g2", name: "old" },
      { id: "g3", name: "never-reported" },
    ]);
  });
});

describe("nameMatchesExactly — the delete-site ceremony (exact match, button dead until then)", () => {
  it("exact match only", () => {
    expect(nameMatchesExactly("HQ", "HQ")).toBe(true);
    expect(nameMatchesExactly("hq", "HQ")).toBe(false);
    expect(nameMatchesExactly("HQ ", "HQ")).toBe(false); // trailing space is not a match
    expect(nameMatchesExactly("", "HQ")).toBe(false);
  });
});

describe("disjointRefusal — VERBATIM per overlap class, null otherwise (no JS re-check)", () => {
  const refusal = (cls: string) => ({ error: { code: "subnet_not_disjoint", message: `this subnet overlaps the ${cls} range 10.0.0.0/24; approval refused` } });
  // One case per overlap class → a future class addition can't render blank.
  it("site-class refusal renders the API message verbatim", () => {
    expect(disjointRefusal(refusal("site"))).toMatch(/overlaps the site range/);
  });
  it("pool-class refusal renders verbatim", () => {
    expect(disjointRefusal(refusal("pool"))).toMatch(/overlaps the pool range/);
  });
  it("reserved-class refusal renders verbatim", () => {
    expect(disjointRefusal(refusal("reserved"))).toMatch(/overlaps the reserved range/);
  });
  it("a non-disjointness error returns null (caller shows its generic message)", () => {
    expect(disjointRefusal({ error: { code: "something_else", message: "x" } })).toBeNull();
    expect(disjointRefusal(undefined)).toBeNull();
  });
});

import { gatewayLiveness, GATEWAY_OFFLINE_MS } from "../src/lib/sitesview";

describe("gatewayLiveness — S8.4 rider (VERIFY-0: a stopped gateway must NOT read healthy on the site card)", () => {
  const now = 1_000_000_000_000;
  it("a freshly-reporting gateway is NOT offline", () => {
    const r = gatewayLiveness(new Date(now - 10_000).toISOString(), now);
    expect(r.offline).toBe(false);
    expect(r.lastSeen).toMatch(/ago|now|just/i);
  });
  it("a gateway stale past the threshold is OFFLINE (the dead-gateway-renders-healthy hole closed)", () => {
    const r = gatewayLiveness(new Date(now - GATEWAY_OFFLINE_MS - 60_000).toISOString(), now);
    expect(r.offline).toBe(true);
  });
  it("a never-connected gateway is offline, stated honestly", () => {
    const r = gatewayLiveness(null, now);
    expect(r.offline).toBe(true);
    expect(r.lastSeen).toBe("never connected");
  });
});

import { forwardsInSubnet } from "../src/lib/sitesview";

describe("forwardsInSubnet — S8.4 F4 (name the DNS forwards a subnet removal will also sweep; advisory only)", () => {
  const fwds = [
    { domain: "corp.local", resolver_ip: "10.20.0.53" },
    { domain: "branch.local", resolver_ip: "10.30.0.53" },
    { domain: "edge.local", resolver_ip: "10.20.0.99" },
  ];
  it("names only the forwards whose resolver is inside the CIDR", () => {
    expect(forwardsInSubnet(fwds, "10.20.0.0/24").sort()).toEqual(["corp.local", "edge.local"]);
  });
  it("excludes forwards resolved elsewhere", () => {
    expect(forwardsInSubnet(fwds, "10.30.0.0/24")).toEqual(["branch.local"]);
  });
  it("a /32 matches only its exact host", () => {
    expect(forwardsInSubnet(fwds, "10.20.0.53/32")).toEqual(["corp.local"]);
  });
  it("a malformed resolver or cidr is excluded (server stays the truth)", () => {
    expect(forwardsInSubnet([{ domain: "x", resolver_ip: "not-an-ip" }], "10.20.0.0/24")).toEqual([]);
    expect(forwardsInSubnet(fwds, "garbage")).toEqual([]);
  });
});

describe("gatewayOnline (WF-1 positive health)", () => {
  const linkDown: HealthBadge = { label: "site link down", tone: "danger" };
  it("active + fresh + no health badge → ONLINE (the positive signal)", () => {
    expect(gatewayOnline("active", false, null)).toBe(true);
  });
  it("offline (stale clock) → NOT online (the fresh side of the SAME clock)", () => {
    expect(gatewayOnline("active", true, null)).toBe(false);
  });
  it("a degraded-health badge (site_link_down) → NOT online (existing kinds win)", () => {
    expect(gatewayOnline("active", false, linkDown)).toBe(false);
  });
  it("revoked → NOT online regardless of freshness", () => {
    expect(gatewayOnline("revoked", false, null)).toBe(false);
  });
});
