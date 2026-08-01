import { describe, expect, it } from "vitest";
import { badgeText, gatewayBadgeText, FAILED, LOADING, ok, INITIAL_NAV_COUNTS, NAV_COUNT_REFRESH_MS } from "../src/lib/navcounts";

// S14.4 — THE STRICTEST DATA SURFACE IN THE PRODUCT.
//
//   A WRONG COUNT ON A PAGE THE USER *OPENED* IS A BUG THEY MIGHT NOTICE.
//   A WRONG COUNT IN *PERMANENT CHROME* IS FURNITURE.
//
// The user did not navigate to the nav, did not ask it a question, and has no moment of expectation against
// which to check its answer. That is why absent-never-zero is stricter here than anywhere else.

describe("badgeText — absent until loaded, absent on failure, never 0", () => {
  it("loading renders NOTHING", () => expect(badgeText(LOADING)).toBeNull());
  it("failed renders NOTHING", () => expect(badgeText(FAILED)).toBeNull());
  it("a known zero DOES render — a true zero is honest", () => {
    // The distinction the whole type exists for: "there are none" is a fact worth showing. "We could not find
    // out" is not. They are the same character on screen and different claims about the world.
    expect(badgeText(ok(0))).toBe("0");
  });
  it("a known number renders", () => expect(badgeText(ok(7))).toBe("7"));
});

describe("gatewayBadgeText — a PARTIAL answer is worse than none", () => {
  it("both known renders n/m", () => expect(gatewayBadgeText(ok(3), ok(7))).toBe("3/7"));
  it("online unknown renders NOTHING — not ?/7", () => expect(gatewayBadgeText(FAILED, ok(7))).toBeNull());
  it("total unknown renders NOTHING — not 3/?", () => expect(gatewayBadgeText(ok(3), FAILED)).toBeNull());
  it("either still loading renders NOTHING", () => expect(gatewayBadgeText(LOADING, ok(7))).toBeNull());
  // `3/?` asserts one half as fact while implying the other is momentarily missing — and the reader has no
  // way to know which half is real, which is strictly worse than an absent badge.
});

describe("the initial state cannot leak a zero", () => {
  it("every count starts UNKNOWN, not 0", () => {
    for (const [k, v] of Object.entries(INITIAL_NAV_COUNTS)) {
      expect(v.state, k).toBe("loading");
      expect(badgeText(v), k).toBeNull();
    }
  });
});

describe("the refresh cadence is slow on purpose", () => {
  it("is a minute, not a few seconds", () => {
    // A static count goes stale the moment anything changes and becomes a remembered number wearing a live
    // badge. A fast poll is four requests on a timer for a number the user glances at. Route-change covers
    // the case that actually matters.
    expect(NAV_COUNT_REFRESH_MS).toBeGreaterThanOrEqual(30_000);
  });
});
