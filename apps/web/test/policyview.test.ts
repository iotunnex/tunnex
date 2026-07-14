import { describe, it, expect } from "vitest";
import {
  modeEnableConfirm,
  policyGate,
  ruleRow,
  swapRule,
  type LoadState,
} from "../src/lib/policyview";
import type { PolicyRule, UserGroup, Resource } from "../src/lib/api";

const G = (id: string, name: string) => ({ id, name } as UserGroup);
const R = (id: string, name: string) => ({ id, name } as Resource);
const LOADED: LoadState = { groupsLoaded: true, resourcesLoaded: true };

describe("D-a4 mode-enable confirm = pure function of the rule COUNT", () => {
  it("N>0 → generic count copy, not danger", () => {
    const c = modeEnableConfirm(3);
    expect(c.danger).toBe(false);
    expect(c.body).toContain("3 allow rules");
    expect(c.body).not.toMatch(/no allow rules/i);
  });
  it("singular vs plural", () => {
    expect(modeEnableConfirm(1).body).toContain("1 allow rule");
    expect(modeEnableConfirm(1).body).not.toContain("1 allow rules");
  });
  it("ZERO rules → the STRONG danger gate naming self-lockout", () => {
    const c = modeEnableConfirm(0);
    expect(c.danger).toBe(true);
    expect(c.body).toMatch(/denies ALL traffic/i);
    expect(c.body).toMatch(/your own access/i);
  });
  it("never computes a blast radius (no device names / counts of affected devices)", () => {
    // Copy is a function of the RULE count only — it must not claim which devices are hit.
    expect(modeEnableConfirm(5).body).not.toMatch(/device/i);
  });
});

describe("policyGate — enterprise + RBAC + verified-email", () => {
  it("open edition → nothing, even for an owner", () => {
    const g = policyGate({ role: "owner", emailVerified: true, edition: "open" });
    expect(g.isEnterprise).toBe(false);
    expect(g.canView).toBe(false);
    expect(g.canManagePolicy).toBe(false);
    expect(g.canManageDevices).toBe(false);
  });
  it("enterprise member → no view (policy is admin/owner only)", () => {
    const g = policyGate({ role: "member", emailVerified: true, edition: "enterprise" });
    expect(g.canView).toBe(false);
    expect(g.canManagePolicy).toBe(false);
  });
  it("enterprise admin, verified → view + manage", () => {
    const g = policyGate({ role: "admin", emailVerified: true, edition: "enterprise" });
    expect(g.canView).toBe(true);
    expect(g.canManagePolicy).toBe(true);
    expect(g.canManageDevices).toBe(true);
  });
  it("enterprise admin, UNVERIFIED email → can view but NOT manage (mirrors server)", () => {
    const g = policyGate({ role: "admin", emailVerified: false, edition: "enterprise" });
    expect(g.canView).toBe(true);
    expect(g.canManagePolicy).toBe(false);
    expect(g.canManageDevices).toBe(false);
  });
});

describe("D-a6 rule label — NEVER omit; DELETED ≠ UNRESOLVED", () => {
  const groups = [G("g-eng", "Engineering"), G("g-db", "Databases")];
  const resources = [R("r-net", "10.0.5.0/24")];

  it("resolves group→group and group→resource to names", () => {
    const g2g: PolicyRule = { id: "1", src_group_id: "g-eng", dst_kind: "group", dst_group_id: "g-db" } as PolicyRule;
    const row = ruleRow(g2g, groups, resources, LOADED);
    expect(row.src.label).toBe("Engineering");
    expect(row.dst.label).toBe("Databases");
    expect(row.broken).toBe(false);

    const g2r: PolicyRule = { id: "2", src_group_id: "g-eng", dst_kind: "resource", dst_resource_id: "r-net" } as PolicyRule;
    expect(ruleRow(g2r, groups, resources, LOADED).dst.label).toBe("10.0.5.0/24");
  });

  it("referent ABSENT from a LOADED set → 'deleted' (not omitted, broken=true)", () => {
    const rule: PolicyRule = { id: "3", src_group_id: "g-gone", dst_kind: "group", dst_group_id: "g-db" } as PolicyRule;
    const row = ruleRow(rule, groups, resources, LOADED);
    expect(row.src.state).toBe("deleted");
    expect(row.src.label).toMatch(/deleted group/i);
    expect(row.broken).toBe(true);
    expect(row.id).toBe("3"); // still present — never hidden
  });

  it("set FAILED TO LOAD → 'unresolved — refresh', NOT 'deleted' (no false alarm)", () => {
    const rule: PolicyRule = { id: "4", src_group_id: "g-eng", dst_kind: "group", dst_group_id: "g-db" } as PolicyRule;
    const row = ruleRow(rule, [], resources, { groupsLoaded: false, resourcesLoaded: true });
    expect(row.src.state).toBe("unresolved");
    expect(row.src.label).toMatch(/unresolved group.*refresh/i);
    expect(row.src.label).not.toMatch(/deleted/i); // must NOT lie about why
  });

  it("resource set failed to load → dst unresolved, not deleted", () => {
    const rule: PolicyRule = { id: "5", src_group_id: "g-eng", dst_kind: "resource", dst_resource_id: "r-net" } as PolicyRule;
    const row = ruleRow(rule, groups, [], { groupsLoaded: true, resourcesLoaded: false });
    expect(row.dst.state).toBe("unresolved");
    expect(row.dst.label).toMatch(/refresh/i);
  });
});

describe("D-a5 swapRule — CREATE-THEN-DELETE, gap-free, LEGIBLE partial", () => {
  it("happy path: create then delete → replaced", async () => {
    const calls: string[] = [];
    const out = await swapRule(
      "old-1",
      async () => { calls.push("create"); return { id: "new-1" }; },
      async () => { calls.push("delete"); return; },
    );
    expect(out).toEqual({ outcome: "replaced", newId: "new-1" });
    expect(calls).toEqual(["create", "delete"]); // create STRICTLY before delete
  });

  it("create fails → old rule is NEVER deleted (no gap), edit aborts", async () => {
    let deleted = false;
    const out = await swapRule(
      "old-1",
      async () => ({ error: "boom" }),
      async () => { deleted = true; },
    );
    expect(out).toEqual({ outcome: "create_failed", error: "boom" });
    expect(deleted).toBe(false); // delete-old must NOT run when create failed
  });

  it("create ok + delete FAILS → 'partial': duplicate persists, LEGIBLE, both ids returned", async () => {
    const out = await swapRule(
      "old-1",
      async () => ({ id: "new-1" }),
      async () => ({ error: "delete failed" }),
    );
    expect(out).toEqual({ outcome: "partial", newId: "new-1", oldId: "old-1", error: "delete failed" });
    // Caller uses this to show BOTH rules + a retry — never a silent duplicate.
  });
});
