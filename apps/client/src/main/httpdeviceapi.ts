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
    const ids = await this.orgIds(); // finding #4: one org-list fetch implementation
    if (!ids.length) throw new Error("no_organization");
    return ids[0];
  }

  private async activeNodeId(orgId: string): Promise<string> {
    const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/nodes`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_nodes_failed: ${r.status}`);
    const nodes = (await r.json()) as Array<{ id: string; status: string }>;
    const active = nodes.find((n) => n.status === "active");
    if (!active) throw new Error("no_active_gateway");
    return active.id;
  }

  async createDevice(fullTunnel: boolean): Promise<{ deviceId: string; confText: string; pendingApproval: boolean; orgId: string }> {
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
    // The config is issued at enrollment even when pending (S7.3 D2) — the peer just
    // isn't served by the gateway until approved. pending_approval tells the caller to
    // hold: gate the tunnel + start the awaiting-approval poll, don't arm the helper.
    const body = (await r.json()) as { device: { id: string }; config?: string; pending_approval?: boolean };
    if (!body.config) throw new Error("no_config_returned"); // server-generated flow only
    return { deviceId: body.device.id, confText: body.config, pendingApproval: body.pending_approval === true, orgId };
  }

  async deviceStatus(deviceId: string, orgId: string): Promise<"active" | "pending" | "gone"> {
    // A PRESENT orgId (new configs, persisted at create) queries the device's OWN org
    // directly — a fetch error THROWS (inconclusive; a blip never reads as a transition),
    // and "gone" is returned ONLY when that org's real list omits the id (no cross-org scan
    // that a transient omit could false-"gone", finding #4).
    //
    // A MISSING/BLANK orgId (a LEGACY config persisted before the orgId field — the
    // installed base / v0.1.0 upgraders) FALLS BACK to the all-orgs SCAN with the empty-orgs
    // inconclusive throw intact, so an upgrader's monitors keep working (never a malformed
    // /organizations//devices URL). Legacy configs stamp orgId on the next resolve
    // (resolveTunnelConfig), retiring the scan path.
    if (!orgId) return (await this.scanDevice(deviceId)).status;
    const s = await this.deviceInOrg(orgId, deviceId);
    return s ?? "gone"; // not in its OWN org -> genuinely gone (direct path, no list-trust)
  }

  // deviceExists is deviceStatus === "active" (finding #6): ONE fail-safe implementation,
  // so the RevocationMonitor (deviceExists) and ApprovalMonitor (deviceStatus) can never
  // disagree on when a device is "gone" vs inconclusive.
  async deviceExists(deviceId: string, orgId: string): Promise<boolean> {
    return (await this.deviceStatus(deviceId, orgId)) === "active";
  }

  // resolveDeviceOrg scans all orgs for the device and returns the org it lives in (null =
  // genuinely gone, per scanDevice's complete-information rule; THROWS on inconclusive).
  // resolveTunnelConfig uses it to STAMP a legacy config's orgId + as the one-fetch existence
  // check (org found = exists), migrating the config onto the direct path so the scan retires.
  async resolveDeviceOrg(deviceId: string): Promise<string | null> {
    return (await this.scanDevice(deviceId)).orgId;
  }

  // scanDevice is the LEGACY all-orgs scan (pre-orgId configs only; retires as configs stamp).
  // COMPLETE-INFORMATION RULE (finding #1): a destructive "gone" requires that EVERY org was
  // read OK and none has the device — a single org's fetch error does NOT abort the scan
  // (a 403 from an org the caller was offboarded from must not mask a device in a later org),
  // and if the device wasn't found AND any fetch failed the verdict is INCONCLUSIVE (throw →
  // keep + backoff), never "gone". Partial information yields unknown, never gone.
  //
  // KNOWN BOUNDED LIMITATION (finding #2, ACCEPTED, TRANSITIONAL): when ALL org fetches
  // succeed but the org LIST itself is transiently partial (omits the device's org), the scan
  // returns a false "gone". Unavoidable when scanning without a known orgId (the direct path
  // has no such trust). Triple-bounded: legacy configs only + a stamp-blip window + a
  // persistently-partial list while all fetches succeed. Self-retires via stamping.
  // RETIREMENT CONDITION: when telemetry/support shows no un-stamped legacy configs remain
  // (or a release-N deprecation), DELETE this scan path and make orgId REQUIRED.
  private async scanDevice(deviceId: string): Promise<{ status: "active" | "pending" | "gone"; orgId: string | null }> {
    const orgIds = await this.orgIds();
    if (orgIds.length === 0) throw new Error("no_organizations: inconclusive");
    let anyFailed = false;
    for (const orgId of orgIds) {
      let s: "active" | "pending" | "gone" | null;
      try {
        s = await this.deviceInOrg(orgId, deviceId);
      } catch {
        anyFailed = true; // a single org's error must not abort the scan (#1)
        continue;
      }
      if (s) return { status: s, orgId };
    }
    if (anyFailed) throw new Error("incomplete_scan: inconclusive"); // partial info -> unknown (#1)
    return { status: "gone", orgId: null }; // every org read OK, none has it -> genuinely gone
  }

  // deviceInOrg fetches ONE org's device list and maps the device's status, or null if it is
  // not in that org. Throws on a non-OK read. The SINGLE per-org lookup both the direct path
  // and the scan share (finding #5 — no duplicated fetch/find/status-map).
  private async deviceInOrg(orgId: string, deviceId: string): Promise<"active" | "pending" | "gone" | null> {
    const r = await fetch(`${this.origin}/api/v1/organizations/${orgId}/devices`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_devices_failed: ${r.status}`);
    const devices = (await r.json()) as Array<{ id: string; status: string }>;
    const d = devices.find((x) => x.id === deviceId);
    if (!d) return null;
    return d.status === "active" ? "active" : d.status === "pending" ? "pending" : "gone";
  }

  private async orgIds(): Promise<string[]> {
    const r = await fetch(`${this.origin}/api/v1/organizations`, { headers: this.headers() });
    if (!r.ok) throw new Error(`list_organizations_failed: ${r.status}`);
    const orgs = (await r.json()) as Array<{ id: string }>;
    return orgs.map((o) => o.id);
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
