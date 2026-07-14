import { test } from "node:test";
import assert from "node:assert/strict";

import { parseWgConf } from "../src/main/wgconf";
import { TunnelConfigStore } from "../src/main/tunnelstore";
import { resolveTunnelConfig, clearTunnelConfigForOrigin, migrateLegacyConfig, PendingApprovalError, type DeviceApi } from "../src/main/deviceconfig";
import { InsecureStorageError, type Persistence, type SafeStorageLike } from "../src/main/credential";

const CONF = `[Interface]
PrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
Address = 10.99.0.2/32
DNS = 10.99.0.1
MTU = 1420

[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`;

test("parseWgConf maps a .conf into a structured config", () => {
  const c = parseWgConf(CONF);
  assert.equal(c.private_key, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=");
  assert.equal(c.peer_public_key, "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=");
  assert.equal(c.address, "10.99.0.2/32");
  assert.equal(c.endpoint, "vpn.example.com:51820");
  assert.deepEqual(c.allowed_ips, ["0.0.0.0/0", "::/0"]);
  assert.deepEqual(c.dns, ["10.99.0.1"]);
  assert.equal(c.mtu, 1420);
  assert.equal(c.persistent_keepalive, 25);
});

test("parseWgConf rejects malformed input", () => {
  assert.throws(() => parseWgConf("PrivateKey = x\n")); // no section
  assert.throws(() => parseWgConf("[Interface]\nAddress = 10.0.0.1/32\n")); // missing PrivateKey
});

// In-memory keychain (identity "encryption") + persistence for the store tests.
function fakeSafe(available = true): SafeStorageLike {
  return {
    isEncryptionAvailable: () => available,
    encryptString: (p) => Buffer.from("enc:" + p, "utf8"),
    decryptString: (b) => b.toString("utf8").replace(/^enc:/, ""),
  };
}
function fakePersist(): Persistence {
  let buf: Buffer | null = null;
  return { read: () => buf, write: (b) => { buf = b; }, clear: () => { buf = null; } };
}

test("TunnelConfigStore is origin-keyed and refuses insecure by default", () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const sc = { origin: "https://a.example", deviceId: "dev-a", orgId: "org-1", config: { ...parseWgConf(CONF), full_tunnel: true } };
  store.put(sc);
  assert.equal(store.get("https://a.example")?.deviceId, "dev-a");
  assert.equal(store.get("https://b.example"), null); // never cross-origin
  assert.equal(store.list().length, 1);
  assert.equal(store.remove("https://a.example")?.deviceId, "dev-a");
  assert.equal(store.get("https://a.example"), null);

  // No keychain + no opt-in → refuse to write plaintext.
  const insecure = new TunnelConfigStore(fakeSafe(false), fakePersist(), false);
  assert.throws(() => insecure.put(sc), (e) => e instanceof InsecureStorageError);
});

// fakeApi counts creates/revokes; `exists` drives the self-heal existence check.
function fakeApi(): DeviceApi & { creates: number; revoked: string[]; exists: boolean; pending: boolean } {
  return {
    creates: 0,
    revoked: [],
    exists: true,
    pending: false, // S7.3: when true, createDevice returns pendingApproval
    async createDevice() {
      this.creates++;
      return { deviceId: "dev-" + this.creates, confText: CONF, pendingApproval: this.pending, orgId: "org-1" };
    },
    async revokeDevice(id: string) {
      this.revoked.push(id);
    },
    async deviceExists() {
      return this.exists;
    },
    async deviceStatus() {
      return this.pending ? "pending" : this.exists ? "active" : "gone";
    },
  };
}

test("resolveTunnelConfig: get-or-create, never re-fetch (D2)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  const origin = "https://t.example";

  const c1 = await resolveTunnelConfig(origin, true, api, store);
  assert.equal(api.creates, 1);
  assert.equal(c1.full_tunnel, true); // intent-set, not guessed
  // Second call reuses the stored config — NO second create (never re-fetch).
  const c2 = await resolveTunnelConfig(origin, true, api, store);
  assert.equal(api.creates, 1);
  assert.equal(c2.private_key, c1.private_key);
});

test("clearTunnelConfigForOrigin: removes + best-effort revokes that origin's device", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  await resolveTunnelConfig("https://t.example", false, api, store);

  await clearTunnelConfigForOrigin("https://t.example", api, store);
  assert.deepEqual(api.revoked, ["dev-1"]);
  assert.equal(store.get("https://t.example"), null);

  // Best-effort: a revoke that throws is swallowed, local removal still happens.
  await resolveTunnelConfig("https://u.example", false, api, store);
  const throwingApi: DeviceApi = { createDevice: api.createDevice.bind(api), revokeDevice: async () => { throw new Error("network"); }, deviceExists: async () => true, deviceStatus: async () => "active" };
  await clearTunnelConfigForOrigin("https://u.example", throwingApi, store); // must not throw
  assert.equal(store.get("https://u.example"), null);
});

test("resolveTunnelConfig: self-heals a revoked device (clear + mint fresh)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  const origin = "https://t.example";

  await resolveTunnelConfig(origin, false, api, store);
  assert.equal(api.creates, 1); // dev-1 minted + stored

  // The device is revoked server-side → the existence check fails on next resolve,
  // so the dead config is dropped and a FRESH device is minted (no manual rm).
  api.exists = false;
  await resolveTunnelConfig(origin, false, api, store);
  assert.equal(api.creates, 2);

  // Fail-safe: a transient existence-check error must NOT nuke a possibly-valid
  // config — reuse it, don't re-create.
  const flakyApi: DeviceApi = {
    createDevice: api.createDevice.bind(api),
    revokeDevice: api.revokeDevice.bind(api),
    deviceExists: async () => { throw new Error("network"); },
    deviceStatus: async () => { throw new Error("network"); },
  };
  await resolveTunnelConfig(origin, false, flakyApi, store);
  assert.equal(api.creates, 2); // reused — no new create on a transient blip
});

test("resolveTunnelConfig: re-mints when the split↔full intent changes", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  const origin = "https://t.example";

  const split = await resolveTunnelConfig(origin, false, api, store);
  assert.equal(api.creates, 1);
  assert.equal(split.full_tunnel, false);

  // Toggling to full-tunnel can't reuse the split profile (AllowedIPs are baked at
  // mint) — the old device is dropped + revoked and a fresh full-tunnel one is minted,
  // so the toggle actually takes effect (a reused split profile would silently ignore it).
  const full = await resolveTunnelConfig(origin, true, api, store);
  assert.equal(api.creates, 2);
  assert.equal(full.full_tunnel, true);
  assert.deepEqual(api.revoked, ["dev-1"]); // the superseded device was revoked

  // Same intent again → reuse (no churn).
  await resolveTunnelConfig(origin, true, api, store);
  assert.equal(api.creates, 2);
});

// S7.3: a pending device GATES the tunnel — resolveTunnelConfig throws PendingApprovalError
// (so tunnel.up() never arms the helper), persists the device with pending=true, and a
// re-resolve while still pending RE-THROWS instead of minting a duplicate (deviceExists
// returns false for pending and would otherwise false-heal into a second create).
test("resolveTunnelConfig: pending device gates (throws, no duplicate re-mint)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  api.pending = true;
  const origin = "https://p.example";

  await assert.rejects(
    () => resolveTunnelConfig(origin, false, api, store),
    (e: unknown) => e instanceof PendingApprovalError && (e as PendingApprovalError).deviceId === "dev-1",
  );
  assert.equal(api.creates, 1); // device minted once
  assert.equal(store.get(origin)?.pending, true); // persisted as pending

  // Re-resolve while STILL pending → re-throws, does NOT mint a second device.
  await assert.rejects(() => resolveTunnelConfig(origin, false, api, store), PendingApprovalError);
  assert.equal(api.creates, 1); // NO duplicate create

  // Once approved (pending flag cleared, device now active) → reuse the stored config.
  const sc = store.get(origin)!;
  store.put({ ...sc, pending: false });
  api.pending = false;
  const cfg = await resolveTunnelConfig(origin, false, api, store);
  assert.ok(cfg); // returned the stored config
  assert.equal(api.creates, 1); // still no re-mint (existence check passes for active)
});

// Finding #3: a MODE change (split<->full) while a device is AWAITING approval must re-mint
// for the new mode, NOT silently re-throw pending for the old-mode device (the reorder:
// mode-mismatch is checked before the pending short-circuit).
test("resolveTunnelConfig: mode change while pending re-mints (not silently dropped)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  api.pending = true;
  const origin = "https://m.example";

  // Enroll split -> pending.
  await assert.rejects(() => resolveTunnelConfig(origin, false, api, store), PendingApprovalError);
  assert.equal(api.creates, 1);
  assert.equal(store.get(origin)?.config.full_tunnel, false);

  // Toggle to full-tunnel while still pending: must abandon the split device (revoke =
  // owner-cancel) and mint a FRESH full-tunnel device — not re-throw the old split one.
  await assert.rejects(() => resolveTunnelConfig(origin, true, api, store), PendingApprovalError);
  assert.equal(api.creates, 2); // re-minted for the new mode
  assert.deepEqual(api.revoked, ["dev-1"]); // the superseded pending device was cancelled
  assert.equal(store.get(origin)?.config.full_tunnel, true);
});

// Finding #1-#5 (stamping): a LEGACY stored config (no orgId, from a pre-orgId build) is
// opportunistically STAMPED with its org on reuse — migrating onto the hardened direct path.

// REDUCTION 2 DEFENSE: connect() migrates a legacy config (clear + revoke + notice, terminal
// for that connect) BEFORE tunnel.up, so this ConfigProvider should never see a no-orgId
// config. If it does, it must NEVER query or arm it — it drops it and creates fresh (it does
// NOT revoke; connect() owns the cap-freeing revoke). This is the belt that guarantees a
// monitor never runs on a legacy config.
test("resolveTunnelConfig: a no-orgId (legacy) config is dropped + re-minted, never queried", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const api = fakeApi();
  let existsCalls = 0;
  let statusCalls = 0;
  const wrapped: DeviceApi = {
    createDevice: api.createDevice.bind(api),
    revokeDevice: api.revokeDevice.bind(api),
    deviceExists: async (...a) => { existsCalls++; return api.deviceExists(...a); },
    deviceStatus: async (...a) => { statusCalls++; return api.deviceStatus(...a); },
  };
  const origin = "https://legacy.example";
  // A legacy stored config: NO orgId field (as an old build persisted it).
  store.put({ origin, deviceId: "dev-old", config: { ...parseWgConf(CONF), full_tunnel: false } } as never);

  const cfg = await resolveTunnelConfig(origin, false, wrapped, store);
  assert.ok(cfg);
  assert.equal(existsCalls, 0); // NEVER queried a no-orgId config (no monitor could run on it)
  assert.equal(statusCalls, 0);
  assert.equal(api.creates, 1); // dropped the legacy config + created fresh
  assert.deepEqual(api.revoked, []); // resolveTunnelConfig does NOT revoke — connect() owns that
  assert.equal(store.get(origin)?.deviceId, "dev-1"); // fresh device
  assert.ok(store.get(origin)?.orgId); // carries orgId (direct path)
});

// REDUCTION 2 harden — REVOKE-FIRST migration. The revoke frees the cap slot the next connect
// needs, so it runs BEFORE clearing and the config is cleared ONLY on revoke success.
test("migrateLegacyConfig: revoke ok -> config cleared (slot freed before next create)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const origin = "https://legacy.example";
  store.put({ origin, deviceId: "dev-old", config: { ...parseWgConf(CONF), full_tunnel: false } } as never);
  const revoked: string[] = [];
  const ok = { revokeDevice: async (id: string) => { revoked.push(id); } } as unknown as DeviceApi;
  await migrateLegacyConfig(origin, "dev-old", ok, store);
  assert.deepEqual(revoked, ["dev-old"]); // slot freed
  assert.equal(store.get(origin), null); // cleared after revoke success
});

// The permanence case, now self-healing: a revoke BLIP keeps the config (throws), and the
// NEXT attempt (working revoke) succeeds — no admin-reap, no lockout.
test("migrateLegacyConfig: a revoke blip KEEPS the config; retry self-heals (no lockout)", async () => {
  const store = new TunnelConfigStore(fakeSafe(), fakePersist(), false);
  const origin = "https://legacy.example";
  store.put({ origin, deviceId: "dev-old", config: { ...parseWgConf(CONF), full_tunnel: false } } as never);

  const failing = { revokeDevice: async () => { throw new Error("network"); } } as unknown as DeviceApi;
  await assert.rejects(() => migrateLegacyConfig(origin, "dev-old", failing, store), /network/);
  assert.equal(store.get(origin)?.deviceId, "dev-old"); // config KEPT (revoke ran before remove)

  const ok = { revokeDevice: async () => {} } as unknown as DeviceApi;
  await migrateLegacyConfig(origin, "dev-old", ok, store); // retry
  assert.equal(store.get(origin), null); // now cleared — self-recovered, no admin-reap
});
