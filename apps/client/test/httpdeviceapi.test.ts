import { test, afterEach } from "node:test";
import assert from "node:assert/strict";

import { HttpDeviceApi } from "../src/main/httpdeviceapi";

// Stub global fetch with a scripted per-path responder. Each entry matches a URL
// substring and yields { ok, status, body }.
type Route = { match: string; ok?: boolean; status?: number; body: unknown };
const realFetch = globalThis.fetch;
function stubFetch(routes: Route[]) {
  globalThis.fetch = (async (url: string) => {
    const r = routes.find((rt) => url.includes(rt.match));
    if (!r) throw new Error(`no stub for ${url}`);
    return {
      ok: r.ok ?? true,
      status: r.status ?? 200,
      json: async () => r.body,
    } as Response;
  }) as typeof fetch;
}
afterEach(() => {
  globalThis.fetch = realFetch;
});

const api = () => new HttpDeviceApi("https://t.example", () => "tok");

test("deviceStatus: queries the device's OWN org directly + maps status (S7.3 #4)", async () => {
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] }]);
  assert.equal(await api().deviceStatus("dev-1", "o1"), "active");
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "pending" }] }]);
  assert.equal(await api().deviceStatus("dev-1", "o1"), "pending");
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "revoked" }] }]);
  assert.equal(await api().deviceStatus("dev-1", "o1"), "gone");
  // absent in its OWN org -> genuinely gone (no cross-org scan that a transient omit could
  // false-"gone" — finding #4).
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "other", status: "active" }] }]);
  assert.equal(await api().deviceStatus("dev-1", "o1"), "gone");
});

test("deviceStatus: THROWS on a non-OK read (fail-safe — a blip never reads as a transition)", async () => {
  stubFetch([{ match: "/organizations/o1/devices", ok: false, status: 503, body: {} }]);
  await assert.rejects(api().deviceStatus("dev-1", "o1"), /list_devices_failed/);
});

test("deviceExists = deviceStatus === 'active' (#6: one fail-safe, no divergence)", async () => {
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] }]);
  assert.equal(await api().deviceExists("dev-1", "o1"), true);
  stubFetch([{ match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "pending" }] }]);
  assert.equal(await api().deviceExists("dev-1", "o1"), false); // pending is not active
  stubFetch([{ match: "/organizations/o1/devices", ok: false, status: 500, body: {} }]);
  await assert.rejects(api().deviceExists("dev-1", "o1"), /list_devices_failed/); // inherits the throw
});

test("deviceStatus: blank orgId (legacy config) falls back to the all-orgs scan (#1-#5)", async () => {
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1", ""), "active");
  // scan empty-org-list -> THROWS inconclusive (never a malformed /organizations//devices URL).
  stubFetch([{ match: "/organizations", body: [] }]);
  await assert.rejects(api().deviceStatus("dev-1", ""), /inconclusive/);
  // revoked, found via scan -> "gone" (the revocation IS detected for legacy configs).
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "revoked" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1", ""), "gone");
});

test("resolveDeviceOrg: returns the found org (for stamping), null when gone", async () => {
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().resolveDeviceOrg("dev-1"), "o1");
  stubFetch([
    { match: "/organizations/o1/devices", body: [] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().resolveDeviceOrg("dev-1"), null);
});
