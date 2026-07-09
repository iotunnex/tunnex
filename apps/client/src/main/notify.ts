import { Notification } from "electron";

// TunnelEvent is the set of tunnel transitions worth a desktop notification. These
// mirror the states the renderer already reacts to (up / down / kill-switch fail /
// revoked) — a revoked device in particular must disconnect LOUDLY, not silently
// (S6.4 watch-item #1: the teardown reason has to reach this path).
export type TunnelEvent = "connected" | "disconnected" | "failed" | "revoked";

// messageFor is the pure copy map — split out so it is unit-testable without an
// Electron main process (Notification never constructs in CI). The wording matches
// the renderer's TunnelControl states so the tray/notification and the window agree.
export function messageFor(ev: TunnelEvent): { title: string; body: string } {
  switch (ev) {
    case "connected":
      return { title: "Tunnex connected", body: "Your VPN tunnel is up." };
    case "disconnected":
      return { title: "Tunnex disconnected", body: "Your VPN tunnel is down." };
    case "failed":
      return {
        title: "Tunnex tunnel failed",
        body: "The kill-switch is active — traffic is blocked until you reconnect or disconnect.",
      };
    case "revoked":
      return { title: "Tunnex device revoked", body: "This device was revoked. Reconnect to re-enroll." };
  }
}

// notifyTunnel shows a native notification for a tunnel event. A no-op where the OS
// has no notification support (headless CI, unsupported desktop).
export function notifyTunnel(ev: TunnelEvent): void {
  if (!Notification.isSupported()) return;
  new Notification(messageFor(ev)).show();
}
