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
  tunnel: Record<string, never>;
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
