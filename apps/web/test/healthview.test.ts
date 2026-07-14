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
});
