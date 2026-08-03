import { useEffect, useMemo, useRef, useState } from "react";
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
import { Logo } from "../brand";
import {
  createHyperState,
  drawGraph,
  drawHyper,
  pushSample,
  stepLink,
  type HyperMode,
} from "./hyperdrive";

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

  // ⛔ THE HYPERDRIVE. Two canvases, both transcribed from the handoff's own draw loop — the window
  // is named after the first of them. The previous build had neither, because I read a TEXT
  // extraction of the block and a text extraction has no canvas in it.
  const hyperRef = useRef<HTMLCanvasElement | null>(null);
  const graphRef = useRef<HTMLCanvasElement | null>(null);
  const hyperState = useRef(createHyperState());

  const mode: HyperMode =
    state === "connected" ? "connected" : state === "connecting" ? "connecting" : "idle";

  useEffect(() => {
    hyperState.current.mode = mode;
  }, [mode]);

  useEffect(() => {
    // Decorative motion: a reader who asked for stillness gets the static first frame.
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    let raf = 0;
    const fit = (cv: HTMLCanvasElement) => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const w = cv.clientWidth;
      const h = cv.clientHeight;
      if (!w || !h) return null;
      if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) {
        cv.width = Math.round(w * dpr);
        cv.height = Math.round(h * dpr);
      }
      const ctx = cv.getContext("2d");
      if (!ctx) return null;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      return { ctx, w, h };
    };
    const frame = () => {
      const st = hyperState.current;
      stepLink(st);
      const now = Date.now();
      if (now - st._last > 70) {
        st._last = now;
        pushSample(st, Math.random);
      }
      const a = hyperRef.current && fit(hyperRef.current);
      if (a) drawHyper(a.ctx, a.w, a.h, st, now);
      const b = graphRef.current && fit(graphRef.current);
      if (b) drawGraph(b.ctx, b.w, b.h, st.graph);
      if (!reduced) raf = requestAnimationFrame(frame);
    };
    frame();
    return () => cancelAnimationFrame(raf);
  }, []);

  return (
    <div className="flex h-dvh flex-col bg-bg text-ink-body">
      {/* ── TITLE ─────────────────────────────────────────────────────────────────────────── */}
      <div className="flex items-center gap-2.5 px-5 pt-4">
        {/* ⛔ THE REAL MARK, via the shared Logo — the previous version drew a bare <img> at 22px
            and lost the wordmark entirely. Logo derives both dimensions from the asset ratios, so
            it cannot be squashed the way a hand-sized img was. */}
        <Logo size={22} markOnly />
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
        {/* ── HYPERDRIVE ──────────────────────────────────────────────────────────────────── */}
        <div className="relative min-h-[180px] flex-1">
          <canvas
            ref={hyperRef}
            id="tnxHyper"
            aria-hidden
            className="absolute inset-0 block h-full w-full"
          />
        </div>

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
          {/* The designer's plot: a filled area under a 1.6px line over a fixed 64-sample window,
              so it SCROLLS rather than rescaling. */}
          <canvas
            ref={graphRef}
            id="tnxGraph"
            aria-hidden
            className="mt-2 block h-12 w-full"
          />
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
