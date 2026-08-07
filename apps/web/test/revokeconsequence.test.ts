import { describe, expect, it } from "vitest";
import { revokeConsequence } from "../src/lib/gatewaysview";

// ⛔ THE CONFIRM MUST COUNT THE PEOPLE IT DISCONNECTS. Revoking a gateway cascades to every device homed to
// it, and the ceiling notice sends operators here to free a licence slot — the exact frame in which a
// fifty-device gateway looks like housekeeping.
describe("revokeConsequence", () => {
  it("names the count and the immediacy when devices are homed there", () => {
    const s = revokeConsequence({ n1: 50 }, "n1");
    expect(s).toContain("50 devices");
    expect(s).toContain("stop connecting immediately");
  });

  it("is SILENT when nothing is homed there", () => {
    // A caution that fires on the harmless case is a caution nobody reads on the dangerous one.
    expect(revokeConsequence({ n1: 50 }, "n2")).toBeNull();
    expect(revokeConsequence({ n1: 0 }, "n1")).toBeNull();
    expect(revokeConsequence({}, "n1")).toBeNull();
  });

  it("agrees in number for a single device", () => {
    expect(revokeConsequence({ n1: 1 }, "n1")).toContain("1 device is");
  });

  // ⛔ THE FAILURE CASE, WHICH IS THE ONE A `{}` DEFAULT WOULD HAVE GOT WRONG. An unreadable devices list
  // must not render as "no devices homed here" — that is a silent all-clear manufactured by a failure, on
  // the one sentence whose whole job is to stop a destructive click.
  it("says it could not count rather than falling silent when the read failed", () => {
    const s = revokeConsequence(null, "n1");
    expect(s).not.toBeNull();
    expect(s).toContain("could not be counted");
  });
});
