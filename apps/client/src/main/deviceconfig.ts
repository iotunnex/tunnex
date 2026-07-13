import { parseWgConf } from "./wgconf";
import type { TunnelConfigStore } from "./tunnelstore";
import type { TunnelConfig } from "./helperclient";

// DeviceApi is the seam over the tenant API (called from MAIN with the bearer).
// The concrete HTTP adapter mirrors the CLI's device flow (pick org + active node
// → POST create-device → the ONE-TIME .conf text). Kept an interface so the D2
// get-or-create + logout-revoke logic below is unit-tested without a live server.
export interface DeviceApi {
  // createDevice creates a device for the current tenant and returns the one-time
  // config text + the new device id. pendingApproval is true when the org requires
  // device approval (S7.3): the device is enrolled but BLOCKED until an admin approves.
  // It is called ONLY when no config is stored for the origin (D2: never a re-fetch).
  createDevice(fullTunnel: boolean): Promise<{ deviceId: string; confText: string; pendingApproval: boolean; orgId: string }>;
  // revokeDevice best-effort revokes a device against the origin it belongs to.
  revokeDevice(deviceId: string): Promise<void>;
  // deviceStatus is the definitive server status (S7.3): "pending" | "active" | "gone".
  // Queried against the device's OWN org (persisted at create) so a transient list that
  // omits that org can't read as a false "gone" (finding #4). Throws on any read error
  // (inconclusive fail-safe) — a blip never reads as a transition.
  deviceStatus(deviceId: string, orgId: string): Promise<"active" | "pending" | "gone">;
  // deviceExists = deviceStatus === "active" (finding #6: ONE fail-safe, no divergence).
  // Self-heals a stale cached config (device revoked/GC'd) — an EXISTENCE check, not a
  // config re-fetch, so D2 holds.
  deviceExists(deviceId: string, orgId: string): Promise<boolean>;
  // resolveDeviceOrg scans for the device's org (null = gone; throws on a blip) — used to
  // STAMP a legacy config (persisted before orgId existed) onto the hardened direct path.
  resolveDeviceOrg(deviceId: string): Promise<string | null>;
}

// PendingApprovalError aborts the ConfigProvider (resolveTunnelConfig) when the device
// is awaiting approval (S7.3): the helper is NEVER armed for a pending device (no dead
// tunnel, no spurious "revoked" from the RevocationMonitor). connect() catches it, shows
// the stable "awaiting approval" state, and starts the ApprovalMonitor. The deviceId is
// carried so the poll knows what to watch.
export class PendingApprovalError extends Error {
  constructor(public readonly deviceId: string) {
    super("device is awaiting admin approval");
    this.name = "PendingApprovalError";
  }
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
  if (existing && existing.config.full_tunnel !== fullTunnel) {
    // MODE CHANGED (split↔full): the stored profile's AllowedIPs are baked at mint, so it
    // can't satisfy the new intent — reusing it would silently keep the old routing. Drop +
    // best-effort revoke the superseded device (now also CANCELS a still-pending one —
    // RevokeDevice accepts an owner's pending cancel, finding #3), then mint fresh for the
    // new mode. Checked BEFORE the pending short-circuit so a mode toggle WHILE AWAITING
    // approval is honored, not silently dropped (finding #3).
    store.remove(origin);
    try {
      await api.revokeDevice(existing.deviceId);
    } catch {
      /* best-effort — the local removal already happened */
    }
  } else if (existing && existing.pending) {
    // SAME mode + still AWAITING approval: re-signal pending (no re-mint — minting again
    // would create a duplicate pending device each connect). connect() keeps the awaiting
    // state + the ApprovalMonitor (which flips this flag on approval) running.
    throw new PendingApprovalError(existing.deviceId);
  } else if (existing) {
    // Same mode: self-heal a dead (revoked/deleted) config. First, if this is a LEGACY
    // config with no persisted orgId (installed base / v0.1.0), opportunistically resolve +
    // STAMP the org so it migrates onto the hardened direct-query path (the scan retires). A
    // resolve blip leaves it unstamped — deviceExists then falls back to the scan, so the
    // check still works either way.
    let orgId = existing.orgId;
    if (!orgId) {
      try {
        const resolved = await api.resolveDeviceOrg(existing.deviceId);
        if (resolved) {
          orgId = resolved;
          store.put({ ...existing, orgId });
        }
      } catch {
        /* blip — leave unstamped; the fallback scan below still works */
      }
    }
    // Existence check against the device's OWN org (or the scan fallback when unstamped) —
    // NOT a config re-fetch (D2 intact); a transient error KEEPS the config (never nuke on a blip).
    let stillThere = true;
    try {
      stillThere = await api.deviceExists(existing.deviceId, orgId);
    } catch {
      stillThere = true;
    }
    if (stillThere) return existing.config;
    store.remove(origin);
  }

  const { deviceId, confText, pendingApproval, orgId } = await api.createDevice(fullTunnel);
  const config: TunnelConfig = { ...parseWgConf(confText), full_tunnel: fullTunnel };
  // Persist BEFORE the pending gate so the ApprovalMonitor + a later connect have the device
  // (config is valid now; the gateway just won't serve the peer until approved). orgId is
  // persisted so the monitors query the device's OWN org directly (finding #4).
  store.put({ origin, deviceId, orgId, config, pending: pendingApproval });
  if (pendingApproval) {
    throw new PendingApprovalError(deviceId); // S7.3: abort — do NOT arm the helper
  }
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
