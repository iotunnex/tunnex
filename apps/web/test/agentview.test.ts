import { describe, expect, it } from "vitest";
import {
  agentRows, attributionNote, enrolmentKind, NO_AGENTS,
  UNDETERMINED_DETAIL, UNDETERMINED_LABEL,
} from "../src/lib/agentview";

const node = (o: Partial<Record<string, unknown>> = {}) => ({
  id: String(o.id ?? "n1"),
  name: String(o.name ?? "agent-a"),
  status: "active",
  agent_version: "1",
  enrolled_at: new Date().toISOString(),
  owner_email: "owner_email" in o ? o.owner_email : "owner@demo.tunnex.local",
  unattributable: o.unattributable ?? false,
}) as never;

const dev = (o: Partial<Record<string, unknown>> = {}) => ({
  id: String(o.id ?? "d1"),
  user_id: "u1",
  node_id: String(o.node_id ?? "n1"),
  name: "x",
  public_key: "k",
  status: "active",
  created_at: new Date().toISOString(),
  kind: o.kind ?? "agent",
  assigned_ip: "assigned_ip" in o ? o.assigned_ip : "10.99.0.4",
}) as never;

describe("the agent surface — S15.3", () => {
  it("⛔ ONLY nodes with an AGENT device row are agents — a gateway is not one", () => {
    const rows = agentRows(
      [node({ id: "n1", name: "agent-a" }), node({ id: "n2", name: "plain-gateway" })],
      [dev({ node_id: "n1" }), dev({ id: "d2", node_id: "n2", kind: "human" })],
    );
    expect(rows.map((r) => r.name)).toEqual(["agent-a"]);
  });

  it("⛔ AN UNATTRIBUTABLE AGENT SORTS FIRST — it is the one state found nowhere else", () => {
    const rows = agentRows(
      [
        node({ id: "n1", name: "aaa-owned", unattributable: false }),
        node({ id: "n2", name: "zzz-orphan", unattributable: true, owner_email: null }),
      ],
      [dev({ node_id: "n1" }), dev({ id: "d2", node_id: "n2" })],
    );
    // ⚠ Alphabetically 'aaa' precedes 'zzz'; the ordering must NOT depend on the name.
    expect(rows[0].name).toBe("zzz-orphan");
    expect(rows[0].unattributable).toBe(true);
  });

  it("⛔ THE ABSENCES ARE FIRST-CLASS — no owner and no address render as null, never as a guess", () => {
    const rows = agentRows(
      [node({ id: "n1", unattributable: true, owner_email: null })],
      [dev({ node_id: "n1", assigned_ip: null })],
    );
    expect(rows[0].ownerEmail).toBeNull();
    expect(rows[0].address).toBeNull();
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
