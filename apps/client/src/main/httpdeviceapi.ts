import os from "node:os";
import type { DeviceApi } from "./deviceconfig";

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
    if (!r.ok) throw new Error(`create_device_failed: ${r.status}`);
    const body = (await r.json()) as { device: { id: string }; config?: string };
    if (!body.config) throw new Error("no_config_returned"); // server-generated flow only
    return { deviceId: body.device.id, confText: body.config };
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
