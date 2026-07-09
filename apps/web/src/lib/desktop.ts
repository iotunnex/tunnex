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
    up(): Promise<TunnelStatus>;
    down(): Promise<void>;
    status(): Promise<TunnelStatus>;
    onStatusChanged(cb: (s: TunnelStatus) => void): () => void;
  };
}

// TunnelStatus mirrors the helper (no secrets — never key material).
export interface TunnelStatus {
  state: "down" | "up" | "failed";
  interface?: string;
  last_handshake_sec?: number;
  rx_bytes?: number;
  tx_bytes?: number;
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
