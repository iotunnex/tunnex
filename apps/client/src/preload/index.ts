import { contextBridge, ipcRenderer } from "electron";

// The ONLY privileged surface exposed to the renderer (contextIsolation on,
// nodeIntegration off, sandbox on). VERB-SPECIFIC + promise-based — NO generic
// invoke(channel, args) passthrough, which would make the allowlist decorative.
// The raw bearer token is NEVER exposed here (no getToken): main attaches it on
// requests via the session injector. tunnel.* is reserved for S6.3.
const api = {
  auth: {
    login: (): Promise<{ fingerprint: string; expiresAt: string }> => ipcRenderer.invoke("auth:login"),
    logout: (): Promise<void> => ipcRenderer.invoke("auth:logout"),
    status: (): Promise<{ loggedIn: boolean; expired?: boolean; fingerprint?: string; expiresAt?: string; secureStorage: boolean }> =>
      ipcRenderer.invoke("auth:status"),
  },
  config: {
    getServerUrl: (): Promise<string> => ipcRenderer.invoke("config:getServerUrl"),
    setServerUrl: (url: string): Promise<{ url: string; reloginRequired: boolean }> => ipcRenderer.invoke("config:setServerUrl", url),
  },
  // S6.3 tunnel control. Verb-specific like the rest — up/down/status only. The
  // renderer holds NO tunnel secret: main resolves the WG config (bearer-fetched)
  // and forwards it to the privileged helper; the renderer only sees status.
  tunnel: {
    // fullTunnel is the split-tunnel toggle INTENT (S6.4); it only takes effect when
    // a device is minted (get-or-create) — an existing config is reused as-is.
    up: (fullTunnel = false): Promise<TunnelStatus> => ipcRenderer.invoke("tunnel:up", fullTunnel),
    down: (): Promise<void> => ipcRenderer.invoke("tunnel:down"),
    status: (): Promise<TunnelStatus> => ipcRenderer.invoke("tunnel:status"),
    // Push channel for live status + the LOUD fail-closed signal (main forwards
    // the helper heartbeat / onLost). Returns an unsubscribe fn. Carries no secret.
    onStatusChanged: (cb: (s: TunnelStatus) => void): (() => void) => {
      const listener = (_e: unknown, s: TunnelStatus) => cb(s);
      ipcRenderer.on("tunnel:status-changed", listener);
      return () => ipcRenderer.removeListener("tunnel:status-changed", listener);
    },
  },
};

// TunnelStatus mirrors apps/helper (no secrets — never carries key material).
// "revoked" is CLIENT-synthesized (the helper never emits it): main sets it when the
// proactive revocation monitor detects this device was revoked/deleted server-side.
export interface TunnelStatus {
  state: "down" | "up" | "failed" | "revoked";
  interface?: string;
  last_handshake_sec?: number;
  rx_bytes?: number;
  tx_bytes?: number;
}

contextBridge.exposeInMainWorld("tunnex", api);

export type TunnexBridge = typeof api;
