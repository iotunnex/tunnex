import { Tray, Menu, nativeImage, type NativeImage } from "electron";
import { trayMenuModel, type TrayState } from "./trayview";

// Re-export the pure view-models so existing imports (`from "./tray"`) keep working;
// the electron-free definitions live in trayview.ts so they stay testable in CI.
export { trayMenuModel, trayStateFor } from "./trayview";
export type { TrayState, TrayMenuModel } from "./trayview";

// A 16×16 template PNG (a filled dot). Template images are auto-masked to match the
// menu-bar theme on macOS; on Windows/Linux it renders as-is. Embedded as a data URI
// so the tray needs no packaged asset (real branded icons arrive at S6.5a packaging).
const ICON_PNG_BASE64 =
  "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAaElEQVR4nGNgoAGQY2Bg6GRgYLjMwMDwC4ovQ8XkCGlOgWr4jwP/gqrBqRmXRnSMYYgcAZuxuQTFO50kaIbhTmQDLpNhwGVkA0hxPrI3qGcAxV6gOBApjkaKExKyIWQnZWTvkJ2ZSAYAJ9qaFTVyRX8AAAAASUVORK5CYII=";

function trayIcon(): NativeImage {
  const img = nativeImage.createFromDataURL(`data:image/png;base64,${ICON_PNG_BASE64}`);
  img.setTemplateImage(true); // macOS: adapt to light/dark menu bar
  return img;
}

// TunnelTray is the menu-bar / system-tray surface. Main-process only; it reuses the
// existing tunnel-control callbacks (no new privileged surface) and is refreshed with
// update(state) from the same status stream the renderer sees, so the two never drift.
export class TunnelTray {
  private tray: Tray | null = null;
  private state: TrayState = "disconnected";

  constructor(
    private readonly actions: {
      onConnect: () => void;
      onDisconnect: () => void;
      onShow: () => void;
      onQuit: () => void;
    },
  ) {}

  // init constructs the OS tray. Separated from the constructor so tests can build
  // the model (trayMenuModel) without an Electron app being ready.
  init(): void {
    if (this.tray) return;
    this.tray = new Tray(trayIcon());
    this.tray.setToolTip("Tunnex");
    // Clicking the icon shows the window (in addition to the right-click menu).
    this.tray.on("click", () => this.actions.onShow());
    this.render();
  }

  update(state: TrayState): void {
    this.state = state;
    if (this.tray) this.render();
  }

  destroy(): void {
    this.tray?.destroy();
    this.tray = null;
  }

  private render(): void {
    if (!this.tray) return;
    const m = trayMenuModel(this.state);
    const template: Electron.MenuItemConstructorOptions[] = [
      { label: `Tunnex — ${m.statusLabel}`, enabled: false },
      { type: "separator" },
    ];
    if (m.showConnect) template.push({ label: m.showDisconnect ? "Reconnect" : "Connect", click: () => this.actions.onConnect() });
    if (m.showDisconnect) template.push({ label: "Disconnect", click: () => this.actions.onDisconnect() });
    template.push({ type: "separator" }, { label: "Show Tunnex", click: () => this.actions.onShow() }, { label: "Quit", click: () => this.actions.onQuit() });
    this.tray.setContextMenu(Menu.buildFromTemplate(template));
    this.tray.setToolTip(`Tunnex — ${m.statusLabel}`);
  }
}
