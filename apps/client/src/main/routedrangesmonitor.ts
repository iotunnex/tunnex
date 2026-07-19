import type { DeviceApi } from "./deviceconfig";
import type { ResolverForward } from "./helperclient";
import { REVOKE_POLL_MS, REVOKE_BACKOFF_MAX_MS } from "./revocation";

// canonRanges dedups + sorts a CIDR list into a stable canonical form for change-compare. The server
// already returns its ranges canonical + sorted (S8.5 2b); we merge in the baked base and re-sort so the
// compare is order-free (the peersEqual lesson at the client tier — compare canonical, never raw).
export function canonRanges(cidrs: string[]): string[] {
  return Array.from(new Set(cidrs.map((c) => c.trim()).filter((c) => c.length > 0))).sort();
}

// canonForwards folds the forward set into a stable string for change-compare (S8.5 Slice 3). Normalized
// (lowercased domain, trimmed) + deduped by the domain=resolver pair + sorted — so a reordered or
// case-shuffled server response is NOT a change and emits ZERO helper calls (the DNS tier's no-churn half).
export function canonForwards(fwds: ResolverForward[]): string {
  return Array.from(
    new Set(fwds.map((f) => `${f.domain.trim().toLowerCase()}=${f.resolver_ip.trim()}`)),
  )
    .sort()
    .join(",");
}

export type RangesOutcome = "skipped" | "applied" | "unchanged" | "inconclusive";

// RoutedRangesMonitor is the S8.5 volatile-config poll — the RevocationMonitor's sibling (same cadence,
// self-scheduling lifecycle, error posture). While a SPLIT-tunnel session is up it fetches the org's
// declared ranges AND reachable DNS forwards in ONE poll, and live-applies each tier via the helper ONLY
// when it changes: ranges → set_allowed_ips (base ∪ ranges), forwards → set_resolvers (the full gated set).
// It is NOT constructed for a full-tunnel session (0.0.0.0/0 subsumes every range and the DNS is the
// full-tunnel resolver — the CLIENT layer skips, so no pointless privileged call; ipc.ts owns that gate).
//
// Discipline (BOTH tiers, independently):
//   - fail-STATIC: a poll THROW (or an apply THROW) keeps that tier's last-applied set — a CP blip never
//     un-routes the office LAN nor drops its DNS. Backoff lengthens the next probe.
//   - no-CHURN: canonical compare → apply only on change. N identical polls = ZERO helper calls.
//   - full-SWEEP: ranges = base ∪ current-ranges (never accumulated); forwards = the full current gated
//     set (helper reconciles owned files) — a removed range/forward vanishes on the next differing poll.
// The two tiers are INDEPENDENT: a set_resolvers failure never blocks a routes apply, and vice-versa —
// DNS and routing degrade separately (a resolver-write refusal must not strand the office-LAN route).
export class RoutedRangesMonitor {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private running = false;
  private inFlight = false;
  private backoff = 0;
  // lastRanges = the canonical join of the AllowedIPs the helper currently holds. Seeded to the BAKED
  // BASE: tunnel_up already applied it, so an empty-ranges org yields "unchanged" on the ranges tier —
  // zero client work (the D5 empty-channel golden's client half).
  private lastRanges: string;
  // lastForwards = the canonical fold of the resolvers the helper currently holds. Seeded EMPTY: up()
  // applies dns_forwards=[] (forwards are volatile, never baked), so an empty-forwards org is "unchanged".
  private lastForwards = "";

  constructor(
    private readonly orgId: string,
    private readonly base: string[], // the baked-stable AllowedIPs (split-tunnel = [pool]) — never dropped
    private readonly api: Pick<DeviceApi, "routedConfig">,
    private readonly applyRanges: (allowedIPs: string[]) => Promise<void>, // TunnelController.setAllowedIPs
    private readonly applyForwards: (fwds: ResolverForward[]) => Promise<void>, // TunnelController.setResolvers
    private readonly baseMs: number = REVOKE_POLL_MS, // reuse the revocation cadence (config changes rarely)
    private readonly maxMs: number = REVOKE_BACKOFF_MAX_MS,
    private readonly setTimer: (cb: () => void, ms: number) => ReturnType<typeof setTimeout> = (cb, ms) => {
      const t = setTimeout(cb, ms);
      t.unref?.();
      return t;
    },
    private readonly clearTimer: (t: ReturnType<typeof setTimeout>) => void = (t) => clearTimeout(t),
  ) {
    this.lastRanges = canonRanges(base).join(",");
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.stopped = false;
    this.backoff = 0;
    // IMMEDIATE first poll (the mint-ruling-A condition): a NEW device gets its ranges + forwards within
    // SECONDS of connecting, not after a full 30s interval — shrinking the new-device gap so ruling A's
    // "poll covers new devices" trade is nearly free. loop() runs checkOnce NOW, then reschedules.
    void this.loop();
  }

  stop(): void {
    this.stopped = true;
    this.running = false;
    if (this.timer) {
      this.clearTimer(this.timer);
      this.timer = null;
    }
  }

  // checkOnce runs a single poll then applies BOTH tiers, each on change only, each fail-static. Exposed
  // for tests.
  async checkOnce(): Promise<RangesOutcome> {
    if (this.stopped || this.inFlight) return "skipped";
    this.inFlight = true;
    try {
      const cfg = await this.api.routedConfig(this.orgId);
      if (this.stopped) return "skipped"; // disconnecting — abandon the result
      const merged = canonRanges([...this.base, ...cfg.ranges]);
      const rangesKey = merged.join(",");
      const forwardsKey = canonForwards(cfg.forwards);

      let changed = false;
      let failed = false;
      // RANGES tier — apply base ∪ ranges via set_allowed_ips on change; fail-static (keep lastRanges).
      if (rangesKey !== this.lastRanges) {
        try {
          await this.applyRanges(merged);
          this.lastRanges = rangesKey;
          changed = true;
        } catch {
          failed = true;
        }
      }
      // FORWARDS tier — apply the full gated set via set_resolvers on change; INDEPENDENT fail-static.
      if (forwardsKey !== this.lastForwards) {
        try {
          await this.applyForwards(cfg.forwards);
          this.lastForwards = forwardsKey;
          changed = true;
        } catch {
          failed = true;
        }
      }

      if (failed) {
        // At least one tier's apply threw: KEEP its last-applied set, lengthen the next probe. The other
        // tier (if it succeeded) already advanced its key, so it won't re-apply — no thrash.
        this.backoff = this.backoff === 0 ? this.baseMs : Math.min(this.backoff * 2, this.maxMs);
        return "inconclusive";
      }
      this.backoff = 0;
      return changed ? "applied" : "unchanged";
    } catch {
      // Poll read error: KEEP both last-applied sets, lengthen the next probe (fail-static, both tiers).
      this.backoff = this.backoff === 0 ? this.baseMs : Math.min(this.backoff * 2, this.maxMs);
      return "inconclusive";
    } finally {
      this.inFlight = false;
    }
  }

  private nextDelay(): number {
    return this.backoff === 0 ? this.baseMs : this.backoff;
  }

  private schedule(ms: number): void {
    if (this.stopped) return;
    this.timer = this.setTimer(() => {
      this.timer = null;
      void this.loop();
    }, ms);
  }

  private async loop(): Promise<void> {
    await this.checkOnce();
    if (!this.stopped) this.schedule(this.nextDelay());
  }
}
