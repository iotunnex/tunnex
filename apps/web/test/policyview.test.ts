import { describe, it, expect } from "vitest";
import {
  modeEnableConfirm,
  policyGate,
  ruleRow,
  swapRule,
  roleFromMembers,
  sectionRender,
  staleNoticeText,
  pruneStaleRuleIds,
  accessView,
  grantExpiry,
  extendErrorCopy,
  attributionLabel,
  activeMembers,
  rulesSummary,
  ruleBody,
  type LoadState,
} from "../src/lib/policyview";
import { loadOne, type Loaded } from "../src/lib/api";
import type { PolicyRule, UserGroup, Resource, Member } from "../src/lib/api";

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
    const row = ruleRow(g2g, groups, resources, [], LOADED);
    expect(row.src.label).toBe("Engineering");
    expect(row.dst.label).toBe("Databases");
    expect(row.broken).toBe(false);

    const g2r: PolicyRule = { id: "2", src_group_id: "g-eng", dst_kind: "resource", dst_resource_id: "r-net" } as PolicyRule;
    expect(ruleRow(g2r, groups, resources, [], LOADED).dst.label).toBe("10.0.5.0/24");
  });

  it("referent ABSENT from a LOADED set → 'deleted' (not omitted, broken=true)", () => {
    const rule: PolicyRule = { id: "3", src_group_id: "g-gone", dst_kind: "group", dst_group_id: "g-db" } as PolicyRule;
    const row = ruleRow(rule, groups, resources, [], LOADED);
    expect(row.src.state).toBe("deleted");
    expect(row.src.label).toMatch(/deleted group/i);
    expect(row.broken).toBe(true);
    expect(row.id).toBe("3"); // still present — never hidden
  });

  it("set FAILED TO LOAD → 'unresolved — refresh', NOT 'deleted' (no false alarm)", () => {
    const rule: PolicyRule = { id: "4", src_group_id: "g-eng", dst_kind: "group", dst_group_id: "g-db" } as PolicyRule;
    const row = ruleRow(rule, [], resources, [], { groupsLoaded: false, resourcesLoaded: true });
    expect(row.src.state).toBe("unresolved");
    expect(row.src.label).toMatch(/unresolved group.*refresh/i);
    expect(row.src.label).not.toMatch(/deleted/i); // must NOT lie about why
  });

  it("resource set failed to load → dst unresolved, not deleted", () => {
    const rule: PolicyRule = { id: "5", src_group_id: "g-eng", dst_kind: "resource", dst_resource_id: "r-net" } as PolicyRule;
    const row = ruleRow(rule, groups, [], [], { groupsLoaded: true, resourcesLoaded: false });
    expect(row.dst.state).toBe("unresolved");
    expect(row.dst.label).toMatch(/refresh/i);
  });
});

describe("loadOne — the class armed-guard: a failure NEVER reads as absence", () => {
  it("non-2xx (error present, data undefined) → NOT ok (never a reassuring empty)", async () => {
    const r = await loadOne(async () => ({ data: undefined, error: { error: { message: "boom" } } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toBe("boom");
  });
  it("data undefined with no error → NOT ok", async () => {
    const r = await loadOne(async () => ({ data: undefined }));
    expect(r.ok).toBe(false);
  });
  it("network REJECT (openapi-fetch throws) → NOT ok, legible message", async () => {
    const r = await loadOne(async () => {
      throw new Error("ECONNREFUSED");
    });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/reach the API/i);
  });
  it("data present → ok with the data", async () => {
    const r = await loadOne(async () => ({ data: [1, 2, 3] }));
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.data).toEqual([1, 2, 3]);
  });
});

describe("S8.3 rulesSummary — states enumerated, derived from Loaded<T> (failed never reads as 0-rules)", () => {
  const ok = <T,>(data: T): Loaded<T> => ({ ok: true, data });
  const fail: Loaded<never> = { ok: false, error: "boom" };

  it("either input still loading → loading (no premature posture claim)", () => {
    expect(rulesSummary({ modeResult: null, rulesResult: ok(0) }).state).toBe("loading");
    expect(rulesSummary({ modeResult: ok("enforcing"), rulesResult: null }).state).toBe("loading");
  });
  it("a FAILED rules load → 'failed', NEVER the 0-rules message (the reassuring-empty class on the loud line)", () => {
    const s = rulesSummary({ modeResult: ok("enforcing"), rulesResult: fail });
    expect(s.state).toBe("failed");
    expect(s.loud).toBe(false);
    expect(s.text).not.toMatch(/0 rules/);
  });
  it("a failed MODE load → failed (can't claim off or enforcing)", () => {
    expect(rulesSummary({ modeResult: fail, rulesResult: ok(3) }).state).toBe("failed");
  });
  it("off → open-mesh copy, not loud", () => {
    const s = rulesSummary({ modeResult: ok("off"), rulesResult: ok(0) });
    expect(s.state).toBe("off");
    expect(s.text).toMatch(/open mesh/i);
    expect(s.loud).toBe(false);
  });
  it("enforcing + 0 rules → LOUD 'all traffic denied' (the legibility-law lockout state)", () => {
    const s = rulesSummary({ modeResult: ok("enforcing"), rulesResult: ok(0) });
    expect(s.state).toBe("enforcing_empty");
    expect(s.loud).toBe(true);
    expect(s.text).toMatch(/denied/i);
  });
  it("enforcing + N rules → 'N rules — default-deny active', not loud; singular at 1", () => {
    expect(rulesSummary({ modeResult: ok("enforcing"), rulesResult: ok(3) }).text).toMatch(/3 rules/);
    expect(rulesSummary({ modeResult: ok("enforcing"), rulesResult: ok(1) }).text).toMatch(/1 rule\b/);
    expect(rulesSummary({ modeResult: ok("enforcing"), rulesResult: ok(3) }).loud).toBe(false);
  });
});

describe("S8.2c D5 ruleBody — the Access builder now creates SITE-subject rules (via the API, not a DB insert)", () => {
  const base = { src: "g1", srcUser: "u1", srcSite: "s1", dstGroup: "g2", dstResource: "r1", dstSite: "s2", expiresAt: "", editing: false };
  it("site → site sets ONLY the site ids (the demo's DB-insert path, now first-class in the UI)", () => {
    const b = ruleBody({ ...base, srcKind: "site", dstKind: "site" });
    expect(b).toMatchObject({ src_kind: "site", src_site_id: "s1", dst_kind: "site", dst_site_id: "s2" });
    expect("src_group_id" in b).toBe(false);
    expect("dst_resource_id" in b).toBe(false);
  });
  it("group → site (a device group reaching a site LAN)", () => {
    expect(ruleBody({ ...base, srcKind: "group", dstKind: "site" })).toMatchObject({ src_kind: "group", src_group_id: "g1", dst_kind: "site", dst_site_id: "s2" });
  });
  it("existing kinds unchanged (group→group, user→resource)", () => {
    expect(ruleBody({ ...base, srcKind: "group", dstKind: "group" })).toMatchObject({ src_kind: "group", dst_kind: "group", dst_group_id: "g2" });
    expect(ruleBody({ ...base, srcKind: "user", dstKind: "resource" })).toMatchObject({ src_kind: "user", src_user_id: "u1", dst_kind: "resource", dst_resource_id: "r1" });
  });
  it("expiry is create-only", () => {
    expect("expires_at" in ruleBody({ ...base, srcKind: "site", dstKind: "site", expiresAt: "2030-01-01T00:00", editing: false })).toBe(true);
    expect("expires_at" in ruleBody({ ...base, srcKind: "site", dstKind: "site", expiresAt: "2030-01-01T00:00", editing: true })).toBe(false);
  });
});

describe("[291] sectionRender — legibility signals COMPOSE, never compete", () => {
  it("loadError + notice both set → retry shows AND notice STILL shows (content hidden)", () => {
    const v = sectionRender("couldn't load", "old rule still active — retry removal");
    expect(v.showRetry).toBe(true);
    expect(v.showNotice).toBe(true); // the partial-swap warning is NOT masked by the load failure
    expect(v.showContent).toBe(false); // only CONTENT is replaced by retry
  });
  it("no error, notice set → content + notice", () => {
    expect(sectionRender(null, "note")).toEqual({ showRetry: false, showContent: true, showNotice: true });
  });
  it("no error, no notice → content only", () => {
    expect(sectionRender(null, null)).toEqual({ showRetry: false, showContent: true, showNotice: false });
  });
});

describe("notices reduction — derived from staleRuleIds (single source of truth)", () => {
  const R = (id: string) => ({ id } as PolicyRule);

  it("staleNoticeText: none → null; one → the partial message; many → a count line", () => {
    expect(staleNoticeText([])).toBeNull();
    expect(staleNoticeText(["abcdef12"])).toMatch(/could not be removed.*still active/i);
    expect(staleNoticeText(["a", "b"])).toMatch(/^2 rules could not be removed/i);
  });

  it("[371] a clean create never drops the warning — the set is only pruned by pruneStaleRuleIds", () => {
    // onDone(clean) adds nothing; the derived notice still reflects the live set.
    const set = ["X"];
    expect(staleNoticeText(set)).not.toBeNull(); // still shown after any unrelated success
  });

  it("[A] pruneStaleRuleIds NEVER clears on a FAILED load (loadOk=false) — persists", () => {
    expect(pruneStaleRuleIds(["X"], false, [])).toEqual(["X"]);
    expect(pruneStaleRuleIds(["X"], false, [R("Y")])).toEqual(["X"]);
  });

  it("[A] on a SUCCESSFUL load, an absent stale id CLEARS; a still-present one persists", () => {
    expect(pruneStaleRuleIds(["X"], true, [R("Y")])).toEqual([]); // X gone → cleared
    expect(pruneStaleRuleIds(["X"], true, [R("X"), R("Y")])).toEqual(["X"]); // unrelated Y present, X kept
  });

  it("[B] sequential partials — per-id prune, the first stale id is NOT orphaned", () => {
    // Two partials tracked; a successful load where only Z resolved keeps X.
    expect(pruneStaleRuleIds(["X", "Z"], true, [R("X")])).toEqual(["X"]);
  });
});

describe("[75]+[101] accessView — upsell needs only edition; role in-flight is not the gate", () => {
  const base = { fatal: false, loadError: false, editionReady: true, isEnterprise: true, roleError: false, roleResolved: true, canView: true };
  it("[75] non-enterprise + members-fail → upsell, NOT role_retry", () => {
    expect(accessView({ ...base, isEnterprise: false, roleError: true, roleResolved: false })).toBe("upsell");
  });
  it("[101] enterprise + role in-flight → role_loading, NOT member_gate", () => {
    expect(accessView({ ...base, roleResolved: false, canView: false })).toBe("role_loading");
  });
  it("enterprise + roleError → role_retry", () => {
    expect(accessView({ ...base, roleError: true, roleResolved: false })).toBe("role_retry");
  });
  it("enterprise admin resolved → admin_body; member resolved → member_gate", () => {
    expect(accessView({ ...base, canView: true })).toBe("admin_body");
    expect(accessView({ ...base, canView: false })).toBe("member_gate");
  });
  it("meta/org not ready → loading; fatal → fatal; loadError → load_retry", () => {
    expect(accessView({ ...base, editionReady: false })).toBe("loading");
    expect(accessView({ ...base, fatal: true })).toBe("fatal");
    expect(accessView({ ...base, loadError: true })).toBe("load_retry");
  });
});

describe("[0] roleFromMembers — a FAILED members load is NOT 'member' (no false lockout)", () => {
  const me = "u-me";
  it("failed load → failed:true, NO role (caller shows retry, not the manage-gate)", () => {
    const res = roleFromMembers({ ok: false, error: "boom" } as Loaded<Member[]>, me);
    expect(res.failed).toBe(true);
    expect(res.role).toBeUndefined();
    // Critical: policyGate must NOT be fed this as role=undefined-treated-as-member.
  });
  it("ok load, actor is admin → role admin, not failed", () => {
    const members = [{ user_id: me, role: "admin" } as Member];
    const res = roleFromMembers({ ok: true, data: members }, me);
    expect(res).toEqual({ role: "admin", failed: false });
  });
  it("ok load, actor absent from roster → role undefined but NOT failed", () => {
    const res = roleFromMembers({ ok: true, data: [] as Member[] }, me);
    expect(res.failed).toBe(false);
    expect(res.role).toBeUndefined();
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

const M = (id: string, name: string, status: "active" | "deactivated" = "active") =>
  ({ user_id: id, name, email: `${name}@x`, status } as Member);

describe("S7.5.4 ruleRow user subject", () => {
  const rule = { id: "r1", src_kind: "user", src_user_id: "u1", dst_kind: "resource", dst_resource_id: "res1" } as PolicyRule;
  const resources = [R("res1", "db")];
  it("resolves a per-user subject to the member name", () => {
    const row = ruleRow(rule, [], resources, [M("u1", "alice")], { groupsLoaded: true, resourcesLoaded: true, membersLoaded: true });
    expect(row.src.label).toBe("alice");
    expect(row.src.state).toBe("ok");
  });
  it("a removed user (not in a loaded roster) shows distinctly, never mislabeled", () => {
    const row = ruleRow(rule, [], resources, [], { groupsLoaded: true, resourcesLoaded: true, membersLoaded: true });
    expect(row.src.label).toMatch(/removed user/);
    expect(row.src.state).toBe("deleted");
    expect(row.broken).toBe(true);
  });
  it("a FAILED roster load reads unresolved (refresh), not removed", () => {
    const row = ruleRow(rule, [], resources, [], { groupsLoaded: true, resourcesLoaded: true, membersLoaded: false });
    expect(row.src.state).toBe("unresolved");
  });
});

describe("S7.5.4 grantExpiry (linger model)", () => {
  const now = 1_000_000_000_000;
  it("no expiry = permanent, not extendable", () => {
    expect(grantExpiry({ expires_at: null }, now)).toEqual({ state: "permanent", label: "permanent", extendable: false });
  });
  it("future expiry = active, extendable", () => {
    const g = grantExpiry({ expires_at: new Date(now + 3 * 3600_000).toISOString() }, now);
    expect(g.state).toBe("active");
    expect(g.label).toMatch(/expires in 3h/);
    expect(g.extendable).toBe(true);
  });
  it("past expiry = expired-but-EXTENDABLE (linger: shown + the extend 409s legibly)", () => {
    const g = grantExpiry({ expires_at: new Date(now - 2 * 3600_000).toISOString() }, now);
    expect(g.state).toBe("expired");
    expect(g.label).toMatch(/expired 2h ago/);
    expect(g.extendable).toBe(true); // the server refuses with 409 grant_lapsed, surfaced legibly
  });
});

describe("S7.5.4 extendErrorCopy", () => {
  it("maps typed 409 codes to legible copy, never a raw error", () => {
    expect(extendErrorCopy("grant_lapsed")).toMatch(/already expired/);
    expect(extendErrorCopy("not_temporary")).toMatch(/permanent grant/);
    expect(extendErrorCopy(undefined)).toMatch(/Could not extend/);
  });
});

describe("S7.5.4 attributionLabel (rider 1 — absence is visible)", () => {
  it("device present + user unresolved shows 'device X · user unknown', never blank", () => {
    expect(attributionLabel({ deviceId: "dev-abc12345", userId: null })).toBe("device dev-abc1… · user unknown");
    expect(attributionLabel({ deviceId: "d", userId: null, deviceName: "alice-laptop" })).toBe("alice-laptop · user unknown");
  });
  it("both resolved shows device · user", () => {
    expect(attributionLabel({ deviceId: "d", userId: "u", deviceName: "laptop", userName: "alice" })).toBe("laptop · alice");
  });
  it("no device stamped reads 'unattributed', not a blank/dash", () => {
    expect(attributionLabel({ deviceId: null, userId: null })).toBe("unattributed");
  });
});

describe("S7.5.4 activeMembers (D1 picker constraint)", () => {
  it("offers only current active members", () => {
    const out = activeMembers([M("u1", "alice"), M("u2", "bob", "deactivated")]);
    expect(out.map((m) => m.user_id)).toEqual(["u1"]);
  });
});

import { canEditRuleInModal } from "../src/lib/policyview";

describe("canEditRuleInModal — site rules are NOT editable in the group/resource modal (S8.1 dst, S8.2 src)", () => {
  it("group and resource rules (with a group/user source) are editable", () => {
    expect(canEditRuleInModal({ src_kind: "group", dst_kind: "group" })).toBe(true);
    expect(canEditRuleInModal({ src_kind: "user", dst_kind: "resource" })).toBe(true);
  });
  it("a site-DST rule is NOT editable here (would silently rewrite it — write-guard, not display)", () => {
    expect(canEditRuleInModal({ src_kind: "group", dst_kind: "site" })).toBe(false);
  });
  it("a site-SRC rule (S8.2) is NOT editable here either (same write-guard)", () => {
    expect(canEditRuleInModal({ src_kind: "site", dst_kind: "group" })).toBe(false);
  });
});

import { ruleRow } from "../src/lib/policyview";

describe("ruleRow — a site-dst rule renders as a site, NEVER a broken 'deleted resource' (S8.1 #2)", () => {
  it("dst_kind='site' → site label, state ok, not broken", () => {
    const rule = {
      id: "r1", org_id: "o", src_kind: "group", src_group_id: "g1",
      dst_kind: "site", dst_site_id: "00000000-0000-0000-0000-0000000051e1", created_at: "x",
    } as any;
    const row = ruleRow(rule, [{ id: "g1", name: "Admins" } as any], [], [], { groupsLoaded: true, resourcesLoaded: true } as any);
    expect(row.dst.state).toBe("ok");
    expect(row.dst.label).toMatch(/^site /);
    expect(row.broken).toBe(false);
  });
});
