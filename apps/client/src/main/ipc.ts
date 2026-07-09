import { ipcMain, BrowserWindow } from "electron";
import { Config } from "./config";
import { CredentialStore } from "./credential";
import { runLogin, runLogout } from "./login";
import { TunnelController, helperSocketPath } from "./tunnel";
import { TunnelConfigStore } from "./tunnelstore";
import { HttpDeviceApi } from "./httpdeviceapi";
import { resolveTunnelConfig, clearTunnelConfigForOrigin } from "./deviceconfig";
import { RevocationMonitor } from "./revocation";
import { notifyTunnel } from "./notify";
import { trayStateFor, type TrayState } from "./tray";
import type { TunnelStatus } from "./helperclient";

// Desktop default: split-tunnel (org network only), matching the CLI default. The UI
// may pass a full-tunnel INTENT at connect (S6.4 split-tunnel toggle); the helper
// enforces both-family completeness when it IS full. Switching split↔full re-mints the
// device (resolveTunnelConfig), so the toggle actually takes effect.
const DEFAULT_FULL_TUNNEL = false;

// ClientTunnelStatus is what main forwards: the helper's TunnelStatus plus the
// client-synthesized "revoked" state (the helper never emits it).
type ClientTunnelStatus = TunnelStatus | { state: "revoked" };

// TunnelControls is what registerIpc returns so the tray (built in index.ts) can drive
// the SAME connect/disconnect path the renderer uses — no duplicated tunnel logic, one
// source of truth for monitor + notification + state emission.
export interface TunnelControls {
  connect(fullTunnel: boolean): Promise<TunnelStatus>;
  disconnect(): Promise<void>;
  currentState(): TrayState;
  subscribe(cb: (s: TrayState) => void): () => void;
}

// The IPC handlers behind the preload allowlist. VERB-SPECIFIC — there is no generic
// invoke(channel,args); each channel validates its own inputs in main (never trust the
// renderer). Registered ONCE at app ready (ipcMain is app-global). `getWindow` resolves
// the CURRENT window (or null when closed) so the tunnel + monitor outlive any window.
export function registerIpc(
  getWindow: () => BrowserWindow | null,
  config: Config,
  store: CredentialStore,
  tunnelStore: TunnelConfigStore,
): TunnelControls {
  // The bearer is bound to the origin it was minted against (store.load().server);
  // build the device API + resolve config against exactly that origin so a config
  // is never fetched/used cross-origin.
  const deviceApiFor = (origin: string) => new HttpDeviceApi(origin, () => store.load()?.token ?? null);

  // --- tunnel state fan-out (renderer push channel + tray subscribers) -----------
  const subscribers = new Set<(s: TrayState) => void>();
  let trayState: TrayState = "disconnected";
  // lastSynth holds a CLIENT-synthesized state (currently only "revoked") so it
  // survives a renderer remount/reload: the helper can't report "revoked", so
  // tunnel:status returns this until the next connect/disconnect clears it.
  let lastSynth: { state: "revoked" } | null = null;

  const emitTray = (s: TrayState): void => {
    trayState = s;
    for (const cb of subscribers) cb(s);
  };
  // pushRenderer sends to the live window's onStatusChanged channel. GUARDED: a closed/
  // destroyed window is a no-op (never throws) — so callers' tray + notification side
  // effects always run, they don't ride behind a throwing send.
  const pushRenderer = (s: ClientTunnelStatus): void => {
    const w = getWindow();
    if (!w || w.isDestroyed()) return;
    try {
      w.webContents.send("tunnel:status-changed", s);
    } catch {
      /* webContents torn down mid-send — the tray/notification path still runs */
    }
  };
  // emit forwards a status to BOTH the renderer and the tray. The tray reflects the
  // handshake-liveness nuance (up-but-stale → "connecting") so it never disagrees with
  // the window (trayStateFor mirrors TunnelControl's derivation).
  const emit = (s: ClientTunnelStatus): void => {
    pushRenderer(s);
    emitTray(trayStateFor(s));
  };

  // --- revocation monitor (proactive, client-side; S6.4) -------------------------
  let monitor: RevocationMonitor | null = null;
  const stopMonitor = (): void => {
    monitor?.stop();
    monitor = null;
  };
  // onRevoked is the definitive-gone teardown: tear the dead tunnel down, clear the
  // dead config (+ best-effort revoke), then surface the distinct revoked state LOUDLY
  // (renderer banner + tray + notification). All best-effort; the local state is
  // authoritative. Runs at most once per monitor (RevocationMonitor fires once).
  const onRevoked = async (origin: string): Promise<void> => {
    stopMonitor();
    await tunnel.down().catch(() => {});
    await clearTunnelConfigForOrigin(origin, deviceApiFor(origin), tunnelStore).catch(() => {});
    lastSynth = { state: "revoked" }; // survive a renderer remount until next connect/disconnect
    emit({ state: "revoked" });
    notifyTunnel("revoked");
  };

  // --- the tunnel controller -----------------------------------------------------
  let requestedFullTunnel = DEFAULT_FULL_TUNNEL; // set by connect() before up()
  const tunnel = new TunnelController(
    helperSocketPath(),
    async () => {
      const cred = store.load();
      if (!cred) throw new Error("not_authenticated");
      return resolveTunnelConfig(cred.server, requestedFullTunnel, deviceApiFor(cred.server), tunnelStore);
    },
    (status) => {
      // Live heartbeat status + the LOUD fail-closed signal (onLost → state "failed").
      emit(status);
      if (status.state === "failed") {
        // The helper died / fail-closed: the monitor is moot (no tunnel to watch) and
        // the user must be told loudly. onLost fires once, so this fires once.
        stopMonitor();
        notifyTunnel("failed");
      }
    },
  );

  const connect = async (fullTunnel: boolean): Promise<TunnelStatus> => {
    // Stop any prior monitor FIRST, unconditionally — even if we can't resolve a new
    // deviceId below, the old monitor must not linger and later tear down THIS tunnel.
    stopMonitor();
    lastSynth = null; // a fresh connect clears any stale revoked state
    requestedFullTunnel = fullTunnel;
    const status = await tunnel.up(); // resolves + persists the device, arms the helper
    // Start the proactive revocation monitor for the device we just brought up.
    const cred = store.load();
    const deviceId = cred ? tunnelStore.get(cred.server)?.deviceId : undefined;
    if (cred && deviceId) {
      monitor = new RevocationMonitor(deviceId, deviceApiFor(cred.server), () => onRevoked(cred.server));
      monitor.start();
    }
    emit(status);
    notifyTunnel("connected");
    return status;
  };

  const disconnect = async (): Promise<void> => {
    stopMonitor();
    lastSynth = null;
    await tunnel.down();
    emit({ state: "down" });
    notifyTunnel("disconnected");
  };

  // --- auth --------------------------------------------------------------------
  ipcMain.handle("auth:status", () => {
    const cred = store.load();
    if (!cred) return { loggedIn: false, secureStorage: store.available() };
    const expired = CredentialStore.isExpired(cred, new Date());
    return { loggedIn: !expired, expired, fingerprint: cred.fingerprint, expiresAt: cred.expiresAt, secureStorage: store.available() };
  });

  ipcMain.handle("auth:login", async () => {
    const server = config.requireServerUrl(); // throws if unset
    const r = await runLogin(server, store);
    getWindow()?.webContents.reload(); // the injected bearer now authenticates the SPA
    return r;
  });

  ipcMain.handle("auth:logout", async () => {
    // Full-sweep on logout (BEFORE the token is cleared, while it can still revoke):
    // stop the monitor + tunnel gracefully, then clear the stored config + best-effort
    // revoke the device for this origin (the local config is discarded like the bearer,
    // so the server peer must not be orphaned). All best-effort — logout must proceed.
    const cred = store.load();
    stopMonitor();
    lastSynth = null;
    await tunnel.down().catch(() => {});
    emitTray("disconnected");
    if (cred) {
      await clearTunnelConfigForOrigin(cred.server, deviceApiFor(cred.server), tunnelStore).catch(() => {});
    }
    // Reload REGARDLESS of a logout error so the renderer re-syncs auth state.
    try {
      await runLogout(store);
    } finally {
      getWindow()?.webContents.reload();
    }
  });

  // --- tunnel (S6.3 control + S6.4 UX) -----------------------------------------
  ipcMain.handle("tunnel:up", (_e, fullTunnel: unknown) => connect(fullTunnel === true));
  ipcMain.handle("tunnel:down", () => disconnect());
  ipcMain.handle("tunnel:status", async () => {
    // A synthesized "revoked" state must survive a renderer remount — the helper only
    // knows up/down/failed, so return the latched synth state until the next connect.
    if (lastSynth) return lastSynth;
    return tunnel.status();
  });

  // --- config ------------------------------------------------------------------
  ipcMain.handle("config:getServerUrl", () => config.getServerUrl());

  ipcMain.handle("config:setServerUrl", async (_e, url: unknown) => {
    if (typeof url !== "string" || url.length === 0 || url.length > 2000) {
      throw new Error("invalid server url");
    }
    const hasCred = store.load() !== null;
    const { url: accepted, reloginRequired, wasUnset } = await config.validateServerUrl(url, hasCred);
    // A credential must never reach a server it wasn't minted against: on a real
    // change, revoke + clear the old credential BEFORE the new URL is persisted,
    // so there is no window where (origin=new, credential=old) can attach.
    if (reloginRequired) {
      // Stop the monitor + tunnel (they belong to the OLD origin). Per the signed-off
      // amendment we do NOT auto-revoke the old-origin device/config — it stays
      // origin-keyed in the store for the UI to surface (remove-or-switch-back).
      stopMonitor();
      lastSynth = null;
      await tunnel.down().catch(() => {});
      emitTray("disconnected");
      await runLogout(store);
    }
    config.commitServerUrl(accepted);
    // First run (unset → set) must LOAD the SPA — reload() would re-load the
    // current (setup data:) URL and cannot change origin. Otherwise a plain
    // reload picks up the new auth/config state.
    const w = getWindow();
    if (wasUnset) {
      void w?.loadURL("app://tunnex/index.html");
    } else {
      w?.webContents.reload();
    }
    return { url: accepted, reloginRequired };
  });

  return {
    connect,
    disconnect,
    currentState: () => trayState,
    subscribe: (cb) => {
      subscribers.add(cb);
      return () => subscribers.delete(cb);
    },
  };
}
