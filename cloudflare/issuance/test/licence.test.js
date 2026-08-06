import { test } from "node:test";
import assert from "node:assert/strict";
import { BANDS, buildPayload, signLicence, verifyLicence, unb64u, b64u } from "../src/licence.js";

// Node 22's WebCrypto Ed25519 is the same API the Worker runtime exposes, so these run the REAL signing
// path with no mock and no install.
async function keypair() {
  return crypto.subtle.generateKey({ name: "Ed25519" }, false, ["sign", "verify"]);
}

const base = { domain: "acme.com", band: "growth", issuedAt: 1000, expiresAt: 2000, licenceId: "lic-1" };

test("a signed licence verifies against a key set that contains its kid", async () => {
  const { privateKey, publicKey } = await keypair();
  const wire = await signLicence(privateKey, buildPayload({ ...base, kid: "k1" }));
  assert.match(wire, /^tnxl_[\w-]+\.[\w-]+$/);
  const r = await verifyLicence({ k1: publicKey }, wire);
  assert.equal(r.ok, true);
  assert.equal(r.payload.dom, "acme.com");
  assert.equal(r.payload.gw, 20);
});

// ⛔ D4 — THE POINT OF A KEY SET. Two keys coexist; each verifies only against its own kid. Without this,
// "we support a set" is a field in a JSON object and nothing more.
test("a key SET verifies keys minted under different kids", async () => {
  const a = await keypair();
  const b = await keypair();
  const set = { k1: a.publicKey, k2: b.publicKey };

  const wireA = await signLicence(a.privateKey, buildPayload({ ...base, kid: "k1" }));
  const wireB = await signLicence(b.privateKey, buildPayload({ ...base, kid: "k2" }));

  assert.equal((await verifyLicence(set, wireA)).ok, true);
  assert.equal((await verifyLicence(set, wireB)).ok, true);
});

// ⛔ AND THE HALF THAT MAKES ROTATION MEAN ANYTHING: dropping a kid from the set stops its keys verifying.
// A rotation that could not retire the old key would not be a rotation.
test("removing a kid from the set retires every key minted under it", async () => {
  const a = await keypair();
  const b = await keypair();
  const wireA = await signLicence(a.privateKey, buildPayload({ ...base, kid: "k1" }));

  assert.equal((await verifyLicence({ k1: a.publicKey, k2: b.publicKey }, wireA)).ok, true);
  const after = await verifyLicence({ k2: b.publicKey }, wireA); // k1 retired
  assert.equal(after.ok, false);
  assert.equal(after.reason, "unknown_kid");
});

// ⛔ AN UNKNOWN kid IS A REFUSAL, NEVER A FALLBACK TO "the only key we have".
//
// This is the mutation that turns a key set back into a single key without anyone noticing: a verifier
// that falls back when the kid is unrecognised accepts a key signed by a RETIRED — possibly compromised —
// key, and every test above still passes.
test("an unknown kid is refused, never fallen back from", async () => {
  const a = await keypair();
  const b = await keypair();
  const wire = await signLicence(a.privateKey, buildPayload({ ...base, kid: "retired" }));
  const r = await verifyLicence({ current: b.publicKey }, wire);
  assert.equal(r.ok, false);
  assert.equal(r.reason, "unknown_kid");
});

// ⛔ THE SIGNATURE COVERS THE CLAIMS. A tampered band must not verify — this is the difference between a
// licence and a suggestion.
test("editing the payload invalidates the signature", async () => {
  const { privateKey, publicKey } = await keypair();
  const wire = await signLicence(privateKey, buildPayload({ ...base, band: "starter", kid: "k1" }));

  const [body, sig] = wire.slice("tnxl_".length).split(".");
  const payload = JSON.parse(new TextDecoder().decode(unb64u(body)));
  payload.band = "scale"; // upgrade yourself
  payload.gw = null;
  const forged = `tnxl_${b64u(new TextEncoder().encode(JSON.stringify(payload)))}.${sig}`;

  const r = await verifyLicence({ k1: publicKey }, forged);
  assert.equal(r.ok, false);
  assert.equal(r.reason, "bad_signature");
});

// ⚠ THE BAND CEILING IS RESOLVED AT MINT, NOT AT VERIFY. If it were looked up at verify time, editing
// BANDS later would silently re-price every key already in a customer's hands — a change to a grant
// nobody re-issued and nobody could take back.
test("the gateway ceiling is baked into the payload at mint time", async () => {
  const { privateKey, publicKey } = await keypair();
  const wire = await signLicence(privateKey, buildPayload({ ...base, band: "starter", kid: "k1" }));
  const r = await verifyLicence({ k1: publicKey }, wire);
  assert.equal(r.payload.gw, 5);
  assert.equal(BANDS.starter.gateways, 5, "if this changed, existing keys must NOT change with it");
});

test("scale is unlimited, expressed as null rather than a large number", async () => {
  const { privateKey, publicKey } = await keypair();
  const wire = await signLicence(privateKey, buildPayload({ ...base, band: "scale", kid: "k1" }));
  const r = await verifyLicence({ k1: publicKey }, wire);
  assert.equal(r.payload.gw, null); // a sentinel like 9999 is a ceiling someone eventually hits
});

// Refusals at BUILD time, where a human can still fix them — not at sign time, where the mistake becomes
// an unrevocable grant (§1).
test("a payload with no kid is refused at build time", () => {
  assert.throws(() => buildPayload({ ...base, kid: "" }), /kid is required/);
});

test("an unknown band is refused at build time", () => {
  assert.throws(() => buildPayload({ ...base, kid: "k1", band: "enterprise-plus" }), /unknown band/);
});

test("an expiry at or before issue is refused at build time", () => {
  assert.throws(() => buildPayload({ ...base, kid: "k1", expiresAt: 1000 }), /must be after/);
  assert.throws(() => buildPayload({ ...base, kid: "k1", expiresAt: 999 }), /must be after/);
});

test("malformed wire strings are refused rather than throwing", async () => {
  const { publicKey } = await keypair();
  for (const bad of ["", "nope", "tnxl_nodot", "tnxl_!!!.!!!", 42, null]) {
    const r = await verifyLicence({ k1: publicKey }, bad);
    assert.equal(r.ok, false, `expected refusal for ${String(bad)}`);
  }
});
