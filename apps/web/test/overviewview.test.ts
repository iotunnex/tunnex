import { describe, expect, it } from "vitest";
import {
  isFreshOrg,
  sortGateways,
  statFrom,
  statText,
  type GatewayRow,
} from "../src/lib/overviewview";
import { FAILED, LOADING, ok } from "../src/lib/navcounts";

// S14.4 — the Overview's PURE decisions.
//
// ⚠ THIS FILE EXISTS BECAUSE TWO MUTATIONS PASSED. `sortGateways`'s ordering and `isFreshOrg`'s
// known-empty requirement were both asserted only INDIRECTLY, through a screen test that happened to hold
// while the decision was wrong. Indirect coverage is coverage of the path, not of the decision.

describe("sortGateways — UNHEALTHY FIRST", () => {
  const rows: GatewayRow[] = [
    { id: "1", name: "b-healthy", label: "healthy", tone: "ok" },
    { id: "2", name: "a-broken", label: "silent desync", tone: "danger" },
    { id: "3", name: "c-syncing", label: "syncing…", tone: "warn" },
    { id: "4", name: "d-unknown", label: "health unknown", tone: "neutral" },
  ];

  it("orders danger, warn, neutral, ok", () => {
    // The list shows ALL gateways, so ORDER is what makes it useful. A broken gateway sorted below three
    // healthy ones is a broken gateway below the fold — present, and not seen.
    expect(sortGateways(rows).map((r) => r.name)).toEqual([
      "a-broken",
      "c-syncing",
      "d-unknown",
      "b-healthy",
    ]);
  });

  it("breaks ties by name, so the order is stable across renders", () => {
    const tie: GatewayRow[] = [
      { id: "1", name: "zed", label: "healthy", tone: "ok" },
      { id: "2", name: "alpha", label: "healthy", tone: "ok" },
    ];
    expect(sortGateways(tie).map((r) => r.name)).toEqual(["alpha", "zed"]);
  });

  it("does not mutate its input", () => {
    const before = rows.map((r) => r.name);
    sortGateways(rows);
    expect(rows.map((r) => r.name)).toEqual(before);
  });
});

describe("isFreshOrg — onboarding only when the org is KNOWN to be empty", () => {
  // ⚠ AND THIS IS BELT-AND-BRACES, WHICH IS WORTH SAYING RATHER THAN OVERSTATING. On the screen today the
  // primary protection is that a failed `/overview` leaves `data` null, so the whole block is unrendered —
  // the screen test passes for THAT reason, not because of these checks. Gating the decision directly means
  // the guard survives a refactor that changes how the screen handles a null `data`.
  it("all zero and all KNOWN -> fresh", () => {
    expect(isFreshOrg(ok(0), ok(0), ok(0))).toBe(true);
  });

  it("a FAILED count is NOT fresh — a failure is not an empty org", () => {
    // Showing onboarding because a fetch failed would tell a founder with a working fleet that they have
    // nothing: the reassuring-empty defect wearing an onboarding hat.
    expect(isFreshOrg(FAILED, ok(0), ok(0))).toBe(false);
    expect(isFreshOrg(ok(0), FAILED, ok(0))).toBe(false);
    expect(isFreshOrg(ok(0), ok(0), FAILED)).toBe(false);
  });

  it("a LOADING count is NOT fresh — the answer has not arrived", () => {
    expect(isFreshOrg(LOADING, ok(0), ok(0))).toBe(false);
  });

  it("a populated org is not fresh", () => {
    expect(isFreshOrg(ok(2), ok(0), ok(1))).toBe(false);
    expect(isFreshOrg(ok(0), ok(5), ok(1))).toBe(false);
  });
});

describe("statFrom / statText", () => {
  it("null (not yet fetched) is LOADING and renders nothing", () => {
    expect(statText(statFrom(null, () => 1))).toBeNull();
  });
  it("a failed load renders nothing", () => {
    expect(statText(statFrom({ ok: false, error: "x" }, () => 1))).toBeNull();
  });
  it("a true zero renders '0'", () => {
    expect(
      statText(statFrom({ ok: true, data: [] }, (a: number[]) => a.length)),
    ).toBe("0");
  });
});
