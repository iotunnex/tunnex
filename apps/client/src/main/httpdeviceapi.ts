import os from "node:os";
import type { DeviceApi } from "./deviceconfig";

// createErr surfaces the server's TYPED error code (body.error.code) when present, so
// a caller can match on it — e.g. the S3.7 `gateway_no_egress` full-tunnel refusal the
// UI mirrors cleanly. Falls back to the status when the body isn't the typed shape.
async function createErr(r: Response): Promise<string> {
  try {
    const body = (await r.json()) as { error?: { code?: string } };
    // Keep BOTH the numeric status (diagnosable: 401 vs 403 vs 5xx) AND the typed code
    // (matchable: e.g. the future S3.7 gateway_no_egress). friendly() uses .includes()
    // so either substring still matches.
    if (body?.error?.code) return `create_device_failed: ${r.status} ${body.error.code}`;
  } catch {
    /* non-JSON body — fall through to the status */
  }
  return `create_device_failed: ${r.status}`;
}

// HttpDeviceApi is the concrete DeviceApi over the tenant REST API, called from
// MAIN with the bearer (never the renderer). It mirrors the CLI's device flow:
// pick the caller's org + an active gateway node, POST create-device, and capture
// the ONE-TIME .conf. Runtime is human-smoke (needs a live tenant); the shape is
// tsc-checked against the OpenAPI contract.
export class HttpDeviceApi implements DeviceApi {
  constructor(
    private readonly origin: string,
    private readonly getToken: () => string | null,
  ) {}

  private headers(): Record<string, string> {
    const t = this.getToken();
    if (!t) throw new Error("not_authenticated");
    // Bearer requests carry no cookie, so the server's CSRF guard is inert; the
    // header is presence-only and harmless (matches the shared client posture).
    return { authorization: `Bearer ${t}`, "content-type": "application/json", "x-tunnex-csrf": "1" };
  }

  private async firstOrgId(): Promise<string> {
    const r = await fetch(`${this.origin}/api/v1/organizations`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_organizations_failed: ${r.status}`);
    const orgs = (await r.json()) as Array<{ id: string }>;
    if (!orgs.length) throw new Error("no_organization");
    return orgs[0].id;
  }

  private async activeNodeId(orgId: string): Promise<string> {
    const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/nodes`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_nodes_failed: ${r.status}`);
    const nodes = (await r.json()) as Array<{ id: string; status: string }>;
    const active = nodes.find((n) => n.status === "active");
    if (!active) throw new Error("no_active_gateway");
    return active.id;
  }

  async createDevice(fullTunnel: boolean): Promise<{ deviceId: string; confText: string }> {
    const orgId = await this.firstOrgId();
    const nodeId = await this.activeNodeId(orgId);
    const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/devices`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({
        name: `tunnex-desktop-${os.hostname()}`,
        node_id: nodeId,
        full_tunnel: fullTunnel,
        platform: process.platform,
      }),
    });
    if (!r.ok) throw new Error(await createErr(r));
    const body = (await r.json()) as { device: { id: string }; config?: string };
    if (!body.config) throw new Error("no_config_returned"); // server-generated flow only
    return { deviceId: body.device.id, confText: body.config };
  }

  private async orgIds(): Promise<string[]> {
    const r = await fetch(`${this.origin}/api/v1/organizations`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_organizations_failed: ${r.status}`);
    const orgs = (await r.json()) as Array<{ id: string }>;
    return orgs.map((o) => o.id);
  }

  async deviceExists(deviceId: string): Promise<boolean> {
    // Check EVERY org, not just orgs[0]: the device may have been created under a
    // different org, and orgs[0] is not stable across list calls — resolving the wrong
    // org would report a live device as absent and make the self-heal delete a good
    // config + re-mint a device (review #8). Any read error THROWS so the caller's
    // fail-safe keeps the config (never nuke on a partial/transient failure).
    const orgIds = await this.orgIds();
    // An EMPTY org list for a client that has a running device is anomalous (read-replica
    // lag, an in-flight membership change, a brief hiccup). Treat it as INCONCLUSIVE and
    // THROW — a bare 200-with-[] must not slip past the fail-safe and be read as "device
    // genuinely gone", which would tear down a healthy tunnel + revoke a live device.
    if (orgIds.length === 0) throw new Error("no_organizations: inconclusive");
    for (const orgId of orgIds) {
      const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/devices`, { headers: this.headers() });
      if (!r.ok) throw new Error(`list_devices_failed: ${r.status}`);
      const devices = (await r.json()) as Array<{ id: string; status: string }>;
      if (devices.some((d) => d.id === deviceId && d.status === "active")) return true;
    }
    return false; // checked every org; no active device with this id anywhere → genuinely gone
  }

  async revokeDevice(deviceId: string): Promise<void> {
    // Re-resolve the org (the device is on the caller's first org, as created).
    const orgId = await this.firstOrgId();
    const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/devices/${deviceId}/revoke`, {
      method: "POST",
      headers: this.headers(),
    });
    // 404 = already gone: fine for a best-effort revoke.
    if (!r.ok && r.status !== 404) throw new Error(`revoke_device_failed: ${r.status}`);
  }
}
