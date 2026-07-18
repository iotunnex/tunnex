import { describe, it, expect } from "vitest";
import { policyHealthBadge } from "../src/lib/healthview";
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

  it("S8.2 kinds render distinct danger badges (site hub/link down, agent too old)", () => {
    expect(policyHealthBadge(node(true, "unsupported_policy_version"))?.tone).toBe("danger");
    const hub = policyHealthBadge(node(true, "site_hub_down"));
    expect(hub?.tone).toBe("danger");
    expect(hub?.label).toMatch(/hub/i);
    expect(policyHealthBadge(node(true, "site_link_down"))?.label).toMatch(/link/i);
    expect(policyHealthBadge(node(true, "site_subnet_unreachable"))?.tone).toBe("danger"); // S8.2c D3
  });

  it("forward-compat: an unknown future kind falls through to the 'degraded' default (never null while degraded)", () => {
    // A kind the switch doesn't enumerate must still badge (the default guards the next kind we add).
    const b = policyHealthBadge(node(true, "some_future_kind" as Node["policy_degraded_kind"]));
    expect(b).toEqual({ label: "degraded", tone: "warn" });
  });
});
