import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";
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
import { desktop, type AppInfo, type ImportedProfile } from "../lib/desktop";
import { Logo, Tagline } from "../brand";
import { drawGraph, pushRate, rateBetween } from "./throughput";
import {
  createHyperState,
  drawHyper,
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
  const preview = useMemo(() => parsePreviewState(window.location.search), []);
  const [live, setLive] = useState<ClientState>("disconnected");
  const [fullTunnel, setFullTunnel] = useState(false);
  // ⛔ REAL COUNTERS NOW, AND THE `n/a` IS NO LONGER PERMANENT. These were hard-wired to null with
  // a comment saying they would arrive "in step 3"; step 3 came and went and they never did, so the
  // panel showed `n/a` in every field forever while the plot beside it drew invented traffic.
  //
  // rx/tx/handshake come from the helper's `wg show` through the bridge. There is no PACKET counter
  // anywhere in that chain — helper, protocol or preload — so that row was a field that could never
  // be filled, and it is gone rather than reserved.
  const [stats, setStats] = useState<{
    rate: number | null;
    peak: number;
    rx: number | null;
    tx: number | null;
    since: number | null;
    handshakeSec: number | null;
    address: string | null;
    history: number[];
  }>({
    rate: null,
    peak: 0,
    rx: null,
    tx: null,
    since: null,
    handshakeSec: null,
    address: null,
    history: [],
  });

  const state = preview ?? live;
  const view = stateView(state);
  const tray = trayAppearance(state);

  // ⛔ THE SURFACE ASKED THE TUNNEL AND NEVER ASKED THE SESSION.
  //
  // It called `tunnel.status()` alone, so a device with NO CREDENTIAL rendered "Disconnected" —
  // a healthy-looking idle state — with a Connect button. Pressing it threw `not_authenticated`
  // from main, unhandled, into a terminal log. The renderer showed nothing at all.
  //
  // Auth is read FIRST and WINS: signed-out is not a kind of disconnected, it is the reason
  // connecting cannot be attempted. `expired` maps to the design's own EXPIRED CREDS.
  const [serverUrl, setServerUrl] = useState<string | null>(null);
  const [identity, setIdentity] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [authed, setAuthed] = useState<boolean | null>(null);

  async function refreshAuth(): Promise<boolean> {
    const d = desktop();
    if (!d) return true;
    try {
      const st = await d.auth.status();
      setIdentity(st.fingerprint ?? null);
      const ok = st.loggedIn && !st.expired;
      setAuthed(ok);
      if (!st.loggedIn) setLive("signed_out");
      else if (st.expired) setLive("expired_creds");
      return ok;
    } catch {
      // ⚠ A FAILED READ IS NOT "SIGNED OUT". Claiming signed-out on an unreadable session would
      // invite a pointless re-login; the same absent-until-known rule the nav counts follow.
      setAuthed(null);
      return true;
    }
  }

  useEffect(() => {
    const d = desktop();
    if (!d || preview) return;
    void d.config
      .getServerUrl()
      .then(setServerUrl)
      .catch(() => {});
    void d.diag
      .appInfo()
      .then(setAppInfo)
      .catch(() => {});
    void d.tunnel
      .importedInfo()
      .then(setImportedProfile)
      .catch(() => {});
    void (async () => {
      const ok = await refreshAuth();
      if (ok) setLive(mapStatus(await d.tunnel.status()));
    })();
    return d.tunnel.onStatusChanged((s) => setLive(mapStatus(s)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [preview]);

  /**
   * ⛔ THE STATS POLL. `onStatusChanged` fires on TRANSITIONS; byte counters change continuously,
   * so a surface driven only by transitions shows the numbers from the moment of connection and
   * then never moves. Polling is the right instrument here precisely because nothing pushes.
   *
   * The rate is a DELTA between readings, not a field — no counter reports bytes/sec.
   */
  const prevCounter = useRef<{ bytes: number; at: number } | null>(null);
  useEffect(() => {
    const d = desktop();
    if (!d || preview) return;
    let stop = false;
    const tick = async () => {
      try {
        const st = await d.tunnel.status();
        if (stop) return;
        const up = st?.state === "up";
        if (!up) {
          // Down: drop the baseline and the clock. Keeping them would make the next connection
          // report a rate computed across the gap and a duration that includes it.
          prevCounter.current = null;
          setStats((p) => ({
            ...p,
            rate: null,
            rx: null,
            tx: null,
            since: null,
            handshakeSec: null,
            address: null,
            history: [],
          }));
          return;
        }
        const bytes = (st.rx_bytes ?? 0) + (st.tx_bytes ?? 0);
        const now = { bytes, at: Date.now() };
        const rate = rateBetween(prevCounter.current, now);
        prevCounter.current = now;
        setStats((p) => ({
          rate,
          peak: Math.max(p.peak, rate),
          rx: st.rx_bytes ?? null,
          tx: st.tx_bytes ?? null,
          since: p.since ?? Date.now(),
          handshakeSec: st.last_handshake_sec ?? null,
          address: st.address ?? null,
          history: pushRate(p.history, rate),
        }));
      } catch {
        /* a failed poll is not a state change — the last known numbers stand */
      }
    };
    void tick();
    const id = window.setInterval(() => void tick(), 1000);
    return () => {
      stop = true;
      window.clearInterval(id);
    };
  }, [preview]);

  const elapsed = stats.since
    ? Math.floor((Date.now() - stats.since) / 1000)
    : null;
  // last_handshake_sec is an ABSOLUTE unix second, not an age — trayview.ts documents the same trap.
  const handshakeAge =
    stats.handshakeSec && stats.handshakeSec > 0
      ? Math.max(0, Math.floor(Date.now() / 1000) - stats.handshakeSec)
      : null;

  /**
   * ⛔ THE VERB HAD NO HANDLER AT ALL — the button rendered and did nothing.
   *
   * Two paths, and they are genuinely different rather than one faked:
   *
   *  · IN ELECTRON the bridge exists, so this calls the real `tunnel.up` / `tunnel.down`. The
   *    renderer holds no secret and no config — main resolves the WG config and forwards it to the
   *    privileged helper; we only ever see status back.
   *  · IN A BROWSER there is no bridge and there never will be. Rather than a dead button, the
   *    surface drives its OWN state so the transitions and the hyperdrive are reviewable — and
   *    says on screen that it is doing so. A simulated transition presented as a real one would be
   *    the render-floor violation this epic keeps catching.
   */
  const simulated = desktop() === null;

  async function onAction() {
    const d = desktop();
    if (d) {
      // ⛔ EVERY BRIDGE CALL IS AWAITED INSIDE A try. Before this, a rejected `tunnel.up` became an
      // unhandled rejection in main's log and the window did not move — the user pressed a button
      // and the product said nothing. A verb that can fail must be able to SAY it failed.
      setProblem(null);
      setBusy(true);
      try {
        if (state === "signed_out" || state === "expired_creds") {
          await d.auth.login();
          await refreshAuth();
        } else if (state === "connected" || state === "kill_switch") {
          await d.tunnel.down();
        } else {
          await d.tunnel.up(fullTunnel);
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        // The one error we can turn into a STATE rather than a sentence: main throws this exact
        // string when no credential is stored, which is precisely `signed_out`.
        if (msg.includes("not_authenticated")) {
          setLive("signed_out");
          setAuthed(false);
          setProblem(null);
        } else {
          setProblem(msg);
        }
      } finally {
        setBusy(false);
      }
      return;
    }
    // Browser: drive the local state so the animation can be judged.
    if (state === "connected" || state === "kill_switch") {
      setLive("disconnected");
      return;
    }
    if (state === "expired_creds") return; // the browser flow has nothing to open here
    setLive("connecting");
    window.setTimeout(() => setLive("connected"), 2200);
  }

  /**
   * ⛔ CHANGE SERVER — THE LAST CAPABILITY THE STEP-3 FLIP STRANDED.
   *
   * `config.setServerUrl` has been on the preload allowlist since S6.2, and after the client stopped
   * loading the web dashboard NOTHING CALLED IT. Pointing the app at a different control plane meant
   * deleting `~/Library/Application Support/@tunnex/client` by hand — an app with a documented verb
   * and no way to reach it, which is the S14.12 class exactly.
   *
   * ⚠ THE SERVER CHANGE REVOKES THE CREDENTIAL, AND THE UI MUST SAY SO BEFORE IT HAPPENS. Main
   * stops the monitors, tears the tunnel down and clears the credential BEFORE persisting the new
   * URL, so there is no window where a new origin holds an old bearer. `reloginRequired` is that
   * fact coming back — it is not advice, it has already happened.
   */
  const [editingServer, setEditingServer] = useState(false);
  const [draftServer, setDraftServer] = useState("");
  const [logText, setLogText] = useState<string>("");
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [importedProfile, setImportedProfile] =
    useState<ImportedProfile | null>(null);
  const [exported, setExported] = useState<string | null>(null);

  /**
   * ⛔ THREE PANES, BECAUSE THE MAIN SCREEN WAS GROWING BY ONE SECTION PER REQUEST.
   *
   * Routing mode, then a server form, then a footer of buttons — each defensible alone, and together
   * a column you scroll to find anything in. A VPN client's home screen answers one question ("am I
   * connected, and what do I press") and everything else is somewhere you go on purpose.
   *
   * > **A SURFACE THAT ONLY EVER GAINS SECTIONS IS NOT A DESIGN, IT IS AN ACCUMULATION.** The fix is
   * > not smaller sections; it is a second place to put them.
   */
  const [pane, setPane] = useState<"home" | "settings" | "logs">("home");

  async function loadLog() {
    const d = desktop();
    if (!d) return;
    setLogText(await d.diag.readLog());
  }

  async function onExportLog() {
    const d = desktop();
    if (!d) return;
    try {
      const path = await d.diag.exportLog();
      // null is a CANCELLED dialog, not a failure — saying "exported" there would be the UI
      // claiming an action it did not perform.
      setExported(path);
    } catch (e) {
      setProblem(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    if (pane === "logs") void loadLog();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pane]);

  async function onImportConfig() {
    const d = desktop();
    if (!d) return;
    setProblem(null);
    setBusy(true);
    try {
      const p = await d.tunnel.importConfig();
      // null = the picker was cancelled. Not an error, and not an import.
      if (p) {
        setImportedProfile(p);
        setFullTunnel(p.fullTunnel);
      }
    } catch (e) {
      // parseWgConf is strict on purpose: a half-parsed profile would be handed to a ROOT helper.
      setProblem(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onForgetImported() {
    const d = desktop();
    if (!d) return;
    setBusy(true);
    try {
      await d.tunnel.forgetImported();
      setImportedProfile(null);
      await refreshAuth();
    } finally {
      setBusy(false);
    }
  }

  async function onChangeServer() {
    const d = desktop();
    if (!d) return;
    setProblem(null);
    setBusy(true);
    try {
      const res = await d.config.setServerUrl(draftServer.trim());
      setServerUrl(res.url);
      setEditingServer(false);
      // The credential was cleared server-side of this call; re-read rather than assume.
      await refreshAuth();
    } catch (e) {
      setProblem(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  /** Sign out — clears the stored credential so the next Connect must re-authenticate. */
  async function onSignOut() {
    const d = desktop();
    if (!d) return;
    setProblem(null);
    setBusy(true);
    try {
      await d.auth.logout();
      await refreshAuth();
    } catch (e) {
      setProblem(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  // ⛔ THE MESH IS BACK — I CUT IT ON TOO WIDE A READING OF ONE SENTENCE.
  //
  // "Remove that hyperdrive thing" covered three things at once: a NAME, a fabricated plot and an
  // animation. I removed all three. Only two deserved it. **The motion was never the problem — the
  // invented numbers beside it were**, and those are gone for good: the plot now draws measured
  // bytes and this canvas makes no claim about data at all.
  //
  // ⚠ An instruction that names a FEATURE by its label ("the hyperdrive thing") is not a list of
  // its parts. The parts had to be separated and dispositioned one at a time, and I collapsed them.
  const hyperRef = useRef<HTMLCanvasElement | null>(null);
  const graphRef = useRef<HTMLCanvasElement | null>(null);
  const hyperState = useRef(createHyperState());

  const mode: HyperMode =
    state === "connected"
      ? "connected"
      : state === "connecting"
        ? "connecting"
        : "idle";

  useEffect(() => {
    hyperState.current.mode = mode;
  }, [mode]);

  useEffect(() => {
    // Decorative motion: a reader who asked for stillness gets the static first frame.
    const reduced = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    let raf = 0;
    const fit = (cv: HTMLCanvasElement) => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const w = cv.clientWidth;
      const h = cv.clientHeight;
      if (!w || !h) return null;
      if (
        cv.width !== Math.round(w * dpr) ||
        cv.height !== Math.round(h * dpr)
      ) {
        cv.width = Math.round(w * dpr);
        cv.height = Math.round(h * dpr);
      }
      const ctx = cv.getContext("2d");
      if (!ctx) return null;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      return { ctx, w, h };
    };
    // The mesh animates every frame; the plot advances once per poll and is redrawn in the same
    // pass because both share the DPR-fitting helper above.
    const frame = () => {
      const st = hyperState.current;
      stepLink(st);
      const a = hyperRef.current && fit(hyperRef.current);
      if (a) drawHyper(a.ctx, a.w, a.h, st, Date.now());
      const b = graphRef.current && fit(graphRef.current);
      if (b) drawGraph(b.ctx, b.w, b.h, historyRef.current);
      if (!reduced) raf = requestAnimationFrame(frame);
    };
    frame();
    return () => cancelAnimationFrame(raf);
  }, []);

  // The loop reads the newest samples through a ref: re-creating it on every poll would restart the
  // animation once a second, which is exactly the stutter a rAF loop is meant to avoid.
  const historyRef = useRef<number[]>([]);
  useEffect(() => {
    historyRef.current = stats.history;
  }, [stats.history]);

  return (
    // ⛔ THE CARD, AND IT IS THE DESIGN'S OWN NUMBER — NOT A TASTE CALL.
    //
    // The block's client is `max-width:440px; width:100%; margin:0 auto` with an 18px radius and a
    // glass gradient. We rendered it FULL-BLEED in an 1100px window, so every row — the stats grid,
    // the verb, the split-tunnel line — stretched to twice the width it was drawn at. Nothing was
    // missing; the proportions were simply not the ones specified, which is why it read as wrong
    // rather than as broken.
    //
    // ⚠ THE OUTER SHELL STILL OWNS THE VIEWPORT. The card is centred inside it, so a resized window
    // widens the MARGINS and never the card — the one behaviour a max-width alone would not give if
    // the shell were sized to the content.
    // ⛔ ONE SURFACE — founder-directed, and it supersedes the design's card.
    //
    // The block draws the client as a 440px card `margin:0 auto` on a page, which is how it has to
    // be drawn in a WIREFRAME: the wireframe is a web page, so the card needs a page to sit on.
    // Transcribed literally into a fixed 480px window it produced a card floating inside a window
    // frame — **two chromes, one of them meaningless**, and an outer margin that exists only
    // because the design needed somewhere to put the card.
    //
    // > **A DESIGN'S CONTAINER IS NOT ALWAYS PART OF THE DESIGN.** Some of what a wireframe shows is
    // > the wireframe's own medium, and copying it faithfully reproduces the medium along with the
    // > work. The 440px width was real; the page it was centred on was not.
    //
    // The window is now the card: it owns the frame, the OS draws the corners, and the only borders
    // left are the ones separating content from content.
    <div className="flex h-dvh flex-col overflow-hidden bg-bg text-ink-body">
      {/* ── TITLE ─────────────────────────────────────────────────────────────────────────── */}
      {/* ⛔ CLEARS THE TRAFFIC LIGHTS, AND IS THE DRAG HANDLE. With `titleBarStyle: hiddenInset` the
          page paints under the window buttons, so content at the top-left would sit BEHIND them —
          the wordmark was going to end up with three coloured circles on it. `pt-8` is the inset
          macOS reserves.

          ⚠ AND THE WINDOW MUST STILL BE DRAGGABLE. A hidden title bar removes the strip people grab,
          so this header declares itself the drag region — with the interactive children opting back
          OUT, since a button inside a drag region swallows the click. */}
      <div
        className="flex items-center gap-2.5 px-5 pt-8"
        style={{ WebkitAppRegion: "drag" } as CSSProperties}
      >
        {/* ⛔ THE REAL MARK, via the shared Logo — the previous version drew a bare <img> at 22px
            and lost the wordmark entirely. Logo derives both dimensions from the asset ratios, so
            it cannot be squashed the way a hand-sized img was. */}
        {/* ⛔ THE MARK IS THE IDENTITY AND IT WAS BEING TRIMMED. It rendered at 22px inside a
            `rounded-lg` crop, so the shape lost its corners at the one size where it can least
            afford to. Bigger, uncropped, and paired with the WORDMARK rather than a mono caption —
            the brand kit draws the name; retyping it in a monospace font was a different logo. */}
        {/* ⛔ THE WORDMARK, NOT THE MARK — AND THE REASON IS IN THE ASSET, NOT IN THE CSS.
            `tunnex-logo.svg` bakes in `<rect width="577" height="551" fill="#0A0A0A">` and its
            glyph runs corner to corner, so at any size it renders as a dark plated tile with zero
            breathing room. Removing `rounded-lg` stopped US cropping it; nothing in CSS can give
            artwork padding it does not have.

            This is the same brand block the web shell uses for its home affordance (wordmark +
            tagline), so the two surfaces now show the identity the same way instead of one of them
            showing a tile. The mark returns here the day the asset ships with a margin. */}
        <span className="flex flex-col">
          <Logo size={26} wordmarkOnly />
          <Tagline className="mt-1" />
        </span>
        {/* The tray appearance is shown in-window too, so a reviewer can see what the icon WOULD
            be without needing the tray — which is the part no instrument of ours can verify. */}
        {/* ⛔ THE RAW APPEARANCE NAME IS GONE. It printed "grey" / "solid" next to the dot —
            internal vocabulary for how the TRAY ICON is drawn, shown to a user who has no reason to
            know the tray has appearances, three lines above a status word that already says
            "Connected". A debug readout that survived into the product.

            The dot stays: it is the one thing in the window that mirrors what the menu-bar icon
            looks like right now. It carries the state in its LABEL, for a screen reader and on
            hover, rather than in a word beside it. */}
        <span
          data-tray={tray}
          className="ml-auto flex items-center gap-1.5"
          title={view.label}
          aria-label={`Status: ${view.label}`}
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
        </span>
      </div>

      {/* ── PANES ───────────────────────────────────────────────────────────────────────────
          ⛔ A SECOND PLACE TO PUT THINGS. Home answers "am I connected, and what do I press";
          everything else is somewhere you go on purpose rather than something you scroll past. */}
      <nav
        className="flex gap-1 border-b border-line px-5 pb-2 pt-3"
        style={{ WebkitAppRegion: "no-drag" } as CSSProperties}
      >
        {(
          [
            ["home", "Connection"],
            ["settings", "Settings"],
            ["logs", "Logs"],
          ] as const
        ).map(([k, label]) => (
          <button
            key={k}
            type="button"
            data-pane={k}
            aria-current={pane === k ? "page" : undefined}
            onClick={() => setPane(k)}
            className={
              "rounded px-2.5 py-1 text-xs transition-colors " +
              (pane === k
                ? "bg-white/[.10] text-ink-heading"
                : "text-ink-secondary hover:text-ink-body")
            }
          >
            {label}
          </button>
        ))}
      </nav>
      <main className="flex min-h-0 flex-1 flex-col gap-4 px-5 py-5">
        {problem && (
          <p className="rounded-lg border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">
            That did not work: {problem}
          </p>
        )}

        {pane === "home" && (
          <>
            {/* ⛔ THE DEGRADATION IS SAID ON THE SCREEN THE USER IS LOOKING AT, NOT IN SETTINGS.
                An imported profile has no device identity, so RevocationMonitor cannot poll and an
                admin revoking this device will not be reflected here. A mode that is only degraded
                in the documentation looks identical to the safe one. */}
            {importedProfile && (
              <p className="rounded-lg border border-warn/40 bg-warn/5 px-3 py-2 text-[11px] text-warn">
                Imported profile — this tunnel is not tied to an account, so
                revocation and posture checks do not apply. It keeps working
                until the server drops the peer.
              </p>
            )}
            {/* ── THE MESH ────────────────────────────────────────────────────────────────────── */}
            {/* Decorative and honest about it: aria-hidden, no data behind it, no claim in the label. */}
            <div className="relative min-h-[150px] flex-1">
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
                      ? "text-accent-400"
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
                  // ⛔ THE HELPER REPORTS A HANDSHAKE, NOT A PACKET COUNT. The old row could never be
                  // filled from any source in the chain; this one is the liveness fact the whole
                  // "never green while the tunnel is dead" rule is built on.
                  [
                    "LAST HANDSHAKE",
                    handshakeAge == null ? "n/a" : `${handshakeAge}s ago`,
                  ],
                  // ⛔ THE ADDRESS WAS ALREADY ON THE WIRE AND NOTHING SHOWED IT. TunnelController
                  // attaches it to every status specifically so the client can answer "what is my IP"
                  // without a round trip, and the panel never asked. It is the single most-looked-at
                  // fact in a VPN client.
                  ["TUNNEL IP", stats.address ?? "n/a"],
                ].map(([k, v]) => (
                  <div key={k} className="flex justify-between">
                    <dt className="font-mono text-[10px] text-ink-secondary">
                      {k}
                    </dt>
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
                onClick={() => void onAction()}
                disabled={busy}
                className={
                  "w-full rounded-lg px-4 py-3 text-sm font-medium transition-colors disabled:opacity-60 " +
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

            {simulated && (
              <p className="text-[11px] text-warn">
                No desktop bridge — this is a browser preview, so Connect drives
                the surface locally instead of a real tunnel.
              </p>
            )}
          </>
        )}

        {pane === "settings" && (
          <>
            {/* ── ROUTING MODE ────────────────────────────────────────────────────────────────────
            ⛔ THE CONTROL SAID THE OPPOSITE OF WHAT IT DID, AND THE SAFE-LOOKING SETTING WAS THE
            LEAKING ONE.

            It was a checkbox LABELLED "Split tunnel" and BOUND to `fullTunnel`. So:

              unchecked -> reads as "split tunnel is off" -> user believes ALL traffic is protected
                        -> actually fullTunnel === false -> SPLIT: most traffic bypasses the tunnel

            > **A USER WHO BELIEVES THEY ARE FULLY TUNNELLED AND IS NOT HAS A WORSE PROBLEM THAN ONE
            > WHO KNOWS THEY ARE SPLIT.** The inverted label pointed the error at the dangerous side,
            > and a checkbox cannot say which state is which — the unchecked box has no words on it.

            Two named options now, each stating what it DOES to traffic. No inference from a tick. */}
            <fieldset className="rounded-lg border border-line p-3">
              <legend className="px-1 font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
                Routing
              </legend>
              {(
                [
                  [
                    "full",
                    "All traffic",
                    "Everything leaves through the tunnel, including your normal browsing.",
                  ],
                  [
                    "split",
                    "Only Tunnex routes",
                    "Just the networks your admin published. Everything else uses your normal connection.",
                  ],
                ] as const
              ).map(([key, label, why]) => (
                <label
                  key={key}
                  className="mt-1 flex cursor-pointer items-start gap-2.5 text-sm text-ink-body"
                >
                  <input
                    type="radio"
                    name="routing"
                    className="mt-1"
                    checked={key === "full" ? fullTunnel : !fullTunnel}
                    onChange={() => setFullTunnel(key === "full")}
                  />
                  <span>
                    {label}
                    <span className="block text-xs text-ink-secondary">
                      {why}
                    </span>
                  </span>
                </label>
              ))}
              {/* Changing this re-mints the device config (deviceconfig.ts) — it is not a live switch. */}
              <p className="mt-2 text-[11px] text-ink-secondary">
                Changing this while connected re-issues the device
                configuration.
              </p>
            </fieldset>

            {/* ⛔ THE FAILURE SENTENCE. `not_authenticated` became a STATE above; anything else is shown
            verbatim rather than swallowed. A raw message is worse than a written one and far better
            than silence — and it names the verb that produced it. */}
            {/* ── PROFILE ─────────────────────────────────────────────────────────────────────
                ⛔ FOUNDER-RULED AFTER I ARGUED AGAINST IT, AND THE OBJECTION IS BUILT IN RATHER
                THAN DROPPED. A `.conf` downloaded at device creation now connects. What it cannot
                do is carry a device identity, so the monitors that keep a tunnel honest have
                nothing to poll — which is stated here and on the connection screen instead of
                being left in a design note. */}
            <section className="rounded-lg border border-line p-3">
              <h2 className="font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
                Profile
              </h2>
              {importedProfile ? (
                <>
                  <p className="mt-1 font-mono text-xs text-ink-body">
                    Imported .conf
                    {importedProfile.address
                      ? ` · ${importedProfile.address}`
                      : ""}
                  </p>
                  <p className="mt-1 text-[11px] text-warn">
                    Routing comes from the file (
                    {importedProfile.fullTunnel
                      ? "all traffic"
                      : "only its routes"}
                    ), so the routing choice above does not apply. No revocation
                    or posture monitoring.
                  </p>
                  <button
                    type="button"
                    data-forgetimported
                    disabled={busy}
                    onClick={() => void onForgetImported()}
                    className="mt-2 rounded border border-line px-2 py-1 text-xs hover:text-ink-body disabled:opacity-50"
                  >
                    Remove imported profile
                  </button>
                </>
              ) : (
                <>
                  <p className="mt-1 text-[11px] text-ink-secondary">
                    Signing in mints a device for you. If you were given a
                    WireGuard <code>.conf</code> when the device was created,
                    import it instead — it connects without an account, and
                    without revocation or posture monitoring.
                  </p>
                  <button
                    type="button"
                    data-importconfig
                    disabled={busy}
                    onClick={() => void onImportConfig()}
                    className="mt-2 rounded border border-line px-2 py-1 text-xs hover:text-ink-body disabled:opacity-50"
                  >
                    Import .conf
                  </button>
                </>
              )}
            </section>

            {/* ── ABOUT ───────────────────────────────────────────────────────────────────────
                ⛔ THE VERSION IS THE ONE UPDATE FACT THAT IS REAL, and the client could not tell
                you its own — the first thing any support conversation asks for.

                ⛔ AND THERE IS NO "CHECK FOR UPDATES" BUTTON, DELIBERATELY. `AUTOUPDATE_ENABLED` is
                false and PINNED false by security.test.ts (Squirrel.Mac cannot verify an unsigned
                app, so an unsigned auto-updater is a remote-code channel with no signature check),
                and `build.publish` is null — there is no feed to query. A button here would be a
                control that cannot work, which this repo shipped twice today already. The state
                model's own rule applies: a null action means NO button, plus a sentence saying
                why. */}
            <section className="rounded-lg border border-line p-3">
              <h2 className="font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
                About
              </h2>
              <p className="mt-1 font-mono text-xs text-ink-body" data-version>
                Tunnex {appInfo ? `v${appInfo.version}` : "version n/a"}
              </p>
              {appInfo && appInfo.update.kind !== "ready" && (
                <p className="mt-2 text-[11px] text-ink-secondary">
                  <span className="text-warn">{appInfo.update.reason}</span>{" "}
                  {appInfo.update.detail}
                </p>
              )}
              {appInfo?.update.kind === "ready" && (
                <button
                  type="button"
                  data-checkupdates
                  className="mt-2 rounded border border-line px-2 py-1 text-xs hover:text-ink-body"
                >
                  Check for updates
                </button>
              )}
            </section>

            {/* ── SERVER ──────────────────────────────────────────────────────────────────── */}
            <section className="rounded-lg border border-line p-3">
              <h2 className="font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
                Server
              </h2>
              <p className="mt-1 break-all font-mono text-xs text-ink-body">
                {serverUrl ?? "n/a"}
              </p>
              {identity && (
                <p className="mt-0.5 font-mono text-[10px] text-ink-secondary">
                  device {identity.slice(0, 12)}
                </p>
              )}
              {!simulated && !editingServer && (
                <div className="mt-2 flex gap-2">
                  <button
                    type="button"
                    data-changeserver
                    disabled={busy}
                    onClick={() => {
                      setDraftServer(serverUrl ?? "");
                      setEditingServer(true);
                    }}
                    className="rounded border border-line px-2 py-1 text-xs hover:text-ink-body disabled:opacity-50"
                  >
                    Change server
                  </button>
                  {authed === true && (
                    <button
                      type="button"
                      data-signout
                      disabled={busy}
                      onClick={() => void onSignOut()}
                      className="rounded border border-line px-2 py-1 text-xs hover:text-ink-body disabled:opacity-50"
                    >
                      Sign out
                    </button>
                  )}
                </div>
              )}
            </section>
            {editingServer && !simulated && (
              <form
                className="rounded-lg border border-line p-3"
                onSubmit={(e) => {
                  e.preventDefault();
                  void onChangeServer();
                }}
              >
                <label
                  className="block text-xs text-ink-secondary"
                  htmlFor="tnx-server"
                >
                  Control-plane URL
                </label>
                <input
                  id="tnx-server"
                  type="url"
                  autoComplete="off"
                  value={draftServer}
                  onChange={(e) => setDraftServer(e.target.value)}
                  placeholder="https://vpn.example.com"
                  className="mt-1 w-full rounded border border-line bg-transparent px-2 py-1.5 font-mono text-xs text-ink-body"
                />
                {/* ⛔ SAID BEFORE THE BUTTON IS PRESSED, NOT AFTER. Changing origin revokes the stored
                credential — the user must know that is the cost, not discover it. */}
                <p className="mt-2 text-[11px] text-warn">
                  Switching servers signs you out and tears down the tunnel. A
                  credential is only ever valid for the server it was issued by.
                </p>
                <div className="mt-2 flex gap-2">
                  <button
                    type="submit"
                    disabled={busy || draftServer.trim().length === 0}
                    className="rounded border border-line px-2 py-1 text-xs hover:text-ink-body disabled:opacity-50"
                  >
                    Switch server
                  </button>
                  <button
                    type="button"
                    onClick={() => setEditingServer(false)}
                    className="rounded px-2 py-1 text-xs text-ink-secondary hover:text-ink-body"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            )}
          </>
        )}

        {pane === "logs" && (
          <section className="flex min-h-0 flex-1 flex-col">
            <div className="flex items-center gap-2">
              <h2 className="font-mono text-[10px] uppercase tracking-wider text-ink-secondary">
                Client log
              </h2>
              <button
                type="button"
                data-refreshlogs
                onClick={() => void loadLog()}
                className="ml-auto rounded border border-line px-2 py-0.5 text-[10px] hover:text-ink-body"
              >
                Refresh
              </button>
              <button
                type="button"
                data-exportlogs
                onClick={() => void onExportLog()}
                className="rounded border border-line px-2 py-0.5 text-[10px] hover:text-ink-body"
              >
                Export
              </button>
              <button
                type="button"
                data-openlogs
                onClick={() => void desktop()?.diag.openLogs()}
                className="rounded border border-line px-2 py-0.5 text-[10px] hover:text-ink-body"
              >
                Reveal
              </button>
            </div>
            {exported && (
              <p className="mt-2 text-[11px] text-ink-secondary">
                Saved to {exported}
              </p>
            )}
            {/* ⛔ NEWEST LAST, AND SCROLLED HERE RATHER THAN ON THE PAGE. The log is the one thing
                in this app that is legitimately long; giving it its own scroll box is what keeps
                the window itself from becoming scrollable. */}
            <pre
              data-logview
              className="mt-2 min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-all rounded-lg border border-line bg-surface-inset p-3 font-mono text-[10px] leading-relaxed text-ink-secondary"
            >
              {logText || "The log is empty."}
            </pre>
          </section>
        )}

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
