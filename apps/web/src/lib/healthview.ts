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

export function policyHealthBadge(node: Pick<Node, "policy_degraded" | "policy_degraded_kind">): HealthBadge | null {
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

export function siteLinkNote(node: Pick<Node, "site_link_note_peer" | "site_link_note_demoted">): SiteLinkNote | null {
  if (!node.site_link_note_peer) return null; // render-floor: the field it consumes, present ⇒ a note
  return { peer: node.site_link_note_peer, demoted: node.site_link_note_demoted ?? false };
}

export function badgeClass(tone: BadgeTone): string {
  return {
    warn: "text-amber-400",
    danger: "text-rose-400",
    unknown: "text-slate-400",
  }[tone];
}
