import { describe, expect, it } from "vitest";
import {
  agentSummary, attributionNote, enrolmentKind, NO_AGENTS, sortAgents,
  UNDETERMINED_DETAIL, UNDETERMINED_LABEL, type AgentRow,
} from "../src/lib/agentview";

describe("the agent surface — S15.3", () => {
  const row = (o: Partial<AgentRow> = {}): AgentRow => ({
    node_id: o.node_id ?? "n1",
    name: o.name ?? "agent-a",
    enrolment_kind: o.enrolment_kind ?? "agent",
    owner_email: "owner_email" in o ? (o.owner_email ?? null) : "owner@demo.tunnex.local",
    unattributable: o.unattributable ?? false,
    address: "address" in o ? (o.address ?? null) : "10.99.0.4",
    status: o.status ?? "active",
  });

  it("⛔ THE TWO STATES FOUND NOWHERE ELSE SORT FIRST — unattributable, then undetermined", () => {
    const sorted = sortAgents([
      row({ node_id: "n1", name: "aaa-normal" }),
      row({ node_id: "n2", name: "zzz-undetermined", enrolment_kind: "undetermined" }),
      row({ node_id: "n3", name: "mmm-orphan", unattributable: true, owner_email: null }),
    ]);
    // ⚠ Neither may depend on a name: alphabetically this order would be aaa, mmm, zzz.
    expect(sorted.map((r) => r.name)).toEqual(["mmm-orphan", "zzz-undetermined", "aaa-normal"]);
  });

  it("⛔ THE ABSENCES ARE FIRST-CLASS — no owner and no address stay null, never a guess", () => {
    const [r] = sortAgents([row({ owner_email: null, address: null, unattributable: true })]);
    expect(r.owner_email).toBeNull();
    expect(r.address).toBeNull();
  });

  describe("the attribution note", () => {
    it("names the gap and says the agent KEEPS RUNNING", () => {
      const n = attributionNote({ unattributable: true })!;
      expect(n.label).toMatch(/unattributable/i);
      expect(n.detail).toMatch(/keeps running/i);
      expect(n.detail).toMatch(/audit trail, not in access control/i);
    });

    it("⚠ TONE IS warn, NEVER danger — a logging gap is not an access-control failure", () => {
      expect(attributionNote({ unattributable: true })!.tone).toBe("warn");
    });

    it("is absent for an attributable agent — without this the note could be a constant", () => {
      expect(attributionNote({ unattributable: false })).toBeNull();
    });
  });

  // ⛔ THE RENDER FLOOR, ENFORCED ON THE COPY ITSELF. These are the two claims the product cannot keep.
  it("⛔ NO DETECTION AND NO PER-TOOL LANGUAGE anywhere in the surface's copy", () => {
    const copy = [
      NO_AGENTS,
      attributionNote({ unattributable: true })!.label,
      attributionNote({ unattributable: true })!.detail,
    ].join(" ");
    for (const forbidden of [
      /\bdetect\w*/i, /\bblocks?\b/i, /\bprevent\w*/i, /\bprompt injection\b/i,
      /\btool\b/i, /\bper-tool\b/i, /\bsecure\b/i, /\bprotected\b/i,
    ]) {
      expect(copy).not.toMatch(forbidden);
    }
  });
});

// ⛔ THE UNDETERMINED STATE'S WORDS ARE PINNED THE WAY THE RENDER FLOOR IS PINNED.
//
// The ruling: undetermined means *we do not know what this was enrolled as, because the fact was not
// recorded at the time and cannot be recovered*. It is NOT "not an agent" (a fact nobody has), NOT "agent"
// (the defect the marker fixed), and NOT a fault (these nodes work correctly).
//
// > **"UNKNOWN" SOFTENING INTO "NONE" IS EXACTLY HOW THE PHRASE WOULD DRIFT INTO A VERDICT** — one is an
// > absence of knowledge, the other is a claim about the world.
describe("the UNDETERMINED state — S15.3, ruled before the surface", () => {
  it("⛔ THREE STATES, NOT TWO — a boolean would force undetermined into one of the others", () => {
    expect(enrolmentKind({ enrolled_kind: "agent" })).toBe("agent");
    expect(enrolmentKind({ enrolled_kind: "gateway" })).toBe("gateway");
    // NULL and absent both mean undetermined — a pre-marker node, and a payload that omits the field.
    expect(enrolmentKind({ enrolled_kind: null })).toBe("undetermined");
    expect(enrolmentKind({})).toBe("undetermined");
  });

  it("⛔ THE COPY SAYS WE DO NOT KNOW — never that there is nothing", () => {
    const copy = `${UNDETERMINED_LABEL} ${UNDETERMINED_DETAIL}`;
    expect(copy).toMatch(/do not know/i);
    expect(copy).toMatch(/cannot be recovered/i);
    // ⛔ NOT A VERDICT. These are the words it must never drift into.
    for (const verdict of [/\bnone\b/i, /\bnot an agent\b/i, /\bno agent\b/i, /\bis a gateway\b/i]) {
      expect(copy).not.toMatch(verdict);
    }
  });

  it("⚠ AND IT MUST NOT READ AS A FAULT — the gap is in our record, not in the node", () => {
    expect(UNDETERMINED_DETAIL).toMatch(/working normally/i);
    expect(UNDETERMINED_DETAIL).toMatch(/gap in our record/i);
    for (const fault of [/\berror\b/i, /\bfailed?\b/i, /\bbroken\b/i, /\bmisconfigur/i, /\bproblem with (this|the) node\b/i]) {
      expect(UNDETERMINED_DETAIL).not.toMatch(fault);
    }
  });
});

describe("the Overview card — S15.3", () => {
  const r = (u: boolean) => ({ unattributable: u });

  it("counts, and names the gap only when it exists", () => {
    expect(agentSummary([r(false), r(false)])).toMatchObject({ total: 2, unattributable: 0, note: null });
    const s = agentSummary([r(true), r(false), r(true)]);
    expect(s).toMatchObject({ total: 3, unattributable: 2 });
    expect(s.note).toMatch(/cannot be attributed to a person/i);
  });

  // ⛔ §0 BINDS HARDEST AT CARD SIZE — this is where copy gets shortened until it implies things.
  it("⛔ the card's copy makes no detection, per-tool or health claim", () => {
    const copy = agentSummary([r(true)]).note ?? "";
    for (const forbidden of [
      /\bdetect\w*/i, /\bblocks?\b/i, /\bprevent\w*/i, /\btool\b/i,
      /\bsecure\b/i, /\bprotected\b/i, /\ball good\b/i, /\bhealthy\b/i,
    ]) {
      expect(copy).not.toMatch(forbidden);
    }
  });
});
