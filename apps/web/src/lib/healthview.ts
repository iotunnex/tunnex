import type { Node } from "./api";

// policyHealthBadge — the S7.4b differentiated gateway-health badge, a PURE projection.
// The `policy_degraded` BOOL is PRIMARY: not degraded → no badge (healthy). When degraded, the
// KIND refines the label + tone, but the badge is NEVER less alarmed than the bool (never an
// "ok" tone, never null, while degraded). `converging` is a normal push settling → a subtle
// "syncing", not a loud alarm; `silent_desync` is the stuck, actionable case; `desync_unknown`
// is the honest can't-determine (never rendered as healthy).
export type BadgeTone = "warn" | "danger" | "unknown";

export interface HealthBadge {
  label: string;
  tone: BadgeTone;
}

export function policyHealthBadge(
  node: Pick<Node, "policy_degraded" | "policy_degraded_kind">,
): HealthBadge | null {
  if (!node.policy_degraded) return null; // bool primary — not degraded → no badge
  switch (node.policy_degraded_kind) {
    case "converging":
      return { label: "syncing…", tone: "warn" };
    case "apply_failing":
      return { label: "apply failing", tone: "warn" };
    case "stuck_enforcing":
      return { label: "enforcing a disabled policy", tone: "danger" };
    case "silent_desync":
      return { label: "silent desync", tone: "danger" };
    case "desync_unknown":
      return { label: "health unknown", tone: "unknown" };
    case "unsupported_policy_version":
      return { label: "agent too old", tone: "danger" }; // refused the artifact → deny-all; remedy: upgrade
    case "site_hub_down":
      return { label: "site hub unreachable", tone: "danger" }; // S8.2: no carrier for site-to-site traffic
    case "site_link_down":
      return { label: "site link down", tone: "danger" }; // S8.2: a site-to-site tunnel has no fresh handshake
    case "site_subnet_unreachable":
      return { label: "site subnet unreachable", tone: "danger" }; // S8.2c: advertises a LAN the gateway isn't on (bridge-trapped)
    case "conntrack_flush_unavailable":
      return { label: "expiry-flush degraded", tone: "warn" }; // S8.7: can't tear down expired-grant flows (CAP_NET_ADMIN?) — revoked flows may linger
    case "cert_expired_cannot_reconnect":
      // S11 WF-S11-6: the agent's cert expired, so it cannot authenticate to the CP — including the renewal
      // endpoint, which needs the cert that expired. The label carries the REMEDY because no other kind's
      // remedy applies and waiting is actively wrong: this never self-heals.
      return {
        label: "certificate expired, re-enroll this gateway",
        tone: "danger",
      };
    case "k8s_endpoints_unavailable":
      // S10.3 WF-K5 — added here by the S11 mirror-surface census (WF-S11-7): the kind shipped in the Go enum
      // and the metrics but never reached the spec or this renderer, so it fell through to the generic
      // degraded badge and its named remedy was invisible in the product.
      return {
        label: "no Kubernetes endpoint view (check API access + RBAC)",
        tone: "danger",
      };
    case "hub_forwarding_not_reconciling":
      // WF-C L2: zombie hub — wire fresh, agent dead. The label names BOTH halves so it lies in neither
      // direction (not "offline" — it forwards; not "healthy" — it's stale). Remedy: restart the agent
      // (the wire is fine, the brain is dead). Danger: it enforces a policy the CP has since changed.
      return {
        label: "agent down, still forwarding (restart agent)",
        tone: "danger",
      };
    default:
      // Degraded per the authoritative bool but the kind is absent/healthy — still show a
      // badge (never less alarmed than the bool).
      return { label: "degraded", tone: "warn" };
  }
}

// SiteLinkNote — WF-B: the SUBORDINATE site-link line, INDEPENDENT of the headline badge
// (policyHealthBadge). A DEMOTED hub member whose link is dead WHILE org transit rides the active
// primary (healthy): the site's headline stays its real state and this names the demoted-dead peer as a
// distinct line ("site link down: aws-gw-1 (demoted)"). The `(demoted)` qualifier tells the operator
// "expected — this member was failed-over-past" vs a live peer's real outage. NEVER accompanies a
// `site_link_down` HEADLINE (the CP never sets the note then — the inverse-red guard).
export interface SiteLinkNote {
  peer: string;
  demoted: boolean;
}

export function siteLinkNote(
  node: Pick<Node, "site_link_note_peer" | "site_link_note_demoted">,
): SiteLinkNote | null {
  if (!node.site_link_note_peer) return null; // render-floor: the field it consumes, present ⇒ a note
  return {
    peer: node.site_link_note_peer,
    demoted: node.site_link_note_demoted ?? false,
  };
}

/**
 * ⛔ A PILL, NOT BARE TEXT — corrected S14.6, founder-caught, and it was product-wide.
 *
 * This returned COLOUR ONLY: `text-amber-400` / `text-rose-400`. So on every surface that renders health
 * beside other states, a DEGRADED gateway showed as bare coloured text while its `healthy` and `revoked`
 * siblings showed as bordered pills — one state styled as a different KIND of thing from the others.
 *
 * The name said badge and the function returned a colour. Four call sites inherited that, and the wireframe
 * badges every state uniformly (`HEALTHY`, `APPLY_FAILING`, `DESYNC_UNKNOWN`, `SITE_LINK_DOWN`,
 * `UNSUPPORTED_VER` are all pills).
 *
 * ⚠ FIXED IN THE HELPER RATHER THAN AT THE CALL SITE, deliberately: a fix at one call site does not reach
 * the call sites beside it — the missing-primitive law, which this repo has now paid for several times.
 * Matches `Badge`'s recipe so the two cannot drift.
 */
export function badgeClass(tone: BadgeTone): string {
  const colour = {
    warn: "border-warn/40 text-warn",
    danger: "border-danger/40 text-danger",
    unknown: "border-white/10 text-slate-400",
  }[tone];
  return `inline-flex items-center rounded-full border px-2 py-0.5 text-micro ${colour}`;
}
