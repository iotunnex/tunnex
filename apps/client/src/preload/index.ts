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
    up: (): Promise<TunnelStatus> => ipcRenderer.invoke("tunnel:up"),
    down: (): Promise<void> => ipcRenderer.invoke("tunnel:down"),
    status: (): Promise<TunnelStatus> => ipcRenderer.invoke("tunnel:status"),
  },
};

// TunnelStatus mirrors apps/helper (no secrets — never carries key material).
export interface TunnelStatus {
  state: "down" | "up" | "failed";
  interface?: string;
  last_handshake_sec?: number;
  rx_bytes?: number;
  tx_bytes?: number;
}

contextBridge.exposeInMainWorld("tunnex", api);

export type TunnexBridge = typeof api;
