// Tunnex licence issuance — Cloudflare Worker (S12.4).
//
// Four surfaces and no more:
//   GET  /              public request form
//   POST /request       validate, attribute a domain, record a trial verdict, queue for a human
//   GET  /admin         pending requests (token-gated)
//   POST /admin/issue   ⛔ THE ONE ACTION: sign and email
//
// ⛔ ISSUANCE IS MANUAL BY DESIGN, NOT BY LIMITATION. Offline verification means there is no revocation —
// a key that leaves here is alive until its expiry and nothing we do afterwards reaches it. Automated
// minting plus no revocation is a system where a mistake cannot be taken back. See
// docs/S12.4-issuance-decisions.md §1. Nothing in this file may mint without a human pressing the button.
//
// ⛔ AND THE PAYMENT SEAM IS A PARAGRAPH, NOT A ROUTE. §4 of the paper describes where a provider webhook
// would arrive and what it would trigger (mark the request paid — and nothing else; the human still
// signs). There is no handler here, and adding one before a provider exists is dormant machinery.

import { BANDS, buildPayload, signLicence, verifyLicence } from "./licence.js";
import { domainOf, trialEligibility } from "./trial.js";

const HTML = { "content-type": "text/html; charset=utf-8" };
const TEXT = { "content-type": "text/plain; charset=utf-8" };

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
  );
}

/**
 * ⛔ CONSTANT-TIME COMPARISON FOR THE ADMIN TOKEN. A `===` on a secret leaks its prefix through timing,
 * and this token is the only thing between the internet and the signing key.
 */
function tokenMatches(a, b) {
  if (typeof a !== "string" || typeof b !== "string" || a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function adminAuthed(request, env) {
  const header = request.headers.get("authorization") || "";
  const token = header.startsWith("Bearer ") ? header.slice(7) : "";
  return Boolean(env.ADMIN_TOKEN) && tokenMatches(token, env.ADMIN_TOKEN);
}

/**
 * Import the active signing key from a Worker secret.
 *
 * ⛔ NON-EXTRACTABLE (`false`). The runtime then enforces "the private key never leaves" rather than this
 * file asking future callers to respect it — construction over discipline. Nothing in this Worker can
 * export, log or return it, because the platform will not produce the bytes.
 *
 * ⭐ D4: the ACTIVE kid is explicit. The product verifies against a SET of public keys and selects by kid,
 * so rotation is a set-membership change rather than a format migration. ⚠ It does NOT make rotation
 * cheap: keys already minted run to their own expiry and the installed base still has to upgrade.
 */
async function activeSigningKey(env) {
  if (!env.SIGNING_KEY_JWK || !env.SIGNING_KID) {
    throw new Error("signing key not configured");
  }
  const jwk = JSON.parse(env.SIGNING_KEY_JWK);
  const key = await crypto.subtle.importKey("jwk", jwk, { name: "Ed25519" }, false, ["sign"]);
  return { key, kid: env.SIGNING_KID };
}

// ── public ───────────────────────────────────────────────────────────────────────────────────────────

function formPage(message) {
  const bands = Object.keys(BANDS)
    .map((b) => `<option value="${b}">${b}</option>`)
    .join("");
  return `<!doctype html><meta charset="utf-8"><title>Request a Tunnex licence</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:38rem;margin:3rem auto;padding:0 1rem}
label{display:block;margin:1rem 0 .25rem;font-weight:600}input,select,textarea{width:100%;padding:.5rem;font:inherit}
button{margin-top:1.5rem;padding:.6rem 1.2rem;font:inherit}.note{background:#f4f4f5;padding:1rem;border-radius:.5rem}</style>
<h1>Request a Tunnex licence</h1>
${message ? `<p class="note">${esc(message)}</p>` : ""}
<p class="note">Licences are reviewed and issued by a person, usually within one business day.
Tunnex verifies licences offline — your deployment never contacts us.</p>
<form method="post" action="/request">
<label>Company<input name="company" required maxlength="200"></label>
<label>Your name<input name="contact_name" maxlength="200"></label>
<label>Work email<input name="email" type="email" required maxlength="320"></label>
<label>Band<select name="band">${bands}</select></label>
<label>Term (months)<input name="term_months" type="number" value="12" min="1" max="60" required></label>
<label>What are you deploying?<textarea name="use_case" rows="4" maxlength="2000"></textarea></label>
<button type="submit">Request licence</button>
</form>`;
}

async function handleRequest(request, env) {
  const form = await request.formData();
  const email = String(form.get("email") || "");
  const domain = domainOf(email);
  const band = String(form.get("band") || "");
  const term = parseInt(String(form.get("term_months") || "0"), 10);
  const company = String(form.get("company") || "").trim();

  // Refusals here are about the request being UNUSABLE, never about whether it deserves a licence — that
  // judgement is the human's.
  if (!company) return new Response(formPage("A company name is required."), { status: 400, headers: HTML });
  if (!domain) return new Response(formPage("That email address could not be read."), { status: 400, headers: HTML });
  if (!(band in BANDS)) return new Response(formPage("Choose a band."), { status: 400, headers: HTML });
  if (!Number.isInteger(term) || term < 1 || term > 60) {
    return new Response(formPage("Term must be between 1 and 60 months."), { status: 400, headers: HTML });
  }

  // ⚠ ADVISORY, NOT A WALL. The verdict is recorded and shown to the reviewer with its reason; the request
  // is queued either way. A legitimate second trial is a conversation.
  let note = "";
  if (band === "trial") {
    const prior = await env.DB.prepare("SELECT trial_issued_at FROM trial_domains WHERE domain = ?")
      .bind(domain)
      .first();
    const verdict = trialEligibility(domain, prior?.trial_issued_at ?? null);
    if (!verdict.eligible) {
      note = verdict.reason === "already_trialled"
        ? `previous trial issued ${new Date(verdict.priorIssuedAt * 1000).toISOString().slice(0, 10)}`
        : verdict.reason;
    }
  }

  await env.DB.prepare(
    `INSERT INTO requests (id, company, contact_name, email, domain, band, term_months, use_case, trial_note, created_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  )
    .bind(
      crypto.randomUUID(), company, String(form.get("contact_name") || "").trim(), email.trim(),
      domain, band, term, String(form.get("use_case") || "").trim(), note,
      Math.floor(Date.now() / 1000),
    )
    .run();

  // ⚠ THE SAME ANSWER WHETHER OR NOT THE TRIAL VERDICT WAS NEGATIVE. Telling a requester "your domain
  // already trialled" at the form turns the table into an oracle for what we know about a company, and
  // teaches a determined requester to try another domain — which is the one thing this control cannot
  // survive.
  return new Response(
    formPage("Thanks — your request is with us. You will hear from a person, usually within one business day."),
    { headers: HTML },
  );
}

// ── admin ────────────────────────────────────────────────────────────────────────────────────────────

async function adminPage(env) {
  const { results } = await env.DB.prepare(
    `SELECT id, company, email, domain, band, term_months, use_case, trial_note, created_at
       FROM requests WHERE status = 'pending' ORDER BY created_at`,
  ).all();

  const rows = (results || [])
    .map(
      (r) => `<tr>
<td>${esc(r.company)}<br><small>${esc(r.email)}</small></td>
<td>${esc(r.domain)}</td>
<td>${esc(r.band)} / ${esc(r.term_months)}m
${r.trial_note ? `<br><b class="warn">⚠ ${esc(r.trial_note)}</b>` : ""}</td>
<td><small>${esc(r.use_case).slice(0, 300)}</small></td>
<td><button data-id="${esc(r.id)}" class="issue">Sign &amp; email</button>
<button data-id="${esc(r.id)}" class="refuse">Refuse</button></td></tr>`,
    )
    .join("");

  return `<!doctype html><meta charset="utf-8"><title>Issuance queue</title>
<style>body{font:15px/1.45 system-ui,sans-serif;margin:2rem}table{border-collapse:collapse;width:100%}
td,th{border-bottom:1px solid #ddd;padding:.6rem;vertical-align:top;text-align:left}.warn{color:#b45309}
#out{white-space:pre-wrap;background:#f4f4f5;padding:1rem;margin-top:1rem;border-radius:.5rem}</style>
<h1>Pending requests</h1>
<p>⛔ Every key issued here is <b>unrevocable</b>. Check the domain and the band before signing.</p>
<table><tr><th>Company</th><th>Domain</th><th>Band</th><th>Use case</th><th></th></tr>${rows || "<tr><td colspan=5>Nothing pending.</td></tr>"}</table>
<div id="out"></div>
<script>
const token = new URLSearchParams(location.search).get("t") || "";
document.querySelectorAll("button").forEach(b => b.onclick = async () => {
  const refuse = b.classList.contains("refuse");
  if (!confirm(refuse ? "Refuse this request?" : "Sign and email a licence? This CANNOT be revoked.")) return;
  b.disabled = true;
  const res = await fetch("/admin/issue", {
    method: "POST",
    headers: { "authorization": "Bearer " + token, "content-type": "application/json" },
    body: JSON.stringify({ id: b.dataset.id, refuse })
  });
  document.getElementById("out").textContent = await res.text();
  if (res.ok) b.closest("tr").style.opacity = .4;
});
</script>`;
}

/**
 * ⛔ THE ONE ACTION. It signs what the request says and nothing a human typed at this screen — every
 * field that determines the key's content was captured at request time and reviewed. A signing form with
 * editable fields is a second place for a typo to become an unrevocable grant (paper §2).
 */
async function handleIssue(request, env) {
  const { id, refuse } = await request.json();
  const req = await env.DB.prepare("SELECT * FROM requests WHERE id = ? AND status = 'pending'")
    .bind(id)
    .first();
  if (!req) return new Response("no such pending request", { status: 404, headers: TEXT });

  if (refuse) {
    await env.DB.prepare("UPDATE requests SET status = 'refused', decided_at = ? WHERE id = ?")
      .bind(Math.floor(Date.now() / 1000), id)
      .run();
    return new Response("refused", { headers: TEXT });
  }

  const now = Math.floor(Date.now() / 1000);
  const expiresAt = now + req.term_months * 30 * 24 * 3600;
  const licenceId = crypto.randomUUID();

  const { key, kid } = await activeSigningKey(env);
  const payload = buildPayload({
    kid, domain: req.domain, band: req.band, issuedAt: now, expiresAt, licenceId,
  });
  const wire = await signLicence(key, payload);

  // ⛔ VERIFY WHAT WE JUST MINTED, BEFORE IT LEAVES. A key that does not verify cannot be recalled and
  // cannot be diagnosed from the customer's side — they simply cannot activate, and we would have no
  // record that anything was wrong. This costs microseconds and closes the one failure mode where the
  // artefact is broken rather than merely mistaken.
  const publicJwk = env.SIGNING_PUBLIC_JWK ? JSON.parse(env.SIGNING_PUBLIC_JWK) : null;
  if (publicJwk) {
    const pub = await crypto.subtle.importKey("jwk", publicJwk, { name: "Ed25519" }, true, ["verify"]);
    const check = await verifyLicence({ [kid]: pub }, wire);
    if (!check.ok) {
      return new Response(`REFUSING TO ISSUE: minted key failed self-verification (${check.reason})`, {
        status: 500,
        headers: TEXT,
      });
    }
  }

  const batch = [
    env.DB.prepare(
      `INSERT INTO issued_keys (id, request_id, domain, band, kid, issued_at, expires_at, licence_key)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    ).bind(licenceId, req.id, req.domain, req.band, kid, now, expiresAt, wire),
    env.DB.prepare("UPDATE requests SET status = 'issued', decided_at = ? WHERE id = ?").bind(now, req.id),
  ];
  if (req.band === "trial") {
    batch.push(
      env.DB.prepare(
        `INSERT INTO trial_domains (domain, trial_issued_at, request_id) VALUES (?, ?, ?)
         ON CONFLICT (domain) DO UPDATE SET trial_issued_at = excluded.trial_issued_at`,
      ).bind(req.domain, now, req.id),
    );
  }
  // ⛔ RECORDED BEFORE IT IS SENT. If delivery fails we still know what exists in the world; if we sent
  // first and the write failed, we would have minted an unrevocable key with no record of it.
  await env.DB.batch(batch);

  const delivery = await sendLicenceEmail(env, req, wire, expiresAt);
  if (delivery.sent) {
    await env.DB.prepare("UPDATE issued_keys SET emailed_at = ? WHERE id = ?").bind(now, licenceId).run();
    return new Response(`issued and emailed to ${req.email}`, { headers: TEXT });
  }
  // ⚠ NEVER SILENT. The key exists whether or not the mail went; the operator must be handed it so they
  // can deliver it themselves rather than discovering days later that nothing arrived.
  return new Response(
    `ISSUED, BUT NOT EMAILED (${delivery.reason}). Send this to ${req.email} manually:\n\n${wire}`,
    { headers: TEXT },
  );
}

/**
 * Delivery. Configured or absent — never a silent failure.
 *
 * ⚠ Deliberately provider-shaped and provider-agnostic: one HTTPS POST to whatever transactional-mail API
 * is configured. With no configuration it reports "not configured" and the caller hands the key to the
 * operator, which is a working path rather than a broken one.
 */
async function sendLicenceEmail(env, req, wire, expiresAt) {
  if (!env.MAIL_ENDPOINT || !env.MAIL_TOKEN || !env.MAIL_FROM) {
    return { sent: false, reason: "mail not configured" };
  }
  const expires = new Date(expiresAt * 1000).toISOString().slice(0, 10);
  const body = {
    from: env.MAIL_FROM,
    to: [req.email],
    subject: `Your Tunnex licence (${req.band})`,
    text: `Hello,

Here is your Tunnex licence key for ${req.domain} (${req.band}, valid until ${expires}):

${wire}

Paste it into Settings → Licence in your Tunnex console.

Your deployment verifies this key offline and never contacts us.

— Tunnex`,
  };
  try {
    const res = await fetch(env.MAIL_ENDPOINT, {
      method: "POST",
      headers: { authorization: `Bearer ${env.MAIL_TOKEN}`, "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) return { sent: false, reason: `mail API ${res.status}` };
    return { sent: true };
  } catch (e) {
    return { sent: false, reason: `mail transport: ${e.message}` };
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (request.method === "GET" && url.pathname === "/") {
      return new Response(formPage(""), { headers: HTML });
    }
    if (request.method === "POST" && url.pathname === "/request") {
      return handleRequest(request, env);
    }

    // ⛔ ADMIN IS TOKEN-GATED HERE, AND THAT IS THE FLOOR RATHER THAN THE ANSWER. The real control is
    // Cloudflare Access in front of this route — see README. Whoever reaches admin reaches the signing
    // key by another route, which is why the account boundary is a register row in the paper (§6.1).
    if (url.pathname.startsWith("/admin")) {
      if (!adminAuthed(request, env)) {
        return new Response("unauthorized", { status: 401, headers: TEXT });
      }
      if (request.method === "GET" && url.pathname === "/admin") {
        return new Response(await adminPage(env), { headers: HTML });
      }
      if (request.method === "POST" && url.pathname === "/admin/issue") {
        return handleIssue(request, env);
      }
    }

    return new Response("not found", { status: 404, headers: TEXT });
  },
};
