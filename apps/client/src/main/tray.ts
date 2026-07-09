import { Tray, Menu, nativeImage, type NativeImage } from "electron";

// TrayState is the coarse tunnel state the tray reflects. The tray deliberately does
// NOT re-derive the window's handshake-liveness nuance (that lives in the renderer);
// it shows the operable state — is there a tunnel to tear down, did it fail closed,
// was the device revoked — so the menu's actions are always honest.
export type TrayState = "disconnected" | "connected" | "failed" | "revoked";

// TrayMenuModel is the pure view-model behind the tray menu, split out so the
// state→menu mapping is unit-testable without constructing an Electron Tray (which
// never happens in CI). showConnect/showDisconnect decide which actions are offered.
export interface TrayMenuModel {
  statusLabel: string;
  showConnect: boolean;
  showDisconnect: boolean;
}

export function trayMenuModel(state: TrayState): TrayMenuModel {
  switch (state) {
    case "connected":
      return { statusLabel: "Connected", showConnect: false, showDisconnect: true };
    case "failed":
      // Failed = kill-switch active. Offer BOTH: reconnect (retry) and disconnect
      // (tear down the kill-switch and go back to normal networking).
      return { statusLabel: "Tunnel failed — kill-switch active", showConnect: true, showDisconnect: true };
    case "revoked":
      // The dead config was already cleared; reconnect re-enrolls a fresh device.
      return { statusLabel: "Device revoked — reconnect to re-enroll", showConnect: true, showDisconnect: false };
    case "disconnected":
      return { statusLabel: "Not connected", showConnect: true, showDisconnect: false };
  }
}

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
