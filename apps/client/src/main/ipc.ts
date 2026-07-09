import { ipcMain, BrowserWindow } from "electron";
import { Config } from "./config";
import { CredentialStore } from "./credential";
import { runLogin, runLogout } from "./login";
import { TunnelController, helperSocketPath } from "./tunnel";
import { TunnelConfigStore } from "./tunnelstore";
import { HttpDeviceApi } from "./httpdeviceapi";
import { resolveTunnelConfig, clearTunnelConfigForOrigin } from "./deviceconfig";

// Desktop default: split-tunnel (org network only), matching the CLI default. A
// full-tunnel toggle is a later UX affordance; the helper enforces both-family
// completeness when it IS full.
const DEFAULT_FULL_TUNNEL = false;

// The IPC handlers behind the preload allowlist. VERB-SPECIFIC — there is no
// generic invoke(channel,args); each channel validates its own inputs in main
// (never trust the renderer, same posture as never trusting the browser). This
// set IS the privileged surface + the audit surface.
//
// Channels: auth:{login,logout,status}, config:{getServerUrl,setServerUrl}.
// tunnel:* is reserved for S6.3 and deliberately registers NOTHING yet.
export function registerIpc(win: BrowserWindow, config: Config, store: CredentialStore, tunnelStore: TunnelConfigStore): void {
  // The bearer is bound to the origin it was minted against (store.load().server);
  // build the device API + resolve config against exactly that origin so a config
  // is never fetched/used cross-origin.
  const deviceApiFor = (origin: string) => new HttpDeviceApi(origin, () => store.load()?.token ?? null);
  ipcMain.handle("auth:status", () => {
    const cred = store.load();
    if (!cred) return { loggedIn: false, secureStorage: store.available() };
    const expired = CredentialStore.isExpired(cred, new Date());
    return { loggedIn: !expired, expired, fingerprint: cred.fingerprint, expiresAt: cred.expiresAt, secureStorage: store.available() };
  });

  ipcMain.handle("auth:login", async () => {
    const server = config.requireServerUrl(); // throws if unset
    const r = await runLogin(server, store);
    win.webContents.reload(); // the injected bearer now authenticates the SPA
    return r;
  });

  ipcMain.handle("auth:logout", async () => {
    // Full-sweep on logout (BEFORE the token is cleared, while it can still revoke):
    // stop the tunnel gracefully, then clear the stored config + best-effort revoke
    // the device for this origin (the local config is discarded like the bearer, so
    // the server peer must not be orphaned). All best-effort — logout must proceed.
    const cred = store.load();
    await tunnel.down().catch(() => {});
    if (cred) {
      await clearTunnelConfigForOrigin(cred.server, deviceApiFor(cred.server), tunnelStore).catch(() => {});
    }
    // Reload REGARDLESS of a logout error so the renderer re-syncs auth state.
    try {
      await runLogout(store);
    } finally {
      win.webContents.reload();
    }
  });

  // S6.3 tunnel control. The controller speaks the framed helper protocol; the
  // ConfigProvider runs in MAIN — GET-OR-CREATE the device config, origin-keyed,
  // so the WG private key (like the bearer) never enters the renderer.
  const tunnel = new TunnelController(
    helperSocketPath(),
    async () => {
      const cred = store.load();
      if (!cred) throw new Error("not_authenticated");
      return resolveTunnelConfig(cred.server, DEFAULT_FULL_TUNNEL, deviceApiFor(cred.server), tunnelStore);
    },
    (status) => win.webContents.send("tunnel:status-changed", status),
  );
  ipcMain.handle("tunnel:up", () => tunnel.up());
  ipcMain.handle("tunnel:down", () => tunnel.down());
  ipcMain.handle("tunnel:status", () => tunnel.status());

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
      // Stop the tunnel (it belongs to the OLD origin). Per the signed-off
      // amendment we do NOT auto-revoke the old-origin device/config — it stays
      // origin-keyed in the store for the UI to surface (remove-or-switch-back).
      await tunnel.down().catch(() => {});
      await runLogout(store);
    }
    config.commitServerUrl(accepted);
    // First run (unset → set) must LOAD the SPA — reload() would re-load the
    // current (setup data:) URL and cannot change origin. Otherwise a plain
    // reload picks up the new auth/config state.
    if (wasUnset) {
      void win.loadURL("app://tunnex/index.html");
    } else {
      win.webContents.reload();
    }
    return { url: accepted, reloginRequired };
  });
}
