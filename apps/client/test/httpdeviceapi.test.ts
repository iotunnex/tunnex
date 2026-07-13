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

test("deviceExists: THROWS on an empty org list (inconclusive, never 'gone')", async () => {
  // A 200-OK empty array must NOT be read as "device genuinely gone" — that would let
  // a transient blip tear down a healthy tunnel + revoke a live device. It must throw
  // so the caller's fail-safe keeps the config.
  stubFetch([{ match: "/organizations", body: [] }]);
  await assert.rejects(api().deviceExists("dev-1"), /inconclusive/);
});

test("deviceExists: THROWS on a non-OK read (fail-safe)", async () => {
  stubFetch([{ match: "/organizations", ok: false, status: 503, body: {} }]);
  await assert.rejects(api().deviceExists("dev-1"), /list_organizations_failed/);
});

test("deviceExists: true when the device is active in some org, false when absent everywhere", async () => {
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] },
    { match: "/organizations/o2/devices", body: [] },
    { match: "/organizations", body: [{ id: "o1" }, { id: "o2" }] },
  ]);
  assert.equal(await api().deviceExists("dev-1"), true);

  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "other", status: "active" }] },
    { match: "/organizations/o2/devices", body: [{ id: "dev-1", status: "revoked" }] }, // present but not active
    { match: "/organizations", body: [{ id: "o1" }, { id: "o2" }] },
  ]);
  assert.equal(await api().deviceExists("dev-1"), false); // checked every org, no ACTIVE match
});

test("deviceStatus: maps active/pending/gone + throws on empty orgs (S7.3)", async () => {
  // active
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "active" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1"), "active");
  // pending
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "pending" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1"), "pending");
  // revoked -> gone
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "dev-1", status: "revoked" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1"), "gone");
  // absent everywhere -> gone
  stubFetch([
    { match: "/organizations/o1/devices", body: [{ id: "other", status: "active" }] },
    { match: "/organizations", body: [{ id: "o1" }] },
  ]);
  assert.equal(await api().deviceStatus("dev-1"), "gone");
  // empty org list -> THROWS (inconclusive, never a false "gone"/rejected)
  stubFetch([{ match: "/organizations", body: [] }]);
  await assert.rejects(api().deviceStatus("dev-1"), /inconclusive/);
});
