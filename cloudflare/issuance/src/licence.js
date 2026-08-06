// Licence key minting — the signing half of S12.4.
//
// ⛔ THE PRIVATE KEY IS THE WHOLE COMMERCIAL MODEL. It authorises MINTING, not one licence; a leak is
// unlimited, unrevocable (offline verification means nothing we do afterwards reaches an issued key) and
// UNDETECTABLE (deployments never call home — the telemetry that would show a forged key is exactly what
// the product promises not to have). Full reasoning: docs/S12.4-issuance-decisions.md §3.
//
// So this module has ONE rule that outranks the others:
//
//   ⛔ NOTHING HERE EVER RETURNS, LOGS, SERIALISES OR OTHERWISE EXPOSES THE PRIVATE KEY. `sign` takes the
//      key, uses it, and drops it. There is no "export" affordance to be misused by a later caller.
//
// ⭐ D4 (FOUNDER-RULED): the payload carries a `kid` and the product verifies against a SET of trusted
// public keys. That does NOT make rotation cheap — keys already minted still run to their expiry and the
// installed base still has to upgrade. It makes rotation POSSIBLE TO EXPRESS, by removing the format
// migration that would otherwise sit on top of the upgrade migration.

/** Wire version. Bumped only by a change the verifier cannot read without knowing. */
export const LICENCE_VERSION = 1;

/** The bands, and the gateway ceiling each one buys. `null` = unlimited (Scale). */
export const BANDS = {
  starter: { gateways: 5 },
  growth: { gateways: 20 },
  scale: { gateways: null },
  // ⚠ `trial` is a BAND, not a flag: a trial is a short-dated Growth-shaped key. Modelling it as a
  // separate boolean would create a second code path in the verifier for a thing that differs only in
  // its expiry.
  trial: { gateways: 20 },
};

const enc = new TextEncoder();

/** base64url without padding — the only encoding that survives an email client and a copy-paste. */
export function b64u(bytes) {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function unb64u(str) {
  const s = str.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(s + "=".repeat((4 - (s.length % 4)) % 4));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

/**
 * Build the claim set a key asserts.
 *
 * ⛔ EVERY FIELD IS SOMETHING A HUMAN REVIEWED. There is no field derived at signing time from anything
 * other than the reviewed request, because a value invented at signing time is a value nobody approved —
 * and under §1 nobody can take it back.
 */
export function buildPayload({ kid, domain, band, issuedAt, expiresAt, licenceId }) {
  if (!kid) throw new Error("kid is required — a key with no kid cannot be verified against a key SET");
  if (!domain) throw new Error("domain is required");
  if (!(band in BANDS)) throw new Error(`unknown band: ${band}`);
  if (!(expiresAt > issuedAt)) throw new Error("expiresAt must be after issuedAt");
  return {
    v: LICENCE_VERSION,
    kid,
    id: licenceId,
    dom: domain,
    band,
    gw: BANDS[band].gateways, // ⚠ RESOLVED AT MINT, not looked up at verify: a later change to BANDS must
    // never silently re-price a key already in a customer's hands.
    iat: issuedAt,
    exp: expiresAt,
  };
}

/**
 * Sign a payload. Returns the wire string `tnxl_<payload>.<signature>`.
 *
 * @param privateKey a non-extractable Ed25519 CryptoKey. Non-extractable is deliberate: it makes the
 *                   "never export the key" rule a property the runtime enforces rather than a convention
 *                   this file asks callers to respect.
 */
export async function signLicence(privateKey, payload) {
  const body = b64u(enc.encode(JSON.stringify(payload)));
  const sig = new Uint8Array(
    await crypto.subtle.sign({ name: "Ed25519" }, privateKey, enc.encode(body)),
  );
  return `tnxl_${body}.${b64u(sig)}`;
}

/**
 * Verify a key against a SET of public keys, selecting by `kid` (D4).
 *
 * ⚠ THIS IS THE ISSUER-SIDE MIRROR, NOT THE PRODUCT'S VERIFIER. The product verifies offline in Go
 * (S12.2). It exists here so a mint can be proven correct at the moment it is made, rather than trusted —
 * and because a mint that cannot be verified is one nobody would discover was broken until a customer
 * pasted it in.
 *
 * @param keySet {kid: CryptoKey} — the trusted set.
 */
export async function verifyLicence(keySet, wire) {
  if (typeof wire !== "string" || !wire.startsWith("tnxl_")) return { ok: false, reason: "malformed" };
  const dot = wire.indexOf(".");
  if (dot < 0) return { ok: false, reason: "malformed" };
  const body = wire.slice("tnxl_".length, dot);
  const sigPart = wire.slice(dot + 1);

  let payload;
  try {
    payload = JSON.parse(new TextDecoder().decode(unb64u(body)));
  } catch {
    return { ok: false, reason: "malformed" };
  }

  // ⛔ THE kid IS SELECTED FROM THE TRUSTED SET, NEVER TRUSTED FROM THE TOKEN. An unknown kid is a
  // refusal, not a fallback to "the only key we have" — falling back is how a key set stops being a set
  // and quietly becomes a single key again.
  const key = keySet[payload.kid];
  if (!key) return { ok: false, reason: "unknown_kid" };

  let sig;
  try {
    sig = unb64u(sigPart);
  } catch {
    return { ok: false, reason: "malformed" };
  }
  const ok = await crypto.subtle.verify({ name: "Ed25519" }, key, sig, enc.encode(body));
  if (!ok) return { ok: false, reason: "bad_signature" };
  return { ok: true, payload };
}
