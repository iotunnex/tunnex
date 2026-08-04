import type { components } from "@tunnex/shared";

type Device = components["schemas"]["Device"];
type Node = components["schemas"]["Node"];

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

/** One agent, as the screen understands it. */
export interface AgentRow {
  nodeId: string;
  name: string;
  /** The human this agent acts for — the join token's ISSUER, resolved server-side. */
  ownerEmail: string | null;
  /**
   * ⛔ UNATTRIBUTABLE IS A STATEMENT ABOUT THE AUDIT TRAIL, NEVER ABOUT PERMISSION. An unattributable agent
   * is NOT less authorized — the policy engine enforces every rule identically. It keeps running, and what
   * is lost is the ability to tie its activity to a person.
   */
  unattributable: boolean;
  /** Its own /32, which is what makes it attributable in the flow log at all. */
  address: string | null;
  status: string;
}

/**
 * Build the agent rows from what the API already serves.
 *
 * ⚠ AN AGENT IS A NODE **AND** A DEVICE ROW, AND BOTH HALVES ARE NEEDED. `nodes` carries the owner and the
 * unattributable flag; the `devices` row carries the /32. Neither alone can answer the screen's questions.
 */
export function agentRows(nodes: Node[], devices: Device[]): AgentRow[] {
  const agentDevices = new Map<string, Device>();
  for (const d of devices) {
    if (d.kind === "agent" && d.node_id) agentDevices.set(d.node_id, d);
  }
  return nodes
    .filter((n) => agentDevices.has(n.id))
    .map((n) => ({
      nodeId: n.id,
      name: n.name,
      ownerEmail: n.owner_email ?? null,
      unattributable: n.unattributable === true,
      address: agentDevices.get(n.id)?.assigned_ip ?? null,
      status: n.status,
    }))
    .sort((a, b) =>
      // ⚠ UNATTRIBUTABLE FIRST. It is the one state an operator cannot learn anywhere else, and burying it
      // alphabetically would make the screen's most important claim depend on a name.
      a.unattributable === b.unattributable
        ? a.name.localeCompare(b.name)
        : a.unattributable
          ? -1
          : 1,
    );
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
