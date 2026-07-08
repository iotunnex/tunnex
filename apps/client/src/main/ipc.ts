import { ipcMain, BrowserWindow } from "electron";
import { Config } from "./config";
import { CredentialStore } from "./credential";
import { runLogin, runLogout } from "./login";

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
    await runLogout(store);
    win.webContents.reload();
  });

  ipcMain.handle("config:getServerUrl", () => config.getServerUrl());

  ipcMain.handle("config:setServerUrl", async (_e, url: unknown) => {
    if (typeof url !== "string" || url.length === 0 || url.length > 2000) {
      throw new Error("invalid server url");
    }
    const hasCred = store.load() !== null;
    const { url: accepted, reloginRequired } = await config.setServerUrl(url, hasCred);
    // A credential must never reach a server it wasn't minted against: on a real
    // change, revoke + clear before anything can send the old token to the new URL.
    if (reloginRequired) {
      await runLogout(store);
    }
    win.webContents.reload();
    return { url: accepted, reloginRequired };
  });
}
