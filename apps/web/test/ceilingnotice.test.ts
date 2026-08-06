import { describe, expect, it } from "vitest";
import { ceilingSentence } from "../src/components/CeilingUpgrade";

// ⛔ AT-CEILING AND OVER-CEILING ARE DIFFERENT SENTENCES, AND THE WRONG ONE CAUSES A DESTRUCTIVE MISTAKE.
describe("the standing ceiling notice", () => {
  it("at the ceiling, offers revoking as a real route", () => {
    const s = ceilingSentence(1, 1, "community");
    expect(s).toContain("no room for another");
    expect(s).toContain("revoke a gateway you no longer use");
  });

  // ⭐ THE ONE THAT MATTERS TODAY. At 6 against 1, revoking a gateway frees NOTHING — five would still be
  // over. Telling that operator "no room left" invites them to revoke one and retry, which fails, and now
  // they have destroyed a working gateway for nothing.
  it("past the ceiling, says how far over and does NOT offer revoking", () => {
    const s = ceilingSentence(6, 1, "community");
    expect(s).toContain("6 are enrolled");
    expect(s).toContain("5 past the limit");
    expect(s).toContain("Revoking one will not free a slot");
    expect(s).not.toContain("revoke a gateway you no longer use");
  });

  // ⚠ BOTH SENTENCES PROMISE THE SAME THING FIRST, because the operator's real question is "is my fleet
  // about to stop", and the answer is no.
  it("always says nothing running is affected", () => {
    expect(ceilingSentence(6, 1, "community")).toContain("Nothing running is affected");
  });

  it("pluralises the ceiling", () => {
    expect(ceilingSentence(2, 2, "trial")).toContain("allows 2 gateways");
    expect(ceilingSentence(1, 1, "community")).toContain("allows 1 gateway");
  });
});
