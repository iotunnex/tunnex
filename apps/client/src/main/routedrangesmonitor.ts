import type { DeviceApi } from "./deviceconfig";
import { REVOKE_POLL_MS, REVOKE_BACKOFF_MAX_MS } from "./revocation";

// canonRanges dedups + sorts a CIDR list into a stable canonical form for change-compare. The server
// already returns its ranges canonical + sorted (S8.5 2b); we merge in the baked base and re-sort so the
// compare is order-free (the peersEqual lesson at the client tier — compare canonical, never raw).
export function canonRanges(cidrs: string[]): string[] {
  return Array.from(new Set(cidrs.map((c) => c.trim()).filter((c) => c.length > 0))).sort();
}

export type RangesOutcome = "skipped" | "applied" | "unchanged" | "inconclusive";

// RoutedRangesMonitor is the S8.5 volatile-routes poll — the RevocationMonitor's sibling (same cadence,
// self-scheduling lifecycle, error posture). While a SPLIT-tunnel session is up it fetches the org's
// declared ranges, merges baked-base ∪ ranges, and live-applies the FULL desired set via the helper ONLY
// when it changes. It is NOT constructed for a full-tunnel session (0.0.0.0/0 subsumes every range and the
// helper would no-op — the CLIENT layer skips, so no pointless privileged call is emitted; ipc.ts owns
// that gate).
//
// Discipline:
//   - fail-STATIC: a poll THROW (or an apply THROW) keeps the last-applied set — a CP blip never
//     un-routes the office LAN (no strip-to-baked). Backoff lengthens the next probe.
//   - no-CHURN: canonical compare → apply only on change. N identical polls = ZERO helper calls.
//   - full-SWEEP: the applied set is ALWAYS baked-base ∪ current-ranges, never accumulated — a removed
//     range vanishes on the next differing poll, and the baked base (pool) is in EVERY applied set.
export class RoutedRangesMonitor {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private running = false;
  private inFlight = false;
  private backoff = 0;
  // lastApplied = the canonical join of the set the helper currently holds. Seeded to the BAKED BASE:
  // tunnel_up already applied it, so an empty-ranges org yields a first poll of "unchanged" — zero client
  // work (the D5 empty-channel golden's client half).
  private lastApplied: string;

  constructor(
    private readonly orgId: string,
    private readonly base: string[], // the baked-stable AllowedIPs (split-tunnel = [pool]) — never dropped
    private readonly api: Pick<DeviceApi, "routedRanges">,
    private readonly apply: (allowedIPs: string[]) => Promise<void>, // TunnelController.setAllowedIPs
    private readonly baseMs: number = REVOKE_POLL_MS, // reuse the revocation cadence (routes change rarely)
    private readonly maxMs: number = REVOKE_BACKOFF_MAX_MS,
    private readonly setTimer: (cb: () => void, ms: number) => ReturnType<typeof setTimeout> = (cb, ms) => {
      const t = setTimeout(cb, ms);
      t.unref?.();
      return t;
    },
    private readonly clearTimer: (t: ReturnType<typeof setTimeout>) => void = (t) => clearTimeout(t),
  ) {
    this.lastApplied = canonRanges(base).join(",");
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.stopped = false;
    this.backoff = 0;
    // IMMEDIATE first poll (the mint-ruling-A condition): a NEW device gets its ranges within SECONDS of
    // connecting, not after a full 30s interval — shrinking the new-device gap so ruling A's "poll covers
    // new devices" trade is nearly free. loop() runs checkOnce NOW, then reschedules at the steady cadence.
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

  // checkOnce runs a single poll+merge+apply. Exposed for tests.
  async checkOnce(): Promise<RangesOutcome> {
    if (this.stopped || this.inFlight) return "skipped";
    this.inFlight = true;
    try {
      const ranges = await this.api.routedRanges(this.orgId);
      if (this.stopped) return "skipped"; // disconnecting — abandon the result
      const merged = canonRanges([...this.base, ...ranges]);
      const key = merged.join(",");
      if (key === this.lastApplied) {
        this.backoff = 0; // healthy poll, unchanged → steady cadence, NO helper call
        return "unchanged";
      }
      await this.apply(merged); // throws → caught below (fail-static; lastApplied NOT advanced)
      this.lastApplied = key;
      this.backoff = 0;
      return "applied";
    } catch {
      // Inconclusive (poll read error OR apply failure): KEEP the last-applied set, lengthen the next probe.
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
