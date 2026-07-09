// Desktop bridge access (S6.2). The Electron preload exposes a verb-specific
// allowlist as window.tunnex (auth.*/config.*/reserved tunnel.*). Its PRESENCE
// is the desktop signal — one SPA bundle, runtime branch. In the browser
// window.tunnex is undefined and every helper here returns null / false.

export interface AuthStatus {
  loggedIn: boolean;
  expired?: boolean;
  fingerprint?: string;
  expiresAt?: string;
  secureStorage: boolean;
}

export interface TunnexBridge {
  auth: {
    login(): Promise<{ fingerprint: string; expiresAt: string }>;
    logout(): Promise<void>;
    status(): Promise<AuthStatus>;
  };
  config: {
    getServerUrl(): Promise<string>;
    setServerUrl(url: string): Promise<{ url: string; reloginRequired: boolean }>;
  };
  tunnel: {
    // fullTunnel = the split-tunnel toggle intent (S6.4); effective only when a
    // device is minted (get-or-create reuses an existing config as-is).
    up(fullTunnel?: boolean): Promise<TunnelStatus>;
    down(): Promise<void>;
    status(): Promise<TunnelStatus>;
    onStatusChanged(cb: (s: TunnelStatus) => void): () => void;
  };
}

// TunnelStatus mirrors the helper (no secrets — never key material). "revoked" is
// client-synthesized by main (proactive revocation monitor) — the helper never emits it.
export interface TunnelStatus {
  state: "down" | "up" | "failed" | "revoked";
  interface?: string;
  last_handshake_sec?: number;
  rx_bytes?: number;
  tx_bytes?: number;
  address?: string; // the device's assigned tunnel address, e.g. "10.99.0.2/32"
}

declare global {
  interface Window {
    tunnex?: TunnexBridge;
  }
}

export function desktop(): TunnexBridge | null {
  return typeof window !== "undefined" && window.tunnex ? window.tunnex : null;
}

export function isDesktop(): boolean {
  return desktop() !== null;
}
