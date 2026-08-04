
/**
 * The AI-agent surface's view model — S15.3.
 *
 * ⛔ THE RENDER FLOOR IS THE FIRST THING IN THIS FILE, BECAUSE IT CONSTRAINS EVERY STRING BELOW.
 *
 * > **UNDER PROMPT INJECTION, AUTHENTICATION IS INTACT AND AUTHORIZATION IS INTACT. ONLY *INTENT* IS
 * > CORRUPTED. ZERO TRUST BOUNDS THE BLAST RADIUS OF A CORRECTLY-AUTHENTICATED PRINCIPAL. IT DOES NOT
 * > DETECT INJECTION.**
 *
 * Two claims this surface may never make, in any copy, at any size:
 *
 *  · ⛔ **DETECTION** — "catches", "blocks", "prevents" a manipulated request. The product does not inspect
 *    intent. A boundary LIMITS WHAT A REQUEST CAN REACH; it does not know the request was manipulated.
 *  · ⛔ **PER-TOOL CONTROL** — enforcement is five fields (`SrcIP, DstCIDR, Protocol, PortLow, PortHigh`).
 *    A tool name is not among them and cannot be. Teleport does per-tool; we do not, and the claim is
 *    checkable and false.
 *
 * ⚠ THE HONEST VERB IS **REACH**. This surface says which agents may reach which destinations. It never
 * says which ACTIONS they may perform there, because the enforcement plane cannot see actions.
 */

/**
 * One agent, as the screen understands it — served whole by `GET /organizations/{id}/agents`.
 *
 * ⚠ ONE SURFACE, ONE SOURCE. This used to be assembled client-side by joining `nodes` to `devices`, which
 * made the screen a second place deriving "which nodes are agents". The server answers it now, from the
 * marker, and the join is gone.
 */
export interface AgentRow {
  node_id: string;
  name: string;
  enrolment_kind: EnrolmentKind;
  owner_email: string | null;
  unattributable: boolean;
  address: string | null;
  status: string;
}

/**
 * Order: **unattributable first, then undetermined, then the rest.**
 *
 * ⚠ THE TWO STATES AN OPERATOR CANNOT LEARN ANYWHERE ELSE COME FIRST, and neither may depend on a name.
 */
export function sortAgents(rows: AgentRow[]): AgentRow[] {
  const rank = (a: AgentRow) =>
    a.unattributable ? 0 : a.enrolment_kind === "undetermined" ? 1 : 2;
  return [...rows].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}

/**
 * What the screen says about an agent's attribution.
 *
 * ⚠ TONE IS `warn`, NEVER `danger` — the same ruling as the gateway badge. An unattributable tunnel is a
 * LOGGING failure, not an access-control one. Painting it red claims a security failure that has not
 * occurred, and over-alarming is the same defect as under-alarming, facing the other way.
 */
export function attributionNote(
  a: Pick<AgentRow, "unattributable">,
): { label: string; tone: "warn"; detail: string } | null {
  if (!a.unattributable) return null;
  return {
    label: "unattributable",
    tone: "warn",
    detail:
      "No owner is recorded for this agent, so its activity cannot be attributed to a person. It keeps running and policy still applies to it — this is a gap in the audit trail, not in access control.",
  };
}

/**
 * The empty state's words.
 *
 * ⛔ "NO AGENTS" AND "COULD NOT LOAD" ARE DIFFERENT CLAIMS and the caller must keep them apart — this
 * returns only the former. A failed load rendering as "no agents" is a zero nobody measured.
 */
export const NO_AGENTS =
  "No AI agents are enrolled in this organization. An agent is enrolled with a join token, the same way a gateway is — it then appears here with the person who authorised it.";


/**
 * ⛔ THE UNDETERMINED STATE — ITS WORDS ARE RULED, AND THEY ARE PINNED LIKE THE RENDER FLOOR.
 *
 * A node enrolled before the marker existed (`enrolled_kind IS NULL`) is **neither an agent nor
 * not-an-agent**. The fact was never recorded, the join token that would have carried it is consumed, and
 * its intent was never asked for — so it **cannot be recovered**.
 *
 * > **NOT "not an agent"** — that asserts a fact nobody has.
 * > **NOT "agent"** — that repeats the defect the marker was built to fix.
 *
 * ⚠ AND IT MUST NOT READ AS A FAULT. These nodes are working correctly. **The gap is in our record, not in
 * them**, and copy that implies otherwise sends an operator to debug a healthy gateway.
 *
 * ⛔ THE PHRASE MUST NOT DRIFT INTO A VERDICT. "Unknown" softening into "none" is exactly how it would —
 * one is an absence of knowledge, the other is a claim about the world. The test enforces the difference.
 */
export const UNDETERMINED_LABEL = "enrolment kind not recorded";

export const UNDETERMINED_DETAIL =
  "We do not know what this was enrolled as. This node was enrolled before Tunnex recorded that choice, so the answer was never captured and cannot be recovered. The node is working normally — this is a gap in our record, not a problem with it.";

/**
 * Which of the three states a node is in.
 *
 * ⚠ THREE, NOT TWO. A boolean here would force undetermined into one of the other two, which is the exact
 * failure the ruling exists to prevent.
 */
export type EnrolmentKind = "agent" | "gateway" | "undetermined";

export function enrolmentKind(n: { enrolled_kind?: string | null }): EnrolmentKind {
  if (n.enrolled_kind === "agent") return "agent";
  if (n.enrolled_kind === "gateway") return "gateway";
  return "undetermined";
}

/**
 * The Overview card's words — S15.3.
 *
 * ⛔ COUNTS AND ONE NAMED GAP. NOTHING ELSE. A card is where copy gets shortened until it implies things,
 * so §0's two forbidden claims bind hardest here: no DETECTION, no PER-TOOL. It says how many agents exist
 * and how many cannot be tied to a person — both facts the server actually holds.
 *
 * ⚠ AND IT IS NOT A HEALTH VERDICT. "3 agents, 1 unattributable" is a count and an audit gap. It is not
 * "you are secure", not "all good", and not a claim about what any agent is doing.
 */
export function agentSummary(rows: Pick<AgentRow, "unattributable">[]): {
  total: number;
  unattributable: number;
  note: string | null;
} {
  const unattributable = rows.filter((r) => r.unattributable).length;
  return {
    total: rows.length,
    unattributable,
    // ⚠ The gap is named only when it exists. A permanent "0 unattributable" would train the reader to
    // stop seeing the line — and it is the line that matters when it is not zero.
    note:
      unattributable > 0
        ? `${unattributable} cannot be attributed to a person`
        : null,
  };
}
