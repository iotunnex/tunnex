import * as fs from "node:fs";
import * as path from "node:path";
import { app, BrowserWindow, shell, protocol, session } from "electron";
import { resolveBundlePath, looksLikeAsset, contained } from "./bundle";
import { contentTypeFor } from "./mime";
import { cspFor } from "./csp";
import { Config } from "./config";
import { buildCredentialStore, buildTunnelConfigStore } from "./store";
import { attachBearer } from "./session";
import { registerIpc } from "./ipc";
import { TunnelTray } from "./tray";
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

// APP-LEVEL, not per-window. On macOS the app OUTLIVES the window (window-all-closed
// does not quit on darwin; the tray keeps it alive), so the tunnel, its monitor, the
// IPC handlers (ipcMain is app-global — registering per-window throws "second handler"
// on the second window), the tray, and the stores are all app-lifetime singletons.
// The window is a detachable VIEW: `mainWindow` is the current one (or null when
// closed); IPC/status resolve it dynamically and no-op when it's gone. The VPN keeps
// running with no window — reopening re-attaches and re-reads live status.
let mainWindow: BrowserWindow | null = null;
let allowInsecureStorage = false; // captured from the store at setup for the setup page

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
  mainWindow = win;
  win.on("closed", () => {
    if (mainWindow === win) mainWindow = null; // drop the ref so nothing sends to a destroyed webContents
  });

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
    void win.loadURL(setupPageDataUrl(allowInsecureStorage, allowInsecure));
  } else {
    void win.loadURL("app://tunnex/index.html");
  }
  return win;
}

// showWindow brings the window forward, recreating it if it was closed (macOS). Used
// by the tray so its "Show Tunnex" always works even after the window was destroyed.
function showWindow(config: Config): void {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.show();
    mainWindow.focus();
    return;
  }
  createWindow(config);
}

app.whenReady().then(() => {
  const config = new Config();
  // App-lifetime singletons — built ONCE, not per-window.
  const store = buildCredentialStore(allowInsecure);
  const tunnelStore = buildTunnelConfigStore(allowInsecure);
  allowInsecureStorage = store.available();
  // attachBearer registers a webRequest handler on the SHARED default session — must
  // run exactly once (a per-window call would stack duplicate injectors).
  attachBearer(session.defaultSession, () => config.getServerUrl(), store);

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
    // /api/* is NEVER in the bundle — if it reaches app:// the desktop transport
    // switch was inert (no server configured), so 404 rather than masking the
    // misconfig by serving index.html as a "200".
    if (url.pathname.startsWith("/api/")) {
      return new Response("not found", { status: 404, headers: { "content-security-policy": csp } });
    }
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

  // IPC handlers + tunnel controls: registered ONCE. They resolve the live window via
  // the getter (null-safe) so a closed window never breaks the tunnel, and vice versa.
  const controls = registerIpc(() => mainWindow, config, store, tunnelStore);

  // Tray: one instance for the app lifetime, subscribed to tunnel state. Its actions
  // target the singleton controls + showWindow (recreates the window if closed).
  const tray = new TunnelTray({
    onConnect: () => void controls.connect(false).catch(() => {}),
    onDisconnect: () => void controls.disconnect().catch(() => {}),
    onShow: () => showWindow(config),
    onQuit: () => app.quit(),
  });
  tray.init();
  controls.subscribe((s) => tray.update(s));
  tray.update(controls.currentState());

  createWindow(config);

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow(config);
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});
