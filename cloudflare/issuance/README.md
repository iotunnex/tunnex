# Tunnex licence issuance (Cloudflare Worker + D1)

Manual licence issuance. A request form, a queue, and one button that signs and emails.

**Decisions and reasoning: `docs/S12.4-issuance-decisions.md`.** This file is how to run it.

> ⛔ **Issuance is manual by design.** Tunnex verifies licences offline, so there is **no revocation** — a
> key that leaves here is alive until its expiry. Automated minting plus no revocation is a system where a
> mistake cannot be taken back. The human is a design property, not a shortcut.

---

## Layout

| | |
| --- | --- |
| `src/index.js` | the Worker — form, queue, and the one issue action |
| `src/licence.js` | payload, sign, verify. Pure; no Worker APIs |
| `src/trial.js` | domain attribution and trial eligibility. Pure |
| `schema.sql` | D1: `requests`, `issued_keys`, `trial_domains` |
| `test/` | `node --test`, no install, real WebCrypto |

**Plain ESM JavaScript, not TypeScript** — deliberately. It runs unmodified in the Worker *and* under
`node --test` with **zero install and no build step**, so the signing logic is provable on any machine
that has Node. This service is outside the pnpm workspace and outside the product's runtime path; a
toolchain here would buy consistency and cost provability.

```sh
npm test     # 19 tests, real Ed25519, no dependencies
```

---

## ⛔ Generating the signing key — the highest-risk five minutes in this system's life

**Read `docs/S12.4-issuance-decisions.md` §3 first.** The private key authorises *minting*, not one
licence. A leak is unlimited, unrevocable, and undetectable.

⚠ **AND HERE IS THE HONEST LIMIT OF THE DESIGN.** The Worker imports the key as **non-extractable**, so
nothing running in production can export it — but **to put it into a Worker secret you must first hold it
as text.** The key therefore exists in plaintext exactly once, on whatever machine generates it.

> ## ⛔ **THAT MOMENT IS THE ONLY TIME THE COMMERCIAL MODEL IS COPYABLE. TREAT IT AS A CEREMONY, NOT A
> ## COMMAND.**

Do this on a machine you trust, with shell history off, and do not let the value touch a file, a
clipboard manager, a password manager, a note, or a terminal that scrolls back:

```sh
set +o history   # bash; zsh: unsetopt HISTORY

node -e '
const { subtle } = crypto;
subtle.generateKey({ name: "Ed25519" }, true, ["sign","verify"]).then(async k => {
  const priv = await subtle.exportKey("jwk", k.privateKey);
  const pub  = await subtle.exportKey("jwk", k.publicKey);
  console.log("KID:    ", "k" + new Date().getFullYear());
  console.log("PRIVATE:", JSON.stringify(priv));
  console.log("PUBLIC: ", JSON.stringify(pub));
})'

wrangler secret put SIGNING_KEY_JWK      # paste PRIVATE
wrangler secret put SIGNING_PUBLIC_JWK   # paste PUBLIC
wrangler secret put SIGNING_KID          # paste KID

clear && set -o history
```

**The PUBLIC half is what gets baked into the product binary** (S12.2), as one member of a **key set** —
see below. Keep it; it is not a secret and you will need it again.

**The PRIVATE half now exists only inside Cloudflare.** `wrangler secret put` is write-only: you cannot
read it back, and neither can anyone who reaches the dashboard. That is the property doing the work.

### ⭐ The key SET, and what it does not buy

Every issued licence carries a **`kid`**. The product verifies against a **set** of trusted public keys and
selects by `kid` (founder-ruled, D4).

Rotation is then: generate a new key with a new `kid` → add its public half to the product's set → ship →
issue new keys under it → eventually drop the old `kid` from the set.

⛔ **This does not make rotation cheap:**

- keys already minted under the old `kid` **run to their own expiry** — dropping it from the set is what
  finally stops them, and only for deployments that upgraded
- **the installed base still has to upgrade** to receive a changed set
- **compromise is still undetectable** — nothing will tell you to start

**A key set makes rotation possible to express. It removes the *format* migration that would otherwise sit
on top of the upgrade migration** — the part that would make rotation unthinkable rather than merely
expensive.

---

## Deploy

```sh
wrangler d1 create tunnex-issuance          # put the printed id into wrangler.toml
wrangler d1 execute tunnex-issuance --remote --file=schema.sql
wrangler deploy
```

### ⛔ Put Cloudflare Access in front of `/admin` before this is real

`ADMIN_TOKEN` is a **floor**, not the answer. It is one shared bearer token with no identity, no audit and
no revocation of its own.

> ⛔ **WHOEVER REACHES `/admin` CAN MINT UNREVOCABLE LICENCES. WHOEVER REACHES THE CLOUDFLARE ACCOUNT CAN
> READ WHAT THE WORKER DOES WITH THE SIGNING KEY, OR DEPLOY CODE THAT EXPORTS IT.**

The account boundary **is** the key's protection — it is a register row in the paper (§6.1), not IT
hygiene. Hardware MFA, minimal membership, no shared logins.

---

## Issuing

1. Open `/admin?t=<ADMIN_TOKEN>`.
2. Read the request. **Check the domain and the band** — the key asserts the domain, and it cannot be
   recalled.
3. `Sign & email`, or `Refuse`.

**The button signs what the request says.** There is no editable field at signing time: everything that
determines the key was captured at request time and reviewed. A signing form is a second place for a typo
to become a permanent grant.

**Before it leaves, the Worker verifies the key it just minted** against the public half. A key that fails
self-verification is not issued — that failure is invisible from the customer's side (they simply cannot
activate) and unrecoverable afterwards.

**If mail is not configured, the key is shown to you for manual delivery** — never silently dropped. The
key exists whether or not the mail went.

---

## Trials

`trial_domains` holds one row per company domain.

⛔ **It stops accidents, not adversaries.** It keys on the email domain; a subdomain, a look-alike or a
second corporate domain defeats it completely. **The real control at today's volume is that a human reads
every request** — and that is true only while the volume is small.

⚠ **When request volume outgrows individual review, this control is unimplemented and the table is still
here looking like it works.** Re-read `docs/S12.4-issuance-decisions.md` D2; the recorded upgrade is
DNS-TXT domain-ownership proof, reusing the S2.5 domain-capture verifier.

A negative trial verdict **never refuses at the form.** It is recorded and shown to the reviewer with its
reason, because a legitimate second trial is a conversation — and because telling a requester "your domain
already trialled" teaches them to try another domain.

---

## ⛔ Payments are not here, and the seam is a paragraph

`docs/S12.4-issuance-decisions.md` §4 describes **where** a provider webhook would arrive and **what it
would trigger** — mark the request paid, and nothing else. The human still signs.

**There is no handler, no route and no schema for it.** A webhook handler with no provider is dormant
machinery, and this repo has already reduced that class once by removal. If you are adding a table "ready
for payments", you have left the seam and started the machinery.

⚠ The provider question is **held**: Lemon Squeezy is a Merchant of Record, but two pages are unread — is
India a supported seller country, and do payouts reach an Indian bank account. And taking payment in India
generally requires a registered entity, which a Merchant of Record may *move* but not obviously *remove*.
**Entity formation gates this and four other things.**
