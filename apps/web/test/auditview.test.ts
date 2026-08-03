import { describe, expect, it } from "vitest";
import {
  UNATTRIBUTED_NOTE,
  resolveActor,
  systemActors,
  unattributedCount,
} from "../src/lib/auditview";
import type { Member } from "../src/lib/api";

const members = [
  { user_id: "u1", name: "Ada Lovelace", email: "ada@acme.io", role: "owner" },
] as unknown as Member[];

// ⛔ "system" MEANT TWO DIFFERENT THINGS ON THE SAME SCREEN.
//
// The shipped cell was `a.actor_id ? actorName(members, a.actor_id) : "system"`. Measured across
// 100 served rows: 40 carried actor_id, 26 carried a NAMED actor_system, and 34 carried NEITHER.
// The last two groups rendered identically — so the name was discarded, and discarding it hid an
// attribution gap behind the word already used for "known, and here is its name".
describe("resolveActor", () => {
  it("⛔ renders a NAMED system actor by its name, never as the generic word", () => {
    const a = resolveActor({ actor_system: "idp-sync", action: "x" }, members);
    expect(a.kind).toBe("system");
    expect(a.label).toBe("idp-sync");
    expect(a.label).not.toBe("system");
    expect(a.gap).toBe(false); // fully attributed — not a fallback
  });

  it("⛔ an UNATTRIBUTED row is its own state, and is flagged as a gap", () => {
    // The state the old code hid. It must not collapse into the named-system arm.
    const a = resolveActor({ action: "hub_set.promotion" }, members);
    expect(a.kind).toBe("unattributed");
    expect(a.gap).toBe(true);
    expect(a.label).toMatch(/not recorded/i);
  });

  it("⛔ named-system and unattributed never render the same label", () => {
    // One assertion for the whole defect: these two were indistinguishable.
    const named = resolveActor({ actor_system: "device-health", action: "x" }, members);
    const none = resolveActor({ action: "x" }, members);
    expect(named.label).not.toBe(none.label);
    expect(named.gap).not.toBe(none.gap);
  });

  it("resolves a human on the roster to their name", () => {
    const a = resolveActor({ actor_id: "u1", action: "x" }, members);
    expect(a.kind).toBe("human");
    expect(a.label).toBe("Ada Lovelace");
    expect(a.gap).toBe(false);
  });

  it("⛔ a departed human is ATTRIBUTED but unnamed — not a gap", () => {
    // We know WHO acted, we just cannot name them. Folding this into "unattributed" would
    // overstate the defect and make the gap count wrong.
    const a = resolveActor({ actor_id: "deadbeef-1111", action: "x" }, members);
    expect(a.kind).toBe("unknown_human");
    expect(a.gap).toBe(false);
    expect(a.label).toMatch(/former member/i);
  });

  it("prefers the system actor when a row somehow carries both", () => {
    // Defensive: `actor_system` is the more specific claim, and a row with both is malformed.
    const a = resolveActor({ actor_id: "u1", actor_system: "idp-sync", action: "x" }, members);
    expect(a.kind).toBe("system");
  });

  it("treats a blank actor field as absent, not as a name", () => {
    expect(resolveActor({ actor_system: "   ", action: "x" }, members).kind).toBe("unattributed");
    expect(resolveActor({ actor_id: "  ", action: "x" }, members).kind).toBe("unattributed");
  });
});

describe("unattributedCount", () => {
  it("counts only the rows with no actor at all", () => {
    const rows = [
      { actor_id: "u1", action: "a" },
      { actor_system: "idp-sync", action: "b" },
      { action: "c" },
      { action: "d" },
    ];
    expect(unattributedCount(rows)).toBe(2);
  });

  it("is zero when every row is attributed", () => {
    expect(unattributedCount([{ actor_system: "device-health", action: "a" }])).toBe(0);
  });
});

describe("systemActors", () => {
  it("derives the names from the rows rather than hardcoding them", () => {
    // Hardcoding would go stale the moment a new writer is added — S14.15 added one.
    const rows = [
      { actor_system: "idp-sync", action: "a" },
      { actor_system: "device-health", action: "b" },
      { actor_system: "idp-sync", action: "c" },
      { actor_id: "u1", action: "d" },
    ];
    expect(systemActors(rows)).toEqual(["device-health", "idp-sync"]);
  });
});

describe("UNATTRIBUTED_NOTE", () => {
  it("⛔ says the gap is OURS, not evidence that nobody acted", () => {
    // "not recorded" reads as a property of the event; it is a property of our write path.
    expect(UNATTRIBUTED_NOTE).toMatch(/gap in how we record/i);
    expect(UNATTRIBUTED_NOTE).toMatch(/not evidence that nobody acted/i);
  });
});
