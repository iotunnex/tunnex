# Tunnex — the product walkthrough

**What this is.** The document you walk the product with, in the order a customer meets it. Every step has
three parts: what you click, what actually happens underneath, and **why a customer would do it at all**.

**How it was written.** Every claim below is from the code or from the running product on 2026-08-07, and
each section says which. Where a step could not be verified without running it, it is marked
**⚠ FOUNDER MUST TRY** rather than asserted.

⛔ **AND THE GAPS ARE NAMED AT THE STEP THEY BELONG TO.** A walkthrough that reads as complete over a hole
is worse than one that says "this part is not built" — because the first kind gets demoed.

---

## 0. THE BLOCKING QUESTION: does the one-command install work today?

### ⛔ NO. A customer who runs the advertised command in the next hour gets nothing.

**Measured 2026-08-07, from the live internet:**

| what | result |
|---|---|
| `tunnex.io` resolves | ✅ `172.67.182.52`, `104.21.83.220` (Cloudflare) |
| `tunnex.io/download/` | ✅ HTTP 200, live, advertising the command |
| **`dl.tunnex.io` resolves** | ⛔ **NXDOMAIN — no records at all** (checked against `1.1.1.1`) |
| `https://dl.tunnex.io/latest/install.sh` | ⛔ `curl: (6) Could not resolve host` |

The download page (`tunnex-web/src/pages/download.astro:205`) tells a customer to run:

```
curl -fsSLO https://dl.tunnex.io/latest/install.sh
curl -fsSLO https://dl.tunnex.io/latest/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
less install.sh && sh install.sh
```

**Every one of those four lines fails at DNS.** The host has never existed. This is the first thing a
customer does and it is the first thing that breaks.

### ⭐ But the script itself is fine — it is served from somewhere else

`deploy/install.sh`'s own header advertises a **different** URL, and that one works:

```bash
curl -fsSL https://raw.githubusercontent.com/iotunnex/tunnex/main/deploy/install.sh | sh
```

Measured: **HTTP 200**. So the product has two advertised install paths, they disagree, and the one on the
website is dead.

### What the working path actually does — verified by reading `deploy/install.sh` (184 lines)

1. **Refuses to guess your address.** Prompts for the public DNS name or IP, and *refuses loopback at the
   source* (`addr_ok`). Email links and the WireGuard endpoint both derive from it, so a wrong value here
   produces a deployment that looks healthy and is unreachable.
2. **Pins a real release, never `:latest`.** Resolves the newest GitHub release tag —
   **`v0.3.0-rc5`** as of today — so the install is reproducible and revertible.
3. **Downloads `deploy/tunnex.yml` at that tag.** Verified reachable: HTTP 200.
4. ⭐ **No Mailpit.** Measured: `grep -c mailpit` on the released `tunnex.yml` returns **0**. The compose
   file sets `TUNNEX_ENV: production` and `SMTP_HOST: ${SMTP_HOST:-}` with **no default** — so an
   unconfigured deployment is loud rather than silently pointed at a dev mail catcher.
5. **Asks for SMTP or lets you skip it**, writing `.env`.
6. **Reuses the existing DB password on a re-run** — idempotent; a fresh one would not match the volume.
7. `docker compose -f tunnex.yml pull && up -d --wait`.

### ⛔ Two things the customer is not told, and one of them is a security window

**(a) The first-run administrator credential is printed where nobody looks.**

`bootstrap.EnsureAdmin` prints a framed banner — email, password, *shown once, stored only as an argon2id
hash, cannot be reprinted* — to the **API container's stdout**. The installer runs
`docker compose up -d` (detached), so **that banner scrolls into a log the operator was never told to
read.** The code comment says the banner exists precisely because a JSON log line was invisible; detaching
put it back out of sight.

⚠ **FOUNDER MUST TRY:** run the installer on a clean VPS and confirm whether you can find the credential
without being told to run `docker compose -f tunnex.yml logs api | grep -A5 "FIRST RUN"`.

**(b) Signup is OPEN on a fresh box, and the installer points at it.**

The installer's step 2 says *"Sign up — you will be guided to create your first organization."* That
**works**, and I checked rather than assumed: `signup_closed` is gated on
`SetupComplete = CountOrganizationsEver > 0` (`auth_handlers.go:48`), which is **zero** on a fresh install.
`checkMayCreateOrg` likewise returns nil while `ever == 0`.

⛔ **So between `docker compose up` and the operator's first login, an internet-reachable Tunnex will accept
a signup from anyone, and that person can create the first organization and own the deployment.** The
bootstrap admin exists to close exactly this window; signup being open in parallel defeats it. The window
shuts the instant the first org exists.

⚠ **AND `/api/v1/auth/signup` HAS NO RATE LIMIT.** This is recorded in the code itself
(`tenancy/service.go`, the note beside `checkMayCreateOrg`): the only throttle in the router is scoped to
the agent re-key path. Signup used to end in an organization and collide with the org ceiling; it now ends
in a bare account and nothing bounds it — unlimited `users` rows, one verification email per attempt.

### ⭐ What a customer gets if they run it in the next hour — plainly

- **From the website's command:** nothing. DNS failure on line one.
- **From the GitHub raw command:** a working, pinned, production-configured Tunnex on a public address,
  with no mail unless they configured SMTP — and a race between them and the internet for who becomes the
  first administrator.

**Fix before any customer touches this, in order:**
1. Point `dl.tunnex.io` at the artifacts, **or** change the website to the `raw.githubusercontent.com` URL
   that already works.
2. Make the installer print the first-run credential itself (it can read the container's stdout after
   `up -d`), or tell the operator the exact command to retrieve it.
3. Decide whether signup should be open at all on a box that has a bootstrap admin. Two admission paths on
   a fresh, public deployment is one more than the design intends.

---

## 1. First login as the control-plane administrator

**Click.** Open `http://<your-address>/`. Sign in with `admin@tunnex.local` and the password from the
first-run banner.

**What happens.** `EnsureAdmin` ran once at first boot, keyed on *"has this deployment ever had a user"*
counting soft-deleted rows — so a restart is not a security event and deleting every account cannot reopen
admin minting. The password was generated, hashed with argon2id, and the plaintext exists only in that
banner. The account carries `users.cp_admin`, which is the capability to create organizations after the
bootstrap window closes.

**Why a customer does this.** It is the only way in that does not race the internet. Every other path —
signup, invitation, SSO — either closes or depends on someone already being inside.

⛔ **There is no recovery.** Lose the credential before signing in and the documented answer is
`docker compose down -v` — destroy the deployment and start again. The banner says so.

---

## 2. Forced password change

**Click.** You are sent straight to a change-password screen. You cannot navigate away.

**What happens.** The bootstrap password stops working the moment you set your own. The wall is a real
route guard, not a suggestion.

**Why.** The first credential was printed to a terminal and possibly a log aggregator. Its life should be
measured in minutes.

⚠ **FOUNDER MUST TRY:** confirm the forced-change screen is what you actually land on, and that the old
password is refused afterwards.

---

## 3. Create the first organization

**Click.** You are guided to `/create-org`. Name it.

**What happens.** `checkMayCreateOrg` allows it because `CountOrganizationsEver == 0` (bootstrap) — or,
later, because you hold `cp_admin`. Creating it **closes signup permanently** for this deployment:
`SetupComplete` flips and `/auth/signup` starts answering `403 signup_closed` with a human reason
("accounts are created by invitation — ask an administrator to invite you").

**Why a customer does this.** The organization is the tenancy boundary. Every device, gateway, policy,
site and audit row is scoped to it.

⭐ **Note the self-closing design:** no setting, nothing to configure wrong, nothing to remember to turn
on. The condition that opens the window is destroyed by using it.

⚠ **A member of org A cannot create org B.** Authority in the identity model is `map[orgID]role` —
org-keyed by construction — so only `cp_admin` licenses creating a *new* organization. Everyone else gets
a 403 with the reason, not a 404, so they are told invitation is how they get in.

---

## 4. Invite your first user

**Click.** Users & Roles → *Invite by email* → address, role, **Send invite**.

**What happens.**
- A row is created and an audit event written **in one transaction**.
- The email is sent best-effort. **The invitation survives a delivery failure** — the row is real and the
  link is valid, so mail being down does not destroy the one thing you can still hand over another way.
- The API answers **202** with `invite_token` (the raw accept link) and `delivered: true|false`.
- On failure the message reads: *"Invitation created — BUT THE EMAIL COULD NOT BE SENT. Copy the link below
  and send it to them yourself."*
- The screen shows a one-time modal with the copyable accept link regardless.

**Why.** Invitations are the **only** way anyone joins after setup. Signup is closed, and a bare account
with no membership lands nowhere.

### What the invitation email looks like (verified on the wire, 2026-08-07)

Branded: dark card, Tunnex wordmark, an **Accept the invitation** button, the URL in full beside it, and
the footer *"Tunnex · Connect everything. Trust nothing."*

⭐ **The logo is embedded in the message (`cid:`), never fetched.** Measured with a third-party MIME parser:
`multipart/related [ multipart/alternative [ text/plain, text/html ], image/png ]`, and the decoded PNG is
sha256-identical to source. **Nothing is requested when the mail opens** — no phone-home to us, no
open-tracking hit in your own logs, and it renders in clients that block remote images.

**Every message carries a working plaintext body** with the link in full — for screen readers, text
clients, and anyone who does not trust a button in an email.

### ⛔ Named gaps at this step

- **The screen cannot tell you whether mail was sent.** The 202 carries `delivered`, and the web UI does
  not read it — the link modal renders identically on success and failure. Use the log
  (`email_accepted_by_provider` vs `invite_email_failed`).
- **Resend returns no token.** `ResendInvitation` answers with a message only, while telling you to "copy
  the link from the invitations list". On a box with no SMTP, a resent invitation has no recoverable link.
- **Header injection is unfixed.** `buildRFC822` interpolates `To` and `Subject` into headers with no CRLF
  stripping — CodeQL's `go/email-injection`, pre-existing, and more reachable now that SMTP is on.

### Proving mail actually leaves

```bash
docker compose logs -f api | grep -E "mail_destination|email_accepted_by_provider|invite_email_failed"
```

- At boot, `mail_destination` states where mail goes — `mail.spacemail.com:587`, or that mail is disabled
  and which variable fixes it.
- `email_accepted_by_provider` = the server **accepted** the message. That is not inbox delivery; SPF/DKIM
  and the recipient's filters act afterwards, and the provider's outbound log is the authority.
- `invite_email_failed` carries the provider's verbatim error.

⚠ **Port 587, not 465.** Go's `net/smtp` dials plaintext and upgrades via STARTTLS; it has **no
implicit-TLS path**, so an SMTPS port hangs or errors. This is a property of the standard library and
cannot be fixed by configuration.

⚠ **`MAIL_DEV_LOG` must never be set on a deployment** — it tees message bodies, and those bodies are
links that work.

---

## 5. The invited user sets their own password and signs in

**Click.** They open the link → `/accept-invite?token=…` → fill *Your name* and *Password* → **Accept
invitation** → see **"You're in"** → go to `/login` → sign in.

**What happens.** The accept endpoint is `security: []` (public) and **mints the account itself** — being
invited *is* the admission, and the account is only the credential. It also marks the email verified,
because arriving via a link sent to that address is the proof. The token is single-use and expires. The
page strips the token from the URL. Accepting deliberately does **not** mint a session, because the link
is admin-visible.

**Why.** The person sets their own password; nobody ever handles it. And an admin who copied the link out
of the modal cannot ride it into the account.

### ⛔ The assertion that matters

After signing in they must land on **Overview**, at a URL ending `/dashboard`.

**Not** the "Invitation required" card. **Not** `/create-org`. That pair is the failure this whole chain
was rebuilt around, and `e2e/tests/invitation.spec.ts` now drives a real invitation end to end and asserts
against exactly it — no mocks, token taken from the 202 body.

⚠ **FOUNDER MUST TRY, and this is the leg that has never run with real mail.** Delivery is proven
(a real invitation reached a Gmail inbox from `support@tunnex.io` via Spacemail on 2026-08-07). The
accept → sign-in → lands-in-org legs have only been proven by the e2e spec against a local stack.

**Telling a mail problem from a routing problem:**
- Log clean, nothing arrives → mail. Check spam, then the provider's outbound log.
- Mail arrives with a **wrong host** in the link → `APP_BASE_URL`. The link is built **server-side** from
  it, not from the browser.
- Link works, sign-in works, and they land on "Invitation required" → neither. That is the loop.

---

# ⛔ WHERE THIS DOCUMENT STOPS, AND WHY

Everything above is verified against the code and, where stated, against the running product today.

**The remaining sections are not written, and I am not going to fake them.** The brief asks for
feature-by-feature coverage of: SSO and directory sync · domain capture · MFA · gateway enrolment · devices,
QR, desktop client · full-tunnel and kill-switch · routed ranges · site-to-site, hub-and-spoke, cross-site
DNS, HA failover · Zero Trust policies, groups, resources, port-scoped rules, time-boxed grants, posture,
device approval · AI agents · the Kubernetes operator and CRDs · OpenVPN · Access Events, Audit Log,
Prometheus · licensing bands and grace · gateway transfer/revoke/delete · backup and restore.

Each of those needs the same treatment the sections above got — the exact control read out of the web
source, the control-plane path read out of the handlers and queries, and the data-plane effect read out of
the agent. **That is roughly fifteen more sections at this depth, and I do not have the working context
left in this session to do it from the code rather than from memory.**

⛔ **Writing them from `PLAN.md` or from recall is exactly what the brief forbids**, and it is the failure
mode this repo has a law about: a document that reads as complete over a gap gets demoed, and the gap is
found by a customer.

**What I know about those areas from this session's measurements, offered as pointers rather than as
walkthrough sections:**

- **Gateway revoke cascades to every device homed on it**, in the same transaction, permanently — and as of
  S12.12 the revoke now **refuses** while any device is homed there, offering a transfer step instead.
  Transfer moves `active` and `pending` devices to another gateway, keeps their addresses (the pool is
  org-scoped), and reports **per device** whether its config must be re-imported.
- **Cross-site transfer changes which policy rules apply** — site-scoped policy is evaluated against the
  device's gateway's site. The transfer surface states it.
- **Gateway delete works only on a revoked gateway**, and deletes its enrolment token with it.
- **Rename works; endpoint edit does not** — there is no snapshot of the endpoint a config was issued
  against, so the change would be invisible to `needs_reexport`. Registered as **S12.12b**.
- **The gateway ceiling notice on the Gateways page pairs an org-scoped count with a deployment-scoped
  ceiling.** On a box with 127 live gateways and a ceiling of 2 it renders the *at-ceiling* sentence, so
  the over-ceiling branch — the one that exists to say "revoking one will not free a slot" — can never
  fire. **Unfixed, held for disposition.**

**To finish this document:** run it as a fresh session per block, in the order the brief lists, with the
same rule — every claim from the code or the running product, and every gap named at its own step.

---

## Appendix — commands used to verify the install section

```bash
dig +short @1.1.1.1 dl.tunnex.io                    # NXDOMAIN
dig +short @1.1.1.1 tunnex.io                       # 172.67.182.52 104.21.83.220
curl -sS -o /dev/null -w '%{http_code}' https://tunnex.io/download/          # 200
curl -sS -o /dev/null -w '%{http_code}' \
  https://raw.githubusercontent.com/iotunnex/tunnex/main/deploy/install.sh   # 200
curl -fsSL https://api.github.com/repos/iotunnex/tunnex/releases/latest \
  | grep -m1 tag_name                               # v0.3.0-rc5
curl -sS https://raw.githubusercontent.com/iotunnex/tunnex/v0.3.0-rc5/deploy/tunnex.yml \
  | grep -c mailpit                                 # 0
```
