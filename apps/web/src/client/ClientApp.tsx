import { useEffect, useMemo, useState } from "react";
import {
  CLIENT_STATES,
  PREVIEW_DISCLAIMER,
  formatBytes,
  formatDuration,
  formatRate,
  parsePreviewState,
  stateView,
  trayAppearance,
  type ClientState,
} from "../lib/clientstate";
import { desktop } from "../lib/desktop";
import logoUrl from "../assets/tunnex-logo.svg";

/**
 * ClientApp — the desktop client's whole UI.
 *
 * ⛔ FOUR REGIONS, AND THE LIST IS CLOSED: status head · connection stats · the primary verb ·
 * split-tunnel. The wireframe's block specifies exactly this and no dashboard content of any kind.
 *
 * It mounts NO router and imports NO page. The only shared code is tokens (index.css), the
 * formatting helpers, and the desktop bridge type.
 */
export function ClientApp() {
  const preview = useMemo(
    () => parsePreviewState(window.location.search),
    [],
  );
  const [live, setLive] = useState<ClientState>("disconnected");
  const [fullTunnel, setFullTunnel] = useState(false);
  // Stats arrive from the bridge in step 3; in a browser they stay null and render "n/a" rather
  // than 0 — a zero nobody measured is a claim, and "n/a" is an answer.
  const [stats] = useState<{
    rate: number | null;
    peak: number;
    rx: number | null;
    tx: number | null;
    since: number | null;
    packets: number | null;
    history: number[];
  }>({ rate: null, peak: 0, rx: null, tx: null, since: null, packets: null, history: [] });

  const state = preview ?? live;
  const view = stateView(state);
  const tray = trayAppearance(state);

  // In a browser there is no bridge; the preview is the point. In Electron the bridge pushes.
  useEffect(() => {
    const d = desktop();
    if (!d || preview) return;
    void d.tunnel.status().then((s) => setLive(mapStatus(s)));
    return d.tunnel.onStatusChanged((s) => setLive(mapStatus(s)));
  }, [preview]);

  const elapsed = stats.since ? Math.floor((Date.now() - stats.since) / 1000) : null;

  return (
    <div className="flex h-dvh flex-col bg-bg text-ink-body">
      {/* ── TITLE ─────────────────────────────────────────────────────────────────────────── */}
      <div className="flex items-center gap-2.5 px-5 pt-4">
        <img src={logoUrl} alt="" aria-hidden width={22} height={21} className="rounded" />
        <span className="font-mono text-xs tracking-wide text-ink-secondary">
          tunnex · hyperdrive
        </span>
        {/* The tray appearance is shown in-window too, so a reviewer can see what the icon WOULD
            be without needing the tray — which is the part no instrument of ours can verify. */}
        <span
          data-tray={tray}
          className="ml-auto flex items-center gap-1.5 font-mono text-[10px] text-ink-secondary"
          title={`tray: ${tray}`}
        >
          <span
            className={
              "h-2 w-2 rounded-full " +
              (tray === "solid"
                ? "bg-accent-400"
                : tray === "pulsing"
                  ? "animate-pulse bg-warn"
                  : tray === "red"
                    ? "bg-danger"
                    : "bg-slate-600")
            }
          />
          {tray}
        </span>
      </div>

      <main className="flex min-h-0 flex-1 flex-col gap-4 px-5 py-5">
        {/* ── STATUS HEAD ─────────────────────────────────────────────────────────────────── */}
        <section>
          <h1
            data-state={state}
            className={
              "text-[26px] font-semibold leading-tight " +
              (view.severity === "loud"
                ? "text-danger"
                : view.severity === "ok"
                  ? "text-accent-300"
                  : view.severity === "warn"
                    ? "text-warn"
                    : "text-ink-heading")
            }
          >
            {view.label}
          </h1>
          <p className="mt-1 text-sm text-ink-secondary">{view.detail}</p>
        </section>

        {/* ── CONNECTION STATS ────────────────────────────────────────────────────────────── */}
        <section className="rounded-xl border border-line bg-surface-inset p-4">
          <div className="flex items-baseline justify-between">
            <span className="font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
              Connection stats
            </span>
            <span className="font-mono text-sm text-ink-heading">
              {formatRate(stats.rate)}
            </span>
          </div>
          <Sparkline history={stats.history} />
          <div className="mt-1 flex justify-between font-mono text-[10px] text-ink-secondary">
            <span>{formatRate(stats.peak)} peak</span>
            <span>{formatRate(stats.rate)}</span>
          </div>
          <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2">
            {[
              ["BYTES IN ↓", formatBytes(stats.rx)],
              ["BYTES OUT ↑", formatBytes(stats.tx)],
              ["DURATION", formatDuration(elapsed)],
              ["PACKET RECEIVED", stats.packets == null ? "n/a" : String(stats.packets)],
            ].map(([k, v]) => (
              <div key={k} className="flex justify-between">
                <dt className="font-mono text-[10px] text-ink-secondary">{k}</dt>
                <dd className="font-mono text-xs text-ink-body">{v}</dd>
              </div>
            ))}
          </dl>
        </section>

        {/* ── THE VERB ────────────────────────────────────────────────────────────────────── */}
        {/* ⛔ NULL MEANS NO BUTTON. Revoked, posture-blocked, pending-approval and helper-outdated
            have nothing the user can press — offering "Connect" there would be a control that
            cannot work, which is worse than none. */}
        {view.action ? (
          <button
            type="button"
            data-action
            className={
              "w-full rounded-lg px-4 py-3 text-sm font-medium transition-colors " +
              (view.severity === "loud"
                ? "bg-danger/15 text-danger hover:bg-danger/25"
                : "border border-white/15 bg-white/[.08] text-ink-heading hover:bg-white/[.14]")
            }
          >
            {view.action}
          </button>
        ) : (
          <p className="rounded-lg border border-dashed border-line px-4 py-3 text-center text-xs text-ink-secondary">
            Nothing to do here — this resolves elsewhere.
          </p>
        )}

        <label className="flex items-center gap-2.5 text-sm text-ink-body">
          <input
            type="checkbox"
            checked={fullTunnel}
            onChange={(e) => setFullTunnel(e.target.checked)}
          />
          Split tunnel
        </label>

        {preview && (
          <div className="mt-auto rounded-lg border border-warn/40 bg-warn/5 p-3">
            <p className="text-xs text-warn">{PREVIEW_DISCLAIMER}</p>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {CLIENT_STATES.map((s) => (
                <a
                  key={s}
                  href={`?state=${s}`}
                  className={
                    "rounded border px-1.5 py-0.5 font-mono text-[10px] " +
                    (s === state
                      ? "border-warn text-warn"
                      : "border-line text-ink-secondary hover:text-ink-body")
                  }
                >
                  {s}
                </a>
              ))}
            </div>
          </div>
        )}
      </main>
    </div>
  );
}

/** A bare sparkline — fixed viewBox, never `w-full` over a viewBox (S14.7's 4× lesson). */
function Sparkline({ history }: { history: number[] }) {
  const pts = history.length ? history : new Array(24).fill(0);
  const max = Math.max(1, ...pts);
  const d = pts
    .map((v, i) => `${(i / (pts.length - 1)) * 100},${28 - (v / max) * 26}`)
    .join(" ");
  return (
    <svg viewBox="0 0 100 28" preserveAspectRatio="none" className="mt-2 h-7 w-full" aria-hidden>
      <polyline points={d} fill="none" stroke="currentColor" strokeWidth="1" className="text-accent-400/70" />
    </svg>
  );
}

/** Map the bridge's status to our state union. Kept tiny and total. */
function mapStatus(s: { state?: string } | null | undefined): ClientState {
  switch (s?.state) {
    case "up":
      return "connected";
    case "connecting":
      return "connecting";
    case "revoked":
      return "revoked";
    case "pending_approval":
      return "pending_approval";
    case "migrate_failed":
      return "migrate_failed";
    case "posture_blocked":
      return "posture_blocked";
    case "failed":
      return "failed";
    default:
      return "disconnected";
  }
}
