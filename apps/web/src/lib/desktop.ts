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

/** Mirrors the preload's AppInfo. `update` is a build-time verdict, not a network result. */
export interface AppInfo {
  version: string;
  update:
    | { kind: "disabled"; reason: string; detail: string }
    | { kind: "no_feed"; reason: string; detail: string }
    | { kind: "ready" };
}

/** An imported `.conf` profile, as main reports it — never key material. */
export interface ImportedProfile {
  address?: string;
  fullTunnel: boolean;
}

export interface TunnexBridge {
  auth: {
    login(): Promise<{ fingerprint: string; expiresAt: string }>;
    logout(): Promise<void>;
    status(): Promise<AuthStatus>;
  };
  // Troubleshooting. `openLogs` reveals the file in the OS file manager — the log is never read
  // into the renderer, so a compromised page cannot read it out.
  diag: {
    logPath(): Promise<string>;
    openLogs(): Promise<void>;
    readLog(): Promise<string>;
    exportLog(): Promise<string | null>;
    appInfo(): Promise<AppInfo>;
  };
  config: {
    getServerUrl(): Promise<string>;
    setServerUrl(
      url: string,
    ): Promise<{ url: string; reloginRequired: boolean }>;
  };
  tunnel: {
    // fullTunnel = the split-tunnel toggle intent (S6.4); effective only when a
    // device is minted (get-or-create reuses an existing config as-is).
    up(fullTunnel?: boolean): Promise<TunnelStatus>;
    down(): Promise<void>;
    status(): Promise<TunnelStatus>;
    onStatusChanged(cb: (s: TunnelStatus) => void): () => void;
    importConfig(): Promise<ImportedProfile | null>;
    importedInfo(): Promise<ImportedProfile | null>;
    forgetImported(): Promise<void>;
  };
}

// TunnelStatus mirrors the helper (no secrets — never key material). "revoked" and
// "migrate_failed" are client-synthesized by main (the helper never emits them):
// "revoked" from the proactive revocation monitor; "migrate_failed" is the one bounded
// failure outcome of a legacy-config replacement (S7.3), surfaced so a stuck migration
// shows a distinct, actionable state instead of a bare "Disconnected".
export interface TunnelStatus {
  state: "down" | "up" | "failed" | "revoked" | "migrate_failed";
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

// ⛔ `isDesktop()` REMOVED (S14.20 step 4). Its callers were the six branches that made one bundle
// serve two products; the last of them went with this change, and the only surviving mention is a
// comment in `client.tsx` describing what used to be. `desktop()` stays — the client's own surface
// asks the bridge for real things.
