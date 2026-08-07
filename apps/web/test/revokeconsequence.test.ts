import { describe, expect, it } from "vitest";
import { revokeConsequence } from "../src/lib/gatewaysview";

// ⛔ THE CONFIRM CARRIES THE WHOLE TRUTH: the devices stop connecting immediately, the gateway cannot be
// un-revoked, and the devices can be moved to another gateway. All three, or the operator acts on a
// partial picture at the one moment the act is irreversible.
describe("revokeConsequence", () => {
  it("names the count, the immediacy, the permanence AND the way out", () => {
    const s = revokeConsequence({ n1: 50 }, "n1");
    expect(s).toContain("50 devices");
    expect(s).toContain("stop connecting immediately");
    expect(s).toContain("never active again");
    // ⭐ THE CLAUSE THAT STOPS IT READING AS A DEAD END. The device rows survive the cascade
    // (`revoked_cause='cascade'`), so re-homing is real — and an operator who does not know that believes
    // the people are simply lost.
    expect(s).toContain("moved to another gateway");
  });

  it("still states the permanence when NOTHING is homed there", () => {
    // ⚠ The count falls silent at zero; the irreversibility does not. It is a fact about the GATEWAY and is
    // true whether or not anyone is connected through it.
    const s = revokeConsequence({ n1: 0 }, "n1");
    expect(s).toContain("never active again");
    expect(s).not.toContain("device is");
    expect(s).not.toContain("devices are");
    expect(revokeConsequence({}, "n1")).toBe(s);
    expect(revokeConsequence({ n1: 50 }, "n2")).toBe(s);
  });

  it("agrees in number for a single device", () => {
    const s = revokeConsequence({ n1: 1 }, "n1");
    expect(s).toContain("1 device is");
    expect(s).toContain("The device can be moved");
  });

  // ⛔ THE FAILURE CASE, WHICH A `{}` DEFAULT WOULD HAVE GOT WRONG. An unreadable devices list must not
  // render as "no devices homed here" — a silent all-clear manufactured by a failure, on the one sentence
  // whose whole job is to stop a destructive click.
  it("says it could not count rather than implying zero when the read failed", () => {
    const s = revokeConsequence(null, "n1");
    expect(s).toContain("could not be counted");
    expect(s).toContain("never active again");
    expect(s).toContain("moved to another gateway");
  });
});
