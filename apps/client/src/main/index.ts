import * as fs from "node:fs";
import * as path from "node:path";
import { app, BrowserWindow, protocol, session } from "electron";
import { resolveBundlePath } from "./bundle";
import { contentTypeFor } from "./mime";
import { Config } from "./config";
import { buildCredentialStore } from "./store";
import { attachBearer } from "./session";
import { registerIpc } from "./ipc";
import { initUpdater } from "./updater";
import { setupPageDataUrl } from "./setup";

// The SPA bundle (apps/web build). Overridable for dev; falls back to the
// packaged resources dir.
function bundleDir(): string {
  return process.env.TUNNEX_BUNDLE_DIR ?? path.join(process.resourcesPath ?? "", "web");
}

// app:// is registered standard + secure so the SPA gets a normal, secure
// origin (fetch/history/etc. behave; not the file:// footgun).
protocol.registerSchemesAsPrivileged([{ scheme: "app", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);

const allowInsecure = process.argv.includes("--allow-insecure-credential-storage");

function createWindow(config: Config): BrowserWindow {
  const win = new BrowserWindow({
    width: 1100,
    height: 760,
    webPreferences: {
      preload: path.join(__dirname, "../preload/index.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  const store = buildCredentialStore(allowInsecure);
  attachBearer(session.defaultSession, () => config.getServerUrl(), store);
  registerIpc(win, config, store);

  // First run (no server configured) → the shell's setup screen; otherwise the
  // SPA served over app://.
  if (!config.getServerUrl()) {
    void win.loadURL(setupPageDataUrl(store.available(), allowInsecure));
  } else {
    void win.loadURL("app://tunnex/index.html");
  }
  return win;
}

app.whenReady().then(() => {
  // Serve the SPA bundle over app://, rejecting any path that escapes the bundle.
  protocol.handle("app", (request) => {
    const url = new URL(request.url);
    const file = resolveBundlePath(bundleDir(), url.pathname);
    if (!file || !fs.existsSync(file)) {
      // SPA client-side routing: unknown non-asset paths fall back to index.html.
      const index = resolveBundlePath(bundleDir(), "/index.html");
      if (index && fs.existsSync(index)) {
        return new Response(fs.readFileSync(index), { headers: { "content-type": "text/html; charset=utf-8" } });
      }
      return new Response("not found", { status: 404 });
    }
    return new Response(fs.readFileSync(file), { headers: { "content-type": contentTypeFor(file) } });
  });

  const config = new Config();
  initUpdater();
  createWindow(config);

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow(config);
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});
