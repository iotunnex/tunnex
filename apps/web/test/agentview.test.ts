import { describe, expect, it } from "vitest";
import {
  agentConnectCommand, agentSummary, attributionNote, NO_AGENTS, sortAgents, type AgentRow,
} from "../src/lib/agentview";

describe("the agent surface — S15.3", () => {
  const row = (o: Partial<AgentRow> = {}): AgentRow => ({
    device_id: o.device_id ?? "d1",
    name: o.name ?? "agent-a",
    owner_email: "owner_email" in o ? (o.owner_email ?? null) : "owner@demo.tunnex.local",
    unattributable: o.unattributable ?? false,
    address: "address" in o ? (o.address ?? null) : "10.99.0.4",
    gateway_name: o.gateway_name ?? "gw-1",
    status: o.status ?? "active",
  });

  it("⛔ UNATTRIBUTABLE SORTS FIRST — the one state found nowhere else", () => {
    const sorted = sortAgents([
      row({ device_id: "d1", name: "aaa-normal" }),
      row({ device_id: "d2", name: "zzz-normal" }),
      row({ device_id: "d3", name: "mmm-orphan", unattributable: true, owner_email: null }),
    ]);
    // ⚠ Must not depend on a name: alphabetically this order would be aaa, mmm, zzz.
    expect(sorted.map((r) => r.name)).toEqual(["mmm-orphan", "aaa-normal", "zzz-normal"]);
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

describe("the connect command — S15.3", () => {
  const conf = "[Interface]\nPrivateKey = k+ey/with$dollar=\nAddress = 10.99.0.7/32\n";

  it("⛔ ONE COMMAND, not a file to save and then a command to run", () => {
    const c = agentConnectCommand(conf);
    expect(c).toMatch(/tee \/etc\/wireguard\/tunnex\.conf/);
    expect(c).toMatch(/wg-quick up tunnex/);
  });

  it("⛔ THE HEREDOC IS QUOTED — an unquoted one would let the shell mangle a key containing $", () => {
    const c = agentConnectCommand(conf);
    expect(c).toContain("<<'TUNNEXEOF'");
    // the key survives verbatim
    expect(c).toContain("k+ey/with$dollar=");
  });

  it("⚠ the config is chmod 600 — a private key must not be world-readable", () => {
    expect(agentConnectCommand(conf)).toMatch(/chmod 600/);
  });
});
