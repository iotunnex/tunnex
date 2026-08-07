import { describe, expect, it } from "vitest";
import { ceilingSentence } from "../src/components/CeilingUpgrade";

// ⛔ AT-CEILING AND OVER-CEILING ARE DIFFERENT SENTENCES, AND THE WRONG ONE CAUSES A DESTRUCTIVE MISTAKE.
describe("the standing ceiling notice", () => {
  it("at the ceiling, offers retiring as a real route AND names what it costs", () => {
    const s = ceilingSentence(1, 1, "community");
    expect(s).toContain("no room for another");
    expect(s).toContain("retire a gateway");
    // ⛔ THE CLAUSE THAT STOPS THIS READING AS HOUSEKEEPING. Revoking cascades to every device homed to
    // that gateway, so the remedy this notice recommends can disconnect fifty people. "Revoke a gateway
    // you no longer use" was TRUE and named none of that.
    expect(s).toContain("permanent and disconnects every device homed to it");
    // ⚠ AND IT SENDS THEM WHERE THE NUMBER IS, rather than promising a count it cannot compute: this
    // notice is deployment-scoped and does not know which gateway the operator will pick.
    expect(s).toContain("says how many");
    // ⭐ AND THE WAY OUT, or the notice reads as a dead end and the operator either freezes or believes
    // the disconnected people are unrecoverable. They are not — the device rows survive the cascade.
    expect(s).toContain("moved to another gateway");
    // The old phrasing must not survive anywhere in the string.
    expect(s).not.toContain("you no longer use");
  });

  // ⭐ THE ONE THAT MATTERS TODAY. At 6 against 1, revoking a gateway frees NOTHING — five would still be
  // over. Telling that operator "no room left" invites them to revoke one and retry, which fails, and now
  // they have destroyed a working gateway for nothing.
  it("past the ceiling, says how far over and does NOT offer revoking", () => {
    const s = ceilingSentence(6, 1, "community");
    expect(s).toContain("6 are enrolled");
    expect(s).toContain("5 past the limit");
    expect(s).toContain("Revoking one will not free a slot");
    expect(s).not.toContain("retire a gateway");
  });

  // ⚠ BOTH SENTENCES PROMISE THE SAME THING FIRST, because the operator's real question is "is my fleet
  // about to stop", and the answer is no.
  it("always says nothing running is affected", () => {
    expect(ceilingSentence(6, 1, "community")).toContain(
      "Nothing running is affected",
    );
  });

  it("pluralises the ceiling", () => {
    expect(ceilingSentence(2, 2, "trial")).toContain("allows 2 gateways");
    expect(ceilingSentence(1, 1, "community")).toContain("allows 1 gateway");
  });
});
