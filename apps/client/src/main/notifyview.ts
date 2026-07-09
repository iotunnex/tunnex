// Pure notification copy — NO electron import, so it is unit-testable in CI (where
// ELECTRON_SKIP_BINARY_DOWNLOAD makes require("electron") throw). notify.ts consumes
// this and adds the Electron Notification wiring.

// TunnelEvent is the set of tunnel transitions worth a desktop notification. These
// mirror the states the renderer already reacts to (up / down / kill-switch fail /
// revoked) — a revoked device in particular must disconnect LOUDLY, not silently.
export type TunnelEvent = "connected" | "disconnected" | "failed" | "revoked";

// messageFor is the pure copy map. The wording matches the renderer's TunnelControl
// states so the tray/notification and the window agree.
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
