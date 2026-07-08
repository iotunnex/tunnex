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
  // Reserved for S6.3 tunnel control — intentionally empty (the namespace is
  // declared so the shape is stable, but exposes no privileged action yet).
  tunnel: {} as Record<string, never>,
};

contextBridge.exposeInMainWorld("tunnex", api);

export type TunnexBridge = typeof api;
