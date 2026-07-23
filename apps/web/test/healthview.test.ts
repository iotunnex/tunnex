import { describe, it, expect } from "vitest";
import { policyHealthBadge, siteLinkNote } from "../src/lib/healthview";
import type { Node } from "../src/lib/api";

const node = (degraded: boolean, kind?: Node["policy_degraded_kind"]) =>
  ({ policy_degraded: degraded, policy_degraded_kind: kind }) as Pick<Node, "policy_degraded" | "policy_degraded_kind">;

describe("policyHealthBadge — bool primary, kind refines, never less alarmed", () => {
  it("not degraded → NO badge (healthy)", () => {
    expect(policyHealthBadge(node(false, "healthy"))).toBeNull();
    expect(policyHealthBadge(node(false))).toBeNull();
  });

  it("converging → subtle 'syncing…' (warn, not danger — a normal push must not alarm loudly)", () => {
    const b = policyHealthBadge(node(true, "converging"));
    expect(b).toEqual({ label: "syncing…", tone: "warn" });
  });

  it("silent_desync → danger (the stuck, actionable case)", () => {
    expect(policyHealthBadge(node(true, "silent_desync"))?.tone).toBe("danger");
  });

  it("desync_unknown → 'health unknown' (honest can't-determine, never rendered healthy)", () => {
    const b = policyHealthBadge(node(true, "desync_unknown"));
    expect(b?.label).toMatch(/unknown/i);
    expect(b).not.toBeNull();
  });

  it("apply_failing / stuck_enforcing render distinct labels", () => {
    expect(policyHealthBadge(node(true, "apply_failing"))?.label).toMatch(/apply/i);
    expect(policyHealthBadge(node(true, "stuck_enforcing"))?.tone).toBe("danger");
  });

  it("degraded bool but kind absent/healthy → STILL a badge (never less alarmed than the bool)", () => {
    expect(policyHealthBadge(node(true, "healthy"))).not.toBeNull();
    expect(policyHealthBadge(node(true))).not.toBeNull();
  });

  it("S8.7 conntrack_flush_unavailable → a distinct warn badge (expiry-flush degraded)", () => {
    const b = policyHealthBadge(node(true, "conntrack_flush_unavailable"));
    expect(b?.tone).toBe("warn");
    expect(b?.label).toMatch(/flush/i);
  });

  it("S8.2 kinds render distinct danger badges (site hub/link down, agent too old)", () => {
    expect(policyHealthBadge(node(true, "unsupported_policy_version"))?.tone).toBe("danger");
    const hub = policyHealthBadge(node(true, "site_hub_down"));
    expect(hub?.tone).toBe("danger");
    expect(hub?.label).toMatch(/hub/i);
    expect(policyHealthBadge(node(true, "site_link_down"))?.label).toMatch(/link/i);
    expect(policyHealthBadge(node(true, "site_subnet_unreachable"))?.tone).toBe("danger"); // S8.2c D3
  });

  it("WF-C L2 hub_forwarding_not_reconciling → danger, names BOTH halves (forwarding + agent down) + the remedy", () => {
    const b = policyHealthBadge(node(true, "hub_forwarding_not_reconciling"));
    expect(b?.tone).toBe("danger"); // stale enforcement is serious, never a soft warn
    expect(b?.label).toMatch(/forward/i); // lies in neither direction: it IS forwarding ...
    expect(b?.label).toMatch(/agent|restart/i); // ... but the agent is down — restart it
  });

  it("forward-compat: an unknown future kind falls through to the 'degraded' default (never null while degraded)", () => {
    // A kind the switch doesn't enumerate must still badge (the default guards the next kind we add).
    const b = policyHealthBadge(node(true, "some_future_kind" as Node["policy_degraded_kind"]));
    expect(b).toEqual({ label: "degraded", tone: "warn" });
  });
});
describe("siteLinkNote — WF-B subordinate line, independent of the headline badge", () => {
  const n = (peer?: string | null, demoted?: boolean) =>
    ({ site_link_note_peer: peer ?? null, site_link_note_demoted: demoted ?? null }) as Pick<
      Node,
      "site_link_note_peer" | "site_link_note_demoted"
    >;

  it("no peer → no note (null)", () => {
    expect(siteLinkNote(n(null))).toBeNull();
    expect(siteLinkNote(n(undefined))).toBeNull();
  });

  it("a demoted-dead peer → named note carrying the (demoted) qualifier", () => {
    expect(siteLinkNote(n("aws-gw-1", true))).toEqual({ peer: "aws-gw-1", demoted: true });
  });

  it("the note is INDEPENDENT of policy_degraded_kind — a healthy headline can still carry it (the walk's state)", () => {
    // The CP only sets the note when the headline is NOT site_link_down; the render shows both truths distinct.
    const healthyHeadline = { policy_degraded: false, policy_degraded_kind: "healthy" } as Pick<Node, "policy_degraded" | "policy_degraded_kind">;
    expect(policyHealthBadge(healthyHeadline)).toBeNull(); // headline healthy
    expect(siteLinkNote(n("aws-gw-1", true))).not.toBeNull(); // + a subordinate named line
  });
});
