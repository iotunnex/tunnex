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
    default:
      // Degraded per the authoritative bool but the kind is absent/healthy — still show a
      // badge (never less alarmed than the bool).
      return { label: "degraded", tone: "warn" };
  }
}

export function badgeClass(tone: BadgeTone): string {
  return {
    warn: "text-amber-400",
    danger: "text-rose-400",
    unknown: "text-slate-400",
  }[tone];
}
