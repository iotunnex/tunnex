import * as fs from "node:fs";
import * as path from "node:path";
import { app, BrowserWindow, shell, protocol, session } from "electron";
import { resolveBundlePath, looksLikeAsset, contained } from "./bundle";
import { contentTypeFor } from "./mime";
import { cspFor } from "./csp";
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

  // Navigation lock: the renderer must never leave app:// (a compromised page
  // navigating to an external origin would keep the preload bridge). External
  // links go to the SYSTEM browser; new windows are denied.
  win.webContents.on("will-navigate", (e, url) => {
    if (!url.startsWith("app://")) {
      e.preventDefault();
      if (url.startsWith("http://") || url.startsWith("https://")) void shell.openExternal(url);
    }
  });
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith("http://") || url.startsWith("https://")) void shell.openExternal(url);
    return { action: "deny" };
  });

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
  const config = new Config();

  // Serve the SPA bundle over app://. Every response carries a CSP; every path is
  // (a) lexically contained, (b) symlink-resolved and RE-checked for containment
  // (fs.readFile follows links), and (c) only extension-less paths fall back to
  // index.html — an asset 404 stays a 404 (never HTML masquerading as a script).
  protocol.handle("app", (request) => {
    const csp = cspFor(config.getServerUrl());
    const htmlHeaders = { "content-type": "text/html; charset=utf-8", "content-security-policy": csp };
    const serveIndex = () => {
      const index = resolveBundlePath(bundleDir(), "/index.html");
      if (index && fs.existsSync(index)) return new Response(fs.readFileSync(index), { headers: htmlHeaders });
      return new Response("not found", { status: 404 });
    };

    const url = new URL(request.url);
    const file = resolveBundlePath(bundleDir(), url.pathname);
    if (!file || !fs.existsSync(file)) {
      return looksLikeAsset(url.pathname) ? new Response("not found", { status: 404, headers: { "content-security-policy": csp } }) : serveIndex();
    }
    // Symlink re-check: resolve the real paths of BOTH the file and the root and
    // confirm the file is still in-bundle (fs.readFile follows links).
    let real: string;
    let realRoot: string;
    try {
      real = fs.realpathSync(file);
      realRoot = fs.realpathSync(bundleDir());
    } catch {
      return new Response("not found", { status: 404 });
    }
    if (!contained(realRoot, real)) {
      return new Response("forbidden", { status: 403 });
    }
    return new Response(fs.readFileSync(real), { headers: { "content-type": contentTypeFor(real), "content-security-policy": csp } });
  });
  initUpdater();
  createWindow(config);

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow(config);
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});
