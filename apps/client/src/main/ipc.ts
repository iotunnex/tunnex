import { ipcMain, BrowserWindow } from "electron";
import { Config } from "./config";
import { CredentialStore } from "./credential";
import { runLogin, runLogout } from "./login";
import { HelperClient } from "./helperclient";
import { TunnelController, helperSocketPath } from "./tunnel";

// The IPC handlers behind the preload allowlist. VERB-SPECIFIC — there is no
// generic invoke(channel,args); each channel validates its own inputs in main
// (never trust the renderer, same posture as never trusting the browser). This
// set IS the privileged surface + the audit surface.
//
// Channels: auth:{login,logout,status}, config:{getServerUrl,setServerUrl}.
// tunnel:* is reserved for S6.3 and deliberately registers NOTHING yet.
export function registerIpc(win: BrowserWindow, config: Config, store: CredentialStore): void {
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
    // Reload REGARDLESS of a logout error so the renderer re-syncs auth state
    // (never leave the user on an authed UI with no feedback).
    try {
      await runLogout(store);
    } finally {
      win.webContents.reload();
    }
  });

  // S6.3 tunnel control. The controller speaks the framed helper protocol; the
  // config provider (bearer-fetch the device WG config in MAIN, keeping the key
  // out of the renderer) is the next integration step — until it lands, tunnel:up
  // fails cleanly with device_config_unavailable, but the full transport path
  // (renderer → ipc → helper framing → privileged helper) is wired and verified.
  const tunnel = new TunnelController(
    new HelperClient(helperSocketPath()),
    async () => {
      throw new Error("device_config_unavailable: WG config acquisition is the next S6.3 step");
    },
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
