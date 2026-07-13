import type { DeviceApi } from "./deviceconfig";

// REVOKE_POLL_MS is the steady-state cadence: while a tunnel is UP the client asks
// the tenant "does THIS device still exist+active?" every 30s. That is frequent
// enough that a revoked device disconnects within a rekey window, and rare enough
// that it is not a battery/wakeup cost. REVOKE_BACKOFF_MAX_MS caps the exponential
// back-off applied after inconclusive (throwing) probes so a flapping server is
// probed ever more slowly, not hammered.
export const REVOKE_POLL_MS = 30_000;
export const REVOKE_BACKOFF_MAX_MS = 5 * 60_000;

// ProbeOutcome is what a single check decided — surfaced for the unit tests (the
// scheduler around checkOnce is thin; the DECISION is what carries the safety
// properties, so that is what the tests drive directly, like the S6.3 supervisor).
export type ProbeOutcome = "torn-down" | "kept" | "inconclusive" | "skipped";

// RevocationMonitor is the CLIENT-side proactive revocation detector (S6.4). It is
// the counterpart to the helper's kernel-side dead-man: while the tunnel is up it
// polls the tenant and, on a DEFINITIVE "device gone", tears the tunnel down + clears
// the dead config + notifies loudly — instead of the user staring at a stuck
// "Connecting…" until the helper's 90s dead-man eventually fires.
//
// Poll discipline (S6.4 watch-item #1 — the poll must not become its own bug):
//   * runs ONLY between start() and stop() — i.e. only while a tunnel is up. A
//     disconnected client polls nothing.
//   * SELF-SCHEDULING (recursive setTimeout, not setInterval): the next probe is
//     armed only after the current one resolves, so probes can NEVER overlap and a
//     wake-from-sleep can NEVER release a backlog of catch-up fires (the #9 concern).
//   * a throw from deviceExists is INCONCLUSIVE → KEEP the tunnel (never disconnect a
//     working tunnel on a transient blip) and BACK OFF the next probe exponentially.
//   * a definitive gone fires onRevoked EXACTLY ONCE, then the monitor stops itself
//     (no re-fire storm while the teardown is in flight).
export class RevocationMonitor {
  private timer: ReturnType<typeof setTimeout> | null = null;
  // stopped=false means "not explicitly stopped" — checkOnce is callable on its own
  // (the tests drive it directly). The scheduling loop only runs between start() and
  // stop(); stop() sets this true so an in-flight probe abandons its result.
  private stopped = false;
  // running guards start() idempotency by INTENT, not by timer presence: during an
  // in-flight probe this.timer is transiently null (the loop nulls it before awaiting),
  // so a timer-only guard could arm a second concurrent loop. running stays true for
  // the whole start→stop lifetime.
  private running = false;
  private inFlight = false;
  private fired = false;
  private backoff = 0; // 0 = steady state (use baseMs); >0 = current backed-off delay

  constructor(
    private readonly deviceId: string,
    private readonly orgId: string, // the device's own org — queried directly (S7.3 #4)
    private readonly api: Pick<DeviceApi, "deviceExists">,
    private readonly onRevoked: () => void | Promise<void>,
    private readonly baseMs: number = REVOKE_POLL_MS,
    private readonly maxMs: number = REVOKE_BACKOFF_MAX_MS,
    // Injectable timer for tests; defaults to the real ones. unref so a pending
    // probe never keeps the app alive on quit.
    private readonly setTimer: (cb: () => void, ms: number) => ReturnType<typeof setTimeout> = (cb, ms) => {
      const t = setTimeout(cb, ms);
      t.unref?.();
      return t;
    },
    private readonly clearTimer: (t: ReturnType<typeof setTimeout>) => void = (t) => clearTimeout(t),
  ) {}

  // start begins polling (idempotent). A fresh start resets fire state so the same
  // monitor instance can be reused across reconnects.
  start(): void {
    if (this.running) return; // already running — idempotent (even mid-probe, when timer is null)
    this.running = true;
    this.stopped = false;
    this.fired = false;
    this.backoff = 0;
    this.schedule(this.baseMs);
  }

  // stop halts polling and abandons any in-flight probe's result (a probe that
  // resolves after stop() must NOT fire teardown — the tunnel is already going down).
  stop(): void {
    this.stopped = true;
    this.running = false;
    if (this.timer) {
      this.clearTimer(this.timer);
      this.timer = null;
    }
  }

  // checkOnce runs a single probe and applies the decision. Exposed for the tests.
  async checkOnce(): Promise<ProbeOutcome> {
    if (this.stopped || this.fired || this.inFlight) return "skipped";
    this.inFlight = true;
    try {
      const exists = await this.api.deviceExists(this.deviceId, this.orgId);
      // A stop() during the await means the user is already disconnecting — abandon
      // the result rather than tearing down (or resetting backoff) behind their back.
      if (this.stopped) return "skipped";
      if (exists) {
        this.backoff = 0; // a healthy probe returns to steady cadence
        return "kept";
      }
      this.fired = true; // fire-once: block any concurrent/next probe from re-firing
      await this.onRevoked();
      return "torn-down";
    } catch {
      // Inconclusive: deviceExists throws on ANY read error (its fail-safe). KEEP the
      // tunnel and lengthen the next probe so a flapping server is not hammered.
      this.backoff = this.backoff === 0 ? this.baseMs : Math.min(this.backoff * 2, this.maxMs);
      return "inconclusive";
    } finally {
      this.inFlight = false;
    }
  }

  // nextDelay is the delay before the next probe: steady cadence normally, the
  // backed-off delay after a throw.
  private nextDelay(): number {
    return this.backoff === 0 ? this.baseMs : this.backoff;
  }

  private schedule(ms: number): void {
    if (this.stopped || this.fired) return;
    this.timer = this.setTimer(() => {
      this.timer = null;
      void this.loop();
    }, ms);
  }

  private async loop(): Promise<void> {
    await this.checkOnce();
    // Reschedule only if still running and not fired — the reschedule happens AFTER
    // checkOnce resolves, which is what guarantees no overlap and no wake-storm.
    if (!this.stopped && !this.fired) this.schedule(this.nextDelay());
  }
}
