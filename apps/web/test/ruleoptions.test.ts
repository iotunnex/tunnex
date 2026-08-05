import { describe, expect, it } from "vitest";
import { sourceOptions, destinationOptions, SELF_SITE_REASON } from "../src/lib/policyview";

/**
 * ⛔ THE VALIDITY RULES, TESTED WITHOUT A DOM — which is why they are pure functions rather than logic inside
 * the picker. The matrix they implement is measured from the compiler (`docs/rule-validity-matrix.md`), never
 * from what the old form happened to offer.
 */
const G = [{ id: "g1", name: "Engineering" }];
const M = [{ user_id: "u1", email: "ana@x.com", name: "Ana" }, { user_id: "u2", email: "bo@x.com" }];
const S = [{ id: "s1", name: "eu-lan" }, { id: "s2", name: "ap-lan" }];
const A = [{ device_id: "d1", name: "mcp-agent", gateway_name: "gw-1" }];
const R = [{ id: "r1", name: "gitlab" }];
const K = [{ id: "k1", name: "payments" }];

describe("rule option lists — one picker per side", () => {
  it("every source kind appears in ONE list, each carrying its kind as text", () => {
    // ⛔ THE TAG IS THE ONLY THING distinguishing a site named eu-lan from a group named eu-lan, and the two
    // behave completely differently in the compiler. It must be text, never a colour alone.
    const o = sourceOptions({ groups: G, members: M, sites: S, agents: A, dstKind: "group", dstSite: "" });
    expect(o.map((x) => x.kind)).toEqual(["group", "user", "user", "site", "site", "agent"]);
    expect(o.every((x) => x.tag.length > 0)).toBe(true);
  });

  it("a person's email rides along even when a display name exists", () => {
    // It is what an operator searches by, and the only disambiguator between two people with one name.
    const o = sourceOptions({ groups: [], members: M, sites: [], agents: [], dstKind: "", dstSite: "" });
    expect(o[0].label).toBe("Ana");
    expect(o[0].detail).toBe("ana@x.com");
    // ⚠ And a member with no name falls back to the email rather than rendering blank.
    expect(o[1].label).toBe("bo@x.com");
  });

  it("an agent names the gateway it connects through", () => {
    const o = sourceOptions({ groups: [], members: [], sites: [], agents: A, dstKind: "", dstSite: "" });
    expect(o[0].detail).toBe("via gw-1");
  });

  it("⭐ A SITE CANNOT REACH ITSELF — and the option is SHOWN, DISABLED, WITH THE REASON", () => {
    // Hiding it would teach nothing: the operator changes the other side and an entry silently vanishes.
    // Saying why teaches the rule. This mirrors the server's invalid_rule_self_site — the API is the guard.
    const src = sourceOptions({ groups: G, members: [], sites: S, agents: [], dstKind: "site", dstSite: "s1" });
    const eu = src.find((o) => o.value === "s1")!;
    expect(eu.unavailable).toBe(SELF_SITE_REASON);
    // ⛔ THE OTHER SITE IS UNTOUCHED. Site-to-site transit is S8.2's whole subject and is proven on the
    // wire; a guard that disabled every site would delete a shipped feature while passing the line above.
    expect(src.find((o) => o.value === "s2")!.unavailable).toBeUndefined();
  });

  it("…and symmetrically on the destination side", () => {
    const dst = destinationOptions({
      groups: G, resources: R, sites: S, services: K, srcKind: "site", srcSite: "s2",
    });
    expect(dst.find((o) => o.value === "s2")!.unavailable).toBe(SELF_SITE_REASON);
    expect(dst.find((o) => o.value === "s1")!.unavailable).toBeUndefined();
  });

  it("⚠ NOTHING IS DISABLED WHEN THE OTHER SIDE IS NOT A SITE", () => {
    // Without this, "disable everything" would satisfy the assertions above and make the form unusable —
    // the guard-that-is-an-outage shape.
    const dst = destinationOptions({
      groups: G, resources: R, sites: S, services: K, srcKind: "group", srcSite: "",
    });
    expect(dst.every((o) => !o.unavailable)).toBe(true);
  });

  it("every destination kind is offered, including k8s services", () => {
    const dst = destinationOptions({
      groups: G, resources: R, sites: S, services: K, srcKind: "group", srcSite: "",
    });
    expect(new Set(dst.map((o) => o.kind))).toEqual(new Set(["group", "resource", "site", "k8s_service"]));
  });
});
