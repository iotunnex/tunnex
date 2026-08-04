import { describe, expect, it } from "vitest";
import { agentRows, attributionNote, NO_AGENTS } from "../src/lib/agentview";

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
