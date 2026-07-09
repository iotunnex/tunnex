import { parseWgConf } from "./wgconf";
import type { TunnelConfigStore } from "./tunnelstore";
import type { TunnelConfig } from "./helperclient";

// DeviceApi is the seam over the tenant API (called from MAIN with the bearer).
// The concrete HTTP adapter mirrors the CLI's device flow (pick org + active node
// → POST create-device → the ONE-TIME .conf text). Kept an interface so the D2
// get-or-create + logout-revoke logic below is unit-tested without a live server.
export interface DeviceApi {
  // createDevice creates a device for the current tenant and returns the one-time
  // config text + the new device id. It is called ONLY when no config is stored
  // for the origin (D2: never a re-fetch).
  createDevice(fullTunnel: boolean): Promise<{ deviceId: string; confText: string }>;
  // revokeDevice best-effort revokes a device against the origin it belongs to.
  revokeDevice(deviceId: string): Promise<void>;
}

// resolveTunnelConfig is the ConfigProvider body: GET-OR-CREATE, origin-keyed.
// If a config is stored for this origin, reuse it (never re-fetch). Otherwise the
// desktop OWNS creation — create a device, capture its one-time config, persist it,
// and return it. full_tunnel is set from the create INTENT (the helper enforces
// both-family completeness when true).
export async function resolveTunnelConfig(
  origin: string,
  fullTunnel: boolean,
  api: DeviceApi,
  store: TunnelConfigStore,
): Promise<TunnelConfig> {
  const existing = store.get(origin);
  if (existing) return existing.config;

  const { deviceId, confText } = await api.createDevice(fullTunnel);
  const config: TunnelConfig = { ...parseWgConf(confText), full_tunnel: fullTunnel };
  store.put({ origin, deviceId, config });
  return config;
}

// clearTunnelConfigForOrigin drops the stored config for an origin and BEST-EFFORT
// revokes its device against THAT origin only (never the current one). Used by
// logout (full-sweep: the local config is gone like the bearer, so revoke the peer
// so it isn't orphaned) and by the UI's remove-orphaned-device action. Revoke
// errors are swallowed — the local state is authoritative for the app; a failed
// server revoke leaves a peer the GC/admin reap handles.
export async function clearTunnelConfigForOrigin(
  origin: string,
  api: DeviceApi,
  store: TunnelConfigStore,
): Promise<void> {
  const removed = store.remove(origin);
  if (!removed) return;
  try {
    await api.revokeDevice(removed.deviceId);
  } catch {
    /* best-effort — local removal already happened */
  }
}
