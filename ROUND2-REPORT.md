# Round-2 Walk — Report (Part A scripted + A5 live; Part B checklist for Pawan)

Run 2026-07-08 against a REAL, UNSEEDED open-edition compose stack (no mocks except one
deliberate network-abort for A1's failure path). Scripted walk: `e2e/tests/round2-walk.spec.ts`
(gated on `ROUND2=1`; never runs in `make e2e`). A5 executed live outside the spec: real compose
agent enrollment + real WireGuard traffic. **Part B (B2–B6) requires the real Entra tenant +
DNS — human-only, checklist below.** B1 browser-level is blocked on the known enterprise-e2e-stack
gap (ledgered, EPIC 7 trigger).

## Step results

| Step | Expected | Observed | Severity | Feeds |
|---|---|---|---|---|
| A1 | signup → verify-pending; resend honest | As specced. Signup does NOT auto-login (202 + mail); after login → `/verify-pending`. Resend under network abort shows "Couldn't send", retry stays; real resend → "Sent". Mail lands in Mailpit. | ok | — |
| A2 | verify link → create-org; record session behavior | Link verifies. **Does NOT establish a session** — fresh browser bounces `/dashboard`→`/login`; re-login required, then → `/create-org`. | ok (recorded) | S5.1-D3 |
| A3 | sane slugs, never stuck-disabled | `Acme Corp-`→`acme-corp` (trailing-hyphen regression clean). `Ächme  Spaces`→`chme-spaces` (umlaut DROPPED, no transliteration). Emoji-only name → empty slug → submit disabled until a manual slug is typed (un-sticks; not stuck). Create → first dashboard. | friction (cosmetic ×2) | UX-backlog |
| A4 | empty state → ceremony; no route back; fingerprint in audit | Empty state + "Enroll a gateway →" work. Ceremony correct: amber, shown-once, ack gate, reload does NOT resurrect. Audit `node.token_issued` row is raw-token-free BUT carries `{"node_name":"walk-gw"}` **only — NO token fingerprint** (convention says keyed fingerprint in logs/audit; issuance→redemption not correlatable). | friction/gap | bugfix |
| A5 | real enrollment; bad-token refused; traffic; live status | **FULL LOOP LIVE**: token → agent enrolled (mTLS cert, `agent_ready`) → node active in API → device `10.99.0.2` → `wg-quick up` in a client container → **handshake + 3/3 pings to 10.99.0.1** → `online:true`, real `last_handshake_at`, overview `nodes:1 devices:1 online:1`. Stale token: 401 `invalid_join_token`. **Reused token: 401 (single-use proven live).** Name-pinned token vs agent default hostname: 400 `node_name_mismatch` retry-loop — see friction F1/F2. | ok + 2 frictions | S5.1-D2 |
| A6 | second signup → invitation card, no create control | REAL `org_limit_reached` 403 → "Invitation required"; form, fields, and submit all gone. Reactive-403 amendment confirmed in real life. | ok | — |
| A7 | manual `/create-org` with an org → re-route to dashboard | **Visit-time re-route does NOT exist** — the form renders. Submit path re-routes correctly (403 → membership re-check → dashboard, card never shown). Harmless but diverges from the walk's expectation. | friction | bugfix (small) |
| A8 | logout; reset revokes all sessions; record TTL | Reset revokes the OTHER session (proven: browser-2 bounced). Cookie: `tunnex_session`, httpOnly, SameSite=Lax, secure=false (dev), **TTL 30 days**. Logout ok. **BUG B1 found at the re-login step (below).** | **bug** | S5.1-D3 + bugfix |

## Bugs

**B1 — stale-cookie CSRF lockout on login (A8; severity: high UX).**
`csrfGuard` (`apps/api/internal/http/session.go:59`) requires `X-Tunnex-CSRF` whenever the request
*carries the session cookie* — presence, not validity. The SPA's pre-auth POSTs (login, signup,
forgot/reset, verify) don't send the header. So a browser holding a REVOKED session cookie (any
password reset, incl. the user's own; any future session revocation) gets
`403 missing X-Tunnex-CSRF header on a state-changing request` on EVERY login attempt until cookies
are manually cleared — with a 30-day cookie TTL. Reproduced deterministically; isolated fresh
browser (no cookie) logs in fine, which is why nothing caught it before.
**Proposed fix (mechanism-level):** attach `X-Tunnex-CSRF` to ALL unsafe-method requests inside
`createTunnexClient` (packages/shared) — the header is presence-only, harmless pre-auth, and this
removes every per-page `headers: CSRF` hand-plumb (no page can ever forget it again). One-line
alternative rejected: exempting login server-side would reopen login-CSRF.

## Friction (non-blocking)

- **F1 (A5):** naming a gateway pins the join token to that name, but the ceremony modal shows only
  `TUNNEX_JOIN_TOKEN=…` — agent then loops `node_name_mismatch` (its default name = hostname).
  Modal should emit the full line: `TUNNEX_JOIN_TOKEN=… TUNNEX_NODE_NAME=<name>`.
- **F2 (A5):** `docker-compose.yml` doesn't plumb `TUNNEX_NODE_NAME` — a pinned token can't be
  redeemed by the compose agent without editing the file.
- **F3 (A4):** `node.token_issued` audit metadata lacks the token's keyed fingerprint (convention:
  fingerprint in logs/audit). Add `Sealer.Fingerprint(token)` to issuance + enrollment audit rows so
  issue→redeem correlates.
- **F4 (A7):** no visit-time membership re-route on `/create-org` (submit-path only). Small guard in
  CreateOrg (or RequireNoOrg wrapper) closes it.
- **F5 (A3):** no transliteration (`Ä`→dropped, not `a`); emoji-only names require a manual slug.
  Cosmetic.
- **F6 (A2):** verify link doesn't log you in — fine security posture, but the "verified" page could
  say "now sign in" more loudly; observed as a mild dead-spot.

## S5.1 decide-items — evidence so far

- **D1 (device-code vs localhost callback): UNDECIDED — needs B3.** Local auth is a pure JSON POST
  (no redirect dance), so a localhost callback trivially works for local accounts. The decider is
  Entra's redirect/MFA/conditional-access behavior against `127.0.0.1:<port>` — only the real-tenant
  walk (B3) answers that.
- **D2 (config storage + perms): strong signal from A5.** The config is served EXACTLY ONCE at
  device creation and is never re-fetchable — so the CLI cannot "fetch config for a device"; it must
  OWN device creation and write the file atomically at that moment. Target `0600`, under a CLI state
  dir (`~/.config/tunnex/` or `/etc/wireguard/`). The browser flow drops a plaintext key in
  `~/Downloads` with default perms — exactly what the CLI must not do.
- **D3 (CLI auth vs cookie sessions): cookies are wrong for the CLI.** Observed: 30-day httpOnly
  SameSite=Lax cookie; CSRF header required on every unsafe method; password reset revokes all
  sessions; email tokens are single-purpose (verify link ≠ session). Nothing token-shaped exists yet
  (no PAT/API keys). Bug B1 is a direct artifact of cookie+CSRF coupling — a CLI riding cookies
  inherits all of it. Implied shape: a dedicated long-lived CLI credential obtained via
  browser/device-code exchange, stored 0600, sent as a header (no CSRF dance). Final D1/D3 wording
  waits on B3.

## Part B — remaining human walk (real tenant)

B1 fingerprint-after-reload (needs enterprise build — blocked on the enterprise-e2e-stack gap, or
run a one-off `-tags enterprise` image) · B2 DNS-TXT + public-domain refusal · B3 JIT first login
(**record every redirect hop — this decides D1**) · B4 account linking · B5 unverified-email SSO
(or explicit N/A) · B6 member-role empty dashboard (UX-backlog).

## Disposition

Uncommitted: this report + `e2e/tests/round2-walk.spec.ts` (ROUND2-gated). Stack left running with
the walk org (`w1@walk.local` / `walk-password-round2-NEW`). Bug B1 + frictions await your call —
none block S5.1's un-hold except the B-walk itself.
