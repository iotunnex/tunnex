# Tunnex.io — Product Build Plan (Story-Driven)

## Context

Tunnex.io is a self-hosted, multi-tenant VPN & Zero Trust access platform — a modern, open alternative to Pritunl. It manages WireGuard (and later OpenVPN), supports SSO (Google + Microsoft) alongside manual user creation, and ships its own desktop client (CLI first, then Electron) for Windows and macOS. The entire stack must come up with a single `docker compose up` (and down cleanly), auto-generating all required secrets/keys/config on first boot.

This plan defines **every story** up front. We then build **one story at a time**: implement → review → merge → next. Each story is independently shippable and testable. **Story numbers match their epic** (E3 → S3.1, S3.2, …) for clean branch names and cross-session continuity.

### Locked Decisions
- **Backend:** Go (chi router, `sqlc` for typed queries, PostgreSQL, Redis for sessions/cache)
- **Frontend:** React + Vite SPA + TypeScript + Tailwind — same bundle reused by the Electron renderer
- **Tenant routing:** Single domain (`app.tunnex.io`), org resolved from membership after login
- **Auth:** OIDC (Google + Microsoft Entra ID) + local users (argon2id); cookie sessions in Redis
- **Control/data plane:** API is the **control plane**; a **`tunnex-node` agent** owns the **data plane** (WireGuard/OpenVPN). The API NEVER calls `wgctrl` directly — it talks to an agent, which in the compose quickstart runs on the same host.
- **API contract:** **OpenAPI-first.** Spec is the source of truth; generate the TS client (`packages/shared`) and validate Go handlers against it — no hand-synced types.
- **VPN control:** `wgctrl-go` inside the node agent for WireGuard; OpenVPN via the node agent (later)
- **Deployment:** Self-hosted only. `docker compose` orchestrates postgres, redis, api, web, nginx, node-agent (+ Mailpit in dev)
- **K8s:** Helm chart + CRD-based operator — operator reconciliation reuses the agent's reconcile loop
- **Client:** CLI first (`tunnex` binary), then Electron (Windows + macOS)
- **Edition:** **Open-core** (see Edition Model section)
- **Repo:** Monorepo — `apps/api` (Go), `apps/node` (Go agent), `apps/cli` (Go), `apps/web` (React), `apps/client` (Electron), `packages/shared` (generated TS types), `deploy/` (docker, helm, operator)

### Cross-Cutting Principles (apply to every story)
- **Identity ↔ credential binding:** a device/peer credential is only ever valid for its owning user's identity. No floating credentials.
- **Revocation is a full sweep:** revoking a credential releases *everything it ever claimed* — its peer slot (removed from the gateway), its pool address (freed for reuse), and its live telemetry (cleared, so it can't report stale "online"). Established for WireGuard devices in S3.3/S3.5/S3.6; **EPIC 9's OpenVPN devices must apply the identical sweep** (cert/CRL revocation + address release + status clear), not just cert revocation.
- **Desired-state reconciliation:** data-plane state (WG interface) is continuously reconciled against control-plane desired state — never assumed in sync. Same pattern powers the K8s operator.
- **Structured logging + request IDs from day one** (S0.1 DoD), not retrofitted at the end.
- **Secrets encrypted at rest** under a bootstrap master key (S0.3); per-org IdP client secrets are never plaintext.

### Build Protocol (per story)
1. Implement the story on its own branch/commit.
2. Self-review + run `/code-review`; run tests; verify end-to-end.
3. Report outcome, get sign-off, then start the next story.

**Where a commit lives:** product code ALWAYS on the story branch (the sign-off/merge
gate depends on branch isolation). A process/docs correction whose value is *immediate*
(e.g. fixing this re-entry checkpoint) lands on `main` directly — a fix that only helps
pre-merge sessions is useless stuck on an unmerged branch. When main advances this way,
rebase the active story branch onto it to keep the ff-merge clean.

**Merge instructions are session-bound:** a merge instruction executes in the session that
receives it, or is RE-CONFIRMED at re-entry — a sign-off read out of a summary/handoff is not
authorization to merge. (Codified after S4.8's merge waited on an explicit re-confirmation.)

---

## Story status (re-entry checkpoint)
**Update this on every merge (one line) — a stale pointer re-enters a fresh session in the wrong epic.**
Current: **EPIC 6 OPEN — S6.1 (client shell) next. Ops CLOCK RUNNING: signing applications (Apple
Dev ID + Windows EV) must be filed this week (Pawan) — S6.5 hard-blocked otherwise. S3.7 parked at
paper. Beta milestone deferred, not rejected — re-decide at EPIC 6 close.**
Ledgered: CLI-code GC → S11, rate limits → S11.3, user-scoped credential surface → security review /
CLI-sessions panel; S3.7 gateway-NAT parked (trigger = EPIC 6 close or beta).
Done through (merged to `main`): **EPIC 0–2, EPIC 3 (S3.1–S3.6), EPIC 4 COMPLETE — S4.1 (shell) ·
S4.2 (auth) · S4.3 (dashboard) · S4.4 (users & roles) · S4.5 (org settings + SSO) · S4.5b (CIDR
resize) · S4.6 (audit viewer) · S4.7 (onboarding funnel) · S4.8 (Round-2 walk fixes) · EPIC 5 / S5.1
(tunnex CLI).** Current epic: EPIC 6 (Electron desktop client), S6.1 next. If this pointer disagrees
with the handoff doc / git log, TRUST GIT (`git log --oneline -15`) and update this line.

## Armed Guards (living inventory — "what protects us")
Each has been demonstrated to *fail* on a real violation during its story's DoD.
Seed for the eventual SECURITY.md.
- **Query-lint / org_id** (`db/querylint_test.go`) — tenant-owned-by-default (tables derived from migrations, `globalTables` allowlist); every tenant table query must scope by `org_id`.
- **Query-lint / deleted_at** — soft-delete tables must filter `deleted_at IS NULL`.
- **Trigger schema check** (`db/schema_test.go`) — every `updated_at` table has the `set_updated_at` trigger.
- **audit_logs append-only** — DB triggers reject UPDATE/DELETE/TRUNCATE; actor FK to `users` enforces attribution.
- **Codegen drift guard** (`make generate-check`) — spec/generated code can't diverge.
- **Edition build+test** (`make build-editions` / `test-editions`) — open and enterprise builds both compiled & tested; neither rots.
- **e2e correlation** (Playwright) — SPA→API `X-Request-Id` chain asserted end-to-end.
- **RBAC matrix** (`rbac_test.go`) — executable privilege-escalation spec.
- **Restart-persistence + fail-loud secrets** (S0.3) — master key never silently regenerates.

## Edition Model — Open-core (resolved)
- **Schema is multi-tenant in core.** Everything carries `org_id`; the open edition simply **does not expose creating a second org** — an API/UI limit, not a schema fork. No migration or code move later.
- **Enterprise features** (gated behind an `internal/enterprise/**` package + build tag): SSO (Google/Microsoft), Zero Trust policies, Kubernetes operator, and the multi-org limit-lift.
- **The enterprise boundary is established in S1.1**, because the first gated decision (org-creation limit) lives there — not at SSO. SSO/policies/operator plug into the same boundary as they arrive.

---

## EPIC 0 — Foundation & Scaffolding

- **S0.1 Monorepo scaffold** — layout, `pnpm` workspace, `go.mod`, Make/Turbo targets, linting, README. **DoD: structured logging (slog) + request-ID middleware + `/healthz` that logs with correlation IDs.**
- **S0.2 Docker Compose one-command boot** — postgres + redis + api + web + nginx + node-agent + Mailpit; `.env.example`; **healthchecks on every service**; `make up`/`make down`. **Non-web bits:** node-agent needs `cap_add: NET_ADMIN` and the **WG UDP port published**. **Non-root:** api (uid 10001) + web/nginx (nginx-unprivileged, uid 101) run non-root; only node-agent stays privileged for WireGuard.
- **S0.3 First-boot bootstrap, secrets & mailer** — entrypoint auto-generates JWT/session secrets, DB creds, WG server keys, and a **master encryption key** if absent; persists to a volume; idempotent. Sensitive per-org data (IdP secrets) stored **DB-encrypted (AES-GCM) under the master key**. **Pluggable mailer:** SMTP env vars for prod; **dev fallback = Mailpit** (compose) + log the link. **DoD: restart-persistence test** — `up → down (no -v) → up` reuses volumes, secrets are stable across restarts, and all services return healthy (foundation already proven for volumes in S0.2; extend to secrets here).
- **S0.4 DB migrations & tooling** — `golang-migrate`, `sqlc`, `make migrate`.
- **S0.5 OpenAPI contract + codegen** — author the OpenAPI spec; generate the TS client into `packages/shared`; wire request/response validation on the Go side. Source of truth for all later endpoints. **Cleanup:** the S0.1 placeholder `/api/v1/ping` and the hand-written `HealthResponse` in `packages/shared/src/index.ts` must be folded into the spec (as `/healthz`) or removed — no hand-maintained types survive S0.5 (avoid spec drift).
- **S0.6 Seed data + e2e test harness** — `make seed` (demo org/user); Playwright (web) + `httptest` (API) skeletons so every later story's "verify end-to-end" has rails. **DoD: seed + e2e run green on the open build using local auth only** (no enterprise/SSO dependency), so the open edition is fully testable end-to-end. **Schema guard:** add a CI check that every table with an `updated_at` column has the `set_updated_at` trigger bound (one query joining `information_schema.columns` + `pg_trigger`) — enforces the convention that policy alone can't.

## EPIC 1 — Multi-Tenancy Core

- **S1.1 Data model + enterprise boundary** — `organizations`, `users`, `memberships`, `invitations`, `audit_logs`; org-id row scoping. **Establish `internal/enterprise/**` + build tag here; open build enforces the single-org-creation limit.**
- **S1.2 Org lifecycle** — create org, settings, slug/domain, soft-delete.
- **S1.3 Tenant context middleware** — resolve current org from session membership; enforce isolation on every query.
- **S1.4 RBAC** — roles (owner, admin, member) + permission-check middleware.

## EPIC 2 — Authentication (Google + Microsoft + Local)

- **S2.1 Local auth** — signup/login, argon2id, **email verification + password reset (uses S0.3 mailer)**. DONE. **Decisions:** unverified users MAY log in; email-verification gates org-*mutating* actions (enforce once the principal carries verified state, S2.2). No account enumeration (generic signup/reset responses; generic login error + dummy-verify timing). Tokens hashed/purpose-bound/single-use/expiring.
- **S2.2 Session management** — Redis-backed cookie sessions, CSRF, logout, refresh. **Carries the S1.3 + S2.1 handoffs:** supply the real session-backed `AuthFunc` (resolves session → `authctx.Principal` with memberships + verified state); spec-driven test asserting every mutation endpoint returns 401 without a session (walks the OpenAPI paths); populate `audit_logs.actor_user_id` for authenticated mutations (NULL = system only); gate the org collection/create endpoints; **wire login to establish a session cookie**; **password reset must revoke all of the user's existing sessions**; enforce verified-gating on org-mutating actions.
- **S2.3 Google OIDC** *(enterprise)* — login + account linking; per-org SSO config (secret encrypted at rest).
- **S2.4 Microsoft Entra OIDC** *(enterprise)* — login + account linking; multi-tenant Azure app; secret encrypted.
- **S2.5 SSO provisioning & domain capture** *(enterprise, security-sensitive — extra review)* — JIT user creation + role mapping. Require **DNS-TXT-verified domain ownership**; **block public domains** (gmail.com, etc.); **domain capture is globally unique** (two orgs cannot capture the same domain); never auto-join on unverified email.
- **S2.6 Manual user management** — admin invites/creates users, resend/revoke invites, deactivate.

## EPIC 3 — WireGuard Core Loop (proves the product — before full dashboard)

- **S3.1 Node agent + control-plane protocol** — define `tunnex-node`: registration, mTLS/gRPC between API and agent, desired-state push + **reconcile loop** (agent compares desired vs. actual `wgctrl` state on an interval; heals drift). **Agent enrollment:** a one-time **join token** (generated in dashboard / compose bootstrap) is exchanged for the agent's mTLS client cert on first connect. **Revocation latency spec:** control plane **pushes** revocations (agent applies in **<5s**); interval reconcile is the safety net, not the primary path.
- **S3.2 WG server lifecycle** — interface up/down via agent, key mgmt, listen port, address pool (CIDR) per org. **DELIVERED** (node-generates key, real wgctrl adapter dirty-checked, reconciled interface, `wg show` e2e). **Deferred limitation (no owning story — noted here):** node re-key currently requires an agent restart (the WG key file is read once at boot); a running agent won't pick up a deleted/rotated key file. Acceptable for now (re-key is an operator action); revisit as a hardening item (live key-file watch / re-report without restart) if/when key rotation becomes routine.
- **S3.3 Peer/device management** — issue peer config, QR/download, per-user device list, revoke. **Acceptance (identity binding):** a peer config cannot be created/activated except via the owning user's authenticated session; admin-created peers are bound to a named user; revocation immediate per S3.1 latency spec. **Also owns:** peer traffic routing (`ip route` for peer AllowedIPs — S3.2 configures the interface but installs no peer routes; a /32 interface addr has no subnet route, so tunneled traffic won't flow until this lands).
- **S3.4 Client config generation + bare UI page** — `.conf` output (DNS, allowed IPs, keepalive) + minimal download page. **← "Tunnex is real" milestone.**
- **S3.5 IP allocation service** — deterministic, collision-free assignment from org pool. **Acceptance (edge cases):** address **release/reuse** on revocation; safe on **org CIDR resize**; no reassignment of an in-flight address.
- **S3.6 Live connection status** — handshake/last-seen, bytes tx/rx, online peers (data from agent).
- **S3.7 Gateway NAT + forwarding (full-tunnel egress)** — make `--full-tunnel` (`AllowedIPs=0.0.0.0/0`)
  actually reach the internet: the agent enables IP forwarding + source-NAT on the gateway so client
  traffic egresses via the gateway host. Today the config connects but egress dies at the gateway
  (split-tunnel only).
  **PARKED at paper (2026-07-08).** The paper decision below stands, unreviewed-for-build; EPIC 6 was
  chosen over pulling this forward. **Ledger trigger: EPIC 6 close OR the beta milestone, whichever
  comes first** — resume with a decision review, then build. Beta was DEFERRED, not rejected.

### S3.7 paper decision (PARKED — decided on paper; review + build deferred to the trigger above)

Grounded in code: the agent drives WG via `wgctrl` (netlink), holds `NET_ADMIN` + `/dev/net/tun`,
and reports endpoint/wg-key to the control plane (`reportKeyLoop`). Device configs already emit
`0.0.0.0/0` for full-tunnel (`devices/config.go`); nothing sets up forwarding/NAT anywhere in
`apps/node/` — that is the whole gap.

**(1) Privilege posture — NET_ADMIN is enough; the real dependency is the HOST kernel, so DETECT it.**
No `privileged: true`. The agent already has `CAP_NET_ADMIN` in its own netns; `net.ipv4.ip_forward`
is a per-netns sysctl writable with NET_ADMIN, and a source-NAT rule on the container's egress
interface needs NET_ADMIN + the host's `nf_nat`/`nf_conntrack` (and masquerade support) loaded in the
kernel — a capability caps can't grant. So S3.7 does NOT add privileges; it PROBES at boot whether
egress NAT is achievable (ip_forward writable AND a masquerade rule can be added AND conntrack is
present) and degrades gracefully when it isn't (locked-down host).

**(2) iptables vs nftables — nftables, native.** The agent already speaks netlink for WG; it manages
the NAT ruleset the same way (nftables netlink API, or the `nft` binary as a fallback) in its OWN
named table/chain (e.g. `tunnex` table, a `postrouting` masquerade chain scoped to the pool CIDR →
egress iface). Rationale: iptables-legacy is deprecated, iptables-nft is a shim over exactly this,
and a dedicated nft table is atomically replaceable and won't collide with host rules. NO shelling to
`wg-quick` PostUp (the agent owns the interface, not wg-quick). Masquerade is scoped to the org pool
source CIDR — never a blanket rule.

**(3) Per-gateway capability flag + `--full-tunnel` REFUSE (not warn).** The agent reports an
`egress_nat` capability bit (from the probe) up the existing report channel; stored on the `nodes`
row. Creating a device with `full_tunnel=true` against a gateway whose `egress_nat` is false is
REFUSED server-side (typed `gateway_no_egress`) — a full-tunnel config that silently blackholes all
internet is worse than a clear refusal; the UI mirrors it (disable/explain the full-tunnel toggle
for incapable gateways). Split-tunnel is always allowed.

**(4) Desired-state + full-sweep (reuse the cross-cutting principles).** NAT rules are data-plane
state → RECONCILED on the S3.1 interval (the agent re-asserts ip_forward + the masquerade ruleset,
heals a flushed table), never assumed. And revocation is a full sweep: when the gateway is revoked
(or its last full-tunnel peer is gone) the NAT table is torn down — no dangling masquerade.

**(5) Egress e2e (the proof obligation).** A compose "internet" target reachable by the gateway but
NOT directly by the client; a client container with a full-tunnel `.conf` reaches it ONLY through the
tunnel (real WG + real NAT, like the S4.5b race harness is real). Deliberate-red: flush the
masquerade rule → egress fails (proves the rule carries it, not a leak). Negative: a device create
with `full_tunnel=true` against a no-capability gateway → `gateway_no_egress`.

Open sub-questions to settle IN the decision review (not assumed): whether `egress_nat` is a boolean
column or a capabilities JSONB on `nodes` (forward-compat for future gateway caps); whether the probe
runs once at enroll or every reconcile (host state can change); and the exact typed error + whether
the open edition gates full-tunnel at all (it is core/edition-neutral like the allocator — lean
neutral, confirm).

## EPIC 4 — Full Web Dashboard

- **S4.1 App shell & design system** — Tunnex brand (logo assets from user), Tailwind theme, layout, nav, auth-gated routing.
- **S4.2 Login / signup / SSO screens** — all three auth paths.
- **S4.3 Dashboard home** — org overview, members, activity, live connection stats. **Delivered:** single `GET /api/v1/organizations/{orgId}/overview` (counts + audit-log activity slice, LIMIT 10; `/organizations` matches every existing route — `/orgs` was only shorthand). Online tile inherits S3.6 honesty ("Seen in last N min", active-owner filter); `tenancy.OnlineWindow` is the single source of truth for the window; future-handshake upper bound is a data invariant at ingestion, not a per-read predicate.
- **S4.4 Users & roles UI** — list, invite, edit role, deactivate.
- **S4.5 Org settings & SSO config UI** — connect Google/Microsoft, domain-capture rules. **Delivered (org settings + SSO config only; CIDR resize split to its own story):** SSO secret is WRITE-ONLY (GET returns a keyed HMAC fingerprint, never the secret — no `client_secret` field in the response type); config writes are audited (`sso.config_updated`, actor-attributed, secret-free metadata); open builds refuse SSO-config endpoints with 403 `edition_required` (the established precedent, not 404); the client RBAC mirror is now GENERATED from the Go grant table (drift = red build). **Deferred tests (enterprise e2e stack, no owning story — EPIC 7 trigger):** the payload-level "GET has no secret" Playwright assertion and the fingerprint-display Playwright check are blocked because the e2e stack builds the open edition (GET /sso → 403 there). Proven structurally (schema) + by the enterprise `View` unit test, which SUBSTITUTES but does not satisfy the e2e — same discipline as the real-node-enrollment download test.
- **S4.5b CIDR resize** (split from S4.5) — resize the org WG pool. **Delivered:** `PUT /organizations/{orgId}/pool-cidr` (edition-neutral — allocator is core/open); grow-superset / shrink-subset only (else `illegal_resize`), identical CIDR = idempotent 200, `< /30` = `cidr_too_small`; canonical (masked) CIDR stored/audited. Shrink that would strand allocations → structured 409 `{orphan_count, orphans[≤20]{device_id,name,assigned_ip,reason}}`, reason = `out_of_range | reserved_collision` (ipalloc.Orphans, reserved-collision-aware, single-read so check == 409 objects). Check runs UNCONDITIONALLY (check-anyway) — provably empty on a valid grow, a backstop if a non-Allocate writer breaks the invariant. Atomic + audited (`org.cidr_resized`, no row on no-op) under the shared per-org `LockDeviceKey`; `TestResizeAllocationRace` proves the lock excludes a concurrent allocation (red-without-lock demonstrated). **Deferred test (SUBSTITUTES, does not satisfy):** the 409 orphan-list UI render is Playwright-tested against a MOCKED endpoint — a real stranded-device render needs an enrolled gateway the open e2e stack lacks. Trigger: whichever lands first — the enterprise e2e stack (EPIC 7) or Playwright-side node enrollment.
- **S4.6 Audit log viewer** — filterable event stream.
- **S4.7 Fresh-user onboarding** — close the empty-funnel gap: a freshly-verified local user with
  zero orgs currently lands on a dead-end dashboard (no create-org / no gateway-enroll affordance).
  Ship the post-verify router + explicit create-org step + gateway-enroll empty state.

### S4.7 onboarding state machine (COMMIT ONE — decided on paper, before code)
Grounded in code: `auth.Signup` makes user + verify token, **no org / no membership**;
`CreateOrganization` (`handlers.go`) is `requireVerifiedUser`-gated; open-build org cap is
`enterprise.Unlimited{MaxOrganizations:1}` → `org_limit_reached` 403 (`tenancy/service.go`); SSO JIT
`ensureMembership` adds a member-role membership + `member.jit_joined` audit and **never** touches
create-org.

Post-verify, a router branches on the caller's **membership count** (not auth-path):

1. **≥1 membership** → straight to dashboard (skip the funnel entirely).
2. **0 memberships, org-create allowed** → **explicit "Create your organization" step**
   (user names the org; slug auto-derived) → on success, owner membership + dashboard.
3. **0 memberships, cap reached** (open build, second tenant) → **invitation-only dead-end card**,
   NO create control. Server is the truth (`org_limit_reached` 403); the UI only mirrors it.
   **Reached REACTIVELY, not pre-empted** — see the amendment below.

Path carve-outs (must NOT hit the create-org step — they already produce membership):
- **Invite accept** → membership added → dashboard.
- **SSO JIT login** → `ensureMembership` → dashboard.

Decisions locked (the three decide-before-code items):
- **(1) Signup→org shape = EXPLICIT create-org step** (not silent auto-create). One funnel; the
  JIT + invite paths bypass it because they already yield membership; auto-create would fork
  behavior by auth-path and inject a phantom "My Organization". User names their own org.
- **(2) Open-edition second-signup = invitation-only.** The single-org cap is already
  server-enforced; the UI mirrors with the dead-end card, never invents permission. A legal second
  local signup with no org lands on the same card.
- **(3) Verified-email gate = structural, upstream of create-org.** `requireVerifiedUser` already
  refuses unverified create-org; the funnel routes signup→verify BEFORE the create-org step, so the
  refusal is by construction, not a surprise 403. TRACE it in a test (unverified → refusal shown).

**AMENDMENT (build) — cap-reached is REACTIVE-403, not pre-empted.** The paper spec put a
verified/0-membership/cap-reached user straight onto the dead-end card (never seeing a create form).
Amended to: show the create step, and on the server's `org_limit_reached` swap to the invitation
card (create form + all create controls removed). **Rationale:** the cap is GLOBAL and deployment-wide
(`tenancy.CreateOrganization` → `CountOrganizations()` ≥ `MaxOrganizations`, i.e. one live org total).
A verified 0-membership user cannot know the single slot is taken without asking the server, and the
only way to pre-empt client-side would be to reveal that *some org they are not a member of exists* —
a tenant-isolation leak. So the server is the sole authority and the UI reacts to its 403. The end
state still satisfies the spec's intent: the user lands on the invitation card with **no usable
create affordance**. **On the 403 the UI re-checks membership first** (`GET /organizations`): if the
user gained a membership between routing and refusal (invite accepted elsewhere, JIT-join, admin add)
they go to the dashboard; only a still-0-membership user sees the card. Proven end-to-end against the
REAL open build (seed `DemoNoOrgUser`, no mock) in `onboarding.spec.ts`.

Edge-case decisions (one line each, for the record):
- **Soft-deleted-org membership counting:** the funnel counts memberships via the `GET /organizations`
  handler → **`ListOrganizationsForUser`** (`organizations.sql`), which filters `o.deleted_at IS NULL`
  (query-lint deleted_at guard enforces it) — so a user whose only org was soft-deleted counts as 0
  and is routed to create-org, never trapped pointing at a dead org.
- **Deactivated-user routing:** a deactivated user is blocked at login (`account_deactivated` 403,
  `auth/service.go`) → no session → never reaches the funnel; no funnel special-case needed.
- **Invite-accept vs email-verification ordering:** `invites.Accept` **marks the email verified THEN
  upserts the membership in one tx** (token proves inbox control) — so an invitee lands in the shell
  already verified and with ≥1 membership (has-org branch); they never hit create-org or
  verify-pending.
  **Clarification (existing behavior, NOT new in S4.7):** this verify-then-membership ordering
  predates S4.7 (`invites.go` Accept, shipped in S2.6) — S4.7 adds **no** new Go for invite-accept, it
  only relies on the existing flow so the funnel is correct. Audit coverage is likewise pre-existing:
  Accept writes an `invite.accepted` audit row (actor = the invitee) in the same tx. S4.7 introduced
  no new server behavior or audit action here; the only new backend code in the story is the
  `DemoNoOrgUser` seed fixture (commit `6ac1a6b`).

Conventions named: gateway **join token = one-time secret** (S4.5 config-download ceremony — amber
callout, "I've saved it" gate, no route back, keyed fingerprint in logs/audit, never the raw token);
audit rows same-tx, actor-attributed, secret-free; guards auto-arm (401-walk picks up any new gated
op, RBAC matrix, deliberate-red one-line per new guard).

Prove: fresh-org empty-state render set (Playwright, all three router branches); enrollment e2e
(join-token → agent joins → node appears — real compose agent if the harness allows, else mocked
ceremony + a deferred-ledger entry).

- **S4.8 Round-2 walk fixes** — the Part A walk's bug + top frictions (see ROUND2-REPORT.md):
  B1 CSRF stale-cookie login lockout (client-wide header in createTunnexClient); F1+F2 name-pinned
  join-token ceremony line + compose plumb; F3 token fingerprint in issuance/enrollment audit;
  F4 visit-time /create-org re-route; commit the ROUND2-gated walk spec + report.

**UX-backlog (from the Round-2 walk — recorded, NO code scheduled):**
- F5: org-name slugging drops non-ASCII (`Ä` → dropped, not transliterated to `a`); emoji-only
  names produce an empty slug requiring a manual one. Cosmetic.
- F6: the verify-email success page could point to sign-in more loudly (the link does not and
  should not establish a session; the page just under-sells the next step).
- B6 (CONFIRMED in the Part B walk): the member-role dashboard shows "Enroll a gateway →" but
  IssueJoinToken requires org:update — the affordance leads to a guaranteed 403. Role-aware
  empty-state copy needed (same class as the S4.3 role-aware empty-state watch-item).
- Domain-capture has API endpoints but NO Settings UI (found in Part B) — surfacing claim/TXT/verify
  states in the UI is an open story candidate (S4.5 watch-item d was never built). Trigger = the
  capture-UI story; the B2 DNS-TXT manual leg rides it.
- B4 negative leg (optional): live-exercise `sso_link_required` 409 (SSO login vs an UNVERIFIED
  local account) — server code confirmed present; needs a third Entra test user.

## EPIC 5 — CLI Client (dogfood & de-risk before Electron)

- **S5.1 `tunnex` CLI** — walk-derived scope (Round-2, D1/D2/D3 resolved; supersedes the original
  "fetch config" sketch):
  **Auth (D1+D3):** `tunnex login` opens the SYSTEM BROWSER to Tunnex; authentication (local or
  SSO — MFA and all) completes in-browser against Tunnex; Tunnex then redirects to the CLI's
  `http://127.0.0.1:<port>/callback` with a ONE-TIME authorization code; the CLI exchanges the code
  for a **dedicated CLI credential** — a NEW server-side model: hashed at rest, identity-bound
  (identity↔credential principle), keyed-fingerprint audit rows written same-tx (proof-of-secret
  convention), header-borne (no cookie → csrfGuard is already inert for it — VERIFY with a test),
  revocable. **Entra never sees the loopback** — the CLI callback is Tunnex's own redirect; the
  server needs a loopback-redirect allowlist (127.0.0.1 only, any port). **D1 caveat (recorded):**
  verified on an MFA-less Free tenant; the MFA claim rides the in-browser-completion ARGUMENT
  (challenges finish before the final redirect), not observation. **Device-code fallback for
  browserless hosts stays in scope.**
  **Config (D2):** the CLI OWNS device creation — the config is captured exactly once at creation
  and written atomically, `0600`, under `~/.config/tunnex/`; then the `wg-quick up/down` wrapper.
  Guards auto-arm: new endpoints picked up by the 401-walk + RBAC matrix; one deliberate-red per
  new guard.

### S5.1 decide-before-code (COMMIT ONE — decided on paper, for review before code)

**(1) CLI credential lifetime + revocation semantics.**
- **Password reset SWEEPS CLI credentials — YES** (the default stands, no argument against it):
  a reset signals identity compromise; S2.2 already sweeps sessions on exactly that signal, and a
  surviving CLI credential would be a back door around the sweep. Same tx, same trigger.
- **Deactivation sweeps too** (S2.6 parity — a deactivated user's CLI must die with their sessions).
- **Lifetime: 90-day absolute expiry**, no sliding refresh in S5.1 (`tunnex login` again is cheap;
  refresh-token machinery is not — defer until dogfooding demands it). Expiry stored server-side
  next to the hash.
- **Listable/revocable: API now, dashboard UI deferred.** The model ships with list + revoke
  endpoints (name, created_at, last_used_at, fingerprint — never the token), because the endpoints
  arm the 401-walk/RBAC guards and `tunnex logout` needs revoke anyway. The dashboard "CLI
  sessions" panel is a LEDGERED follow-up (rides a later dashboard story), not S5.1.

**(2) Header format + OpenAPI representation.**
- **`Authorization: Bearer <token>`** — the standard every CLI/tool ecosystem expects; no custom
  header. Token format: opaque random (32B, base64url), prefixed `tnx_` so leaked-secret scanners
  can pattern-match it; NEVER a JWT (server-side revocation must be instant, not TTL-bound).
- **OpenAPI: a second securityScheme** (`http`/`bearer`) alongside the cookie scheme; gated ops
  accept either. The 401-walk keeps walking sessionless ops; a new deliberate-red proves a
  REVOKED bearer token is refused (not just a missing one).
- csrfGuard stays cookie-keyed and is therefore inert for bearer requests — one test PROVES a
  bearer mutation with no cookie and no X-Tunnex-CSRF header succeeds (the CLI never does the
  CSRF dance; that was D3's point).

**(3) Loopback callback discipline (join-token-class hygiene).**
- **Port: OS-assigned ephemeral** — the CLI listens on `127.0.0.1:0` and puts the actual port in
  the redirect it requests; the server's allowlist validates host `127.0.0.1` (or `[::1]`)
  EXACTLY, any port, fixed path `/callback` — nothing else, ever (no `localhost` — DNS-spoofable).
- **Code: single-use, 60s TTL, PKCE-bound.** The CLI mints a code_verifier, sends the S256
  challenge on the authorize leg; the exchange requires the matching verifier — a stolen code
  alone is useless (same discipline as join tokens: hashed at rest, consumed atomically).
- **Exact-match binding:** the code is bound at mint to the EXACT redirect (host+port+path) it was
  issued for; the exchange re-presents it and must match. State parameter carried end-to-end for
  the CLI's own request correlation.

**Approved (sign-off) with recorded ADDITIONS:**
- **(a) Minting is verified-gated** (`requireVerifiedUser` on the authorize + device-approve legs).
  No exception argued: an unverified account must not mint a long-lived credential when the same
  account can't perform org mutations — the credential would outlive/outrank its owner's standing.
- **(b) bearer ≡ cookie on ALL authenticated endpoints.** Any exception is ARGUED IN THE SPEC on
  the op itself. Two exceptions argued: `cliAuthorize` and `cliDeviceApprove` are cookie-session
  ONLY — minting a new credential from an existing bearer credential is self-replication (a stolen
  token could outlive its expiry by re-minting); the browser leg is the human checkpoint.
- **(c) `state` carried on the loopback callback alongside PKCE** (CLI-side request correlation +
  CSRF on the loopback listener); the device-code fallback inherits the SAME code-hygiene class
  (hashed at rest, single-use, short TTL, atomic consume).
- **SHA256SUMS for website distribution:** every released CLI artifact set ships a SHA256SUMS file
  (and its URL is printed in install docs); signing rides the EPIC 5 ops item (Apple ID + EV cert).
- **Expired-credential UX:** on any 401 `credential_expired`, the CLI prints exactly one actionable
  line — `credential expired — run 'tunnex login'` — never a raw error dump.

**S5.1 ACCEPTANCE CRITERION (spec sign-off flag 1) — the consent page is a real checkpoint:**
the browser leg renders an explicit consent page that (i) requires a DELIBERATE CLICK to mint —
never auto-approves on load (an instant redirect would reduce the "human checkpoint" argument for
the cookie-only exception to theater); (ii) DISPLAYS the loopback redirect it will send the code
to, INCLUDING THE PORT (the user can see which local process is asking); (iii) the device-approve
page displays the user_code it is approving. **Playwright proof of the no-click-no-mint property**
(landing on the consent page mints nothing; only the click calls cliAuthorize).

Ledgered at spec sign-off (flags 2+3):
- **Rate-limit targets for the public CLI endpoints** (cliToken, cliDeviceStart, cliDeviceToken —
  brute-force surface: code guessing, device-code polling) → S11.3 (rate limiting & security
  headers); interval/slow_down semantics are already in the device contract.

Ledgered at story-end review (S5.1 5/n+):
- **Expired-credential 401 oracle — REMOVED (not accepted).** The distinct `credential_expired`
  code was dropped: the server now returns a generic 401 for expired, BYTE-IDENTICAL to
  revoked/unknown (extended no-oracle test asserts all three identical). The CLI disambiguates
  expiry from its LOCALLY stored `expires_at` and prints the exact "run 'tunnex login'" line, so the
  UX is preserved with no server-side oracle. Closed at Pawan's direction pre-merge.
- **Expired/consumed CLI-code GC**: `cli_auth_codes` (60s) and `cli_device_codes` (15m) rows are
  never deleted after expiry/consumption → unbounded growth. Add a periodic
  `DELETE … WHERE expires_at < now() OR consumed_at IS NOT NULL` sweep (a cron/boot job). → S11 hardening.
- **Rate limits for the public CLI endpoints** (cliToken code-guessing; cliDeviceStart/cliDeviceToken
  device-code brute-force + phishing amplification) → S11.3. The device-flow phishing surface is
  inherent to device-code flows; mitigated now by the anti-phishing warning on /cli-device, fully
  addressed by the rate limit.

Ledgered at implementation sign-off (MERGED item):
- **User-scoped credential surface** = admin revoke of another user's CLI credential + the
  CLI-credential audit slice (cli.credential_issued/_revoked rows are written org-NULL and are
  therefore invisible to the org-filtered audit viewer — the rows exist, the surface doesn't).
  **Trigger: the security-review pass or the dashboard CLI-sessions panel story, whichever lands
  first.** Until then: revocation is self-serve (`tunnex logout`, DELETE endpoint) + the
  reset/deactivation sweeps; the audit slice is queryable in the DB.
- **(Ops, when EPIC 5 begins)** Begin **code-signing cert procurement** — Apple Developer ID + Windows EV cert (weeks of lead time).

## EPIC 6 — Electron Desktop Client (Windows + macOS)

- **S6.1 Client shell** — Electron app, reuse React renderer, secure IPC, auto-update scaffold.
- **S6.2 Client auth** — login against tenant (local + SSO via system browser + deep link).
- **S6.3 Tunnel control** — start/stop WireGuard, embed `wireguard-go`/wintun (mac/win), privilege helper.
- **S6.4 Connection UX** — status, server picker, split-tunnel toggle, tray icon, notifications.
- **S6.5 Packaging & signing** — `electron-builder` `.dmg` + `.exe`/msi, code-signing + notarization (certs from EPIC 5).

## EPIC 7 — Zero Trust Access *(enterprise)*

- **S7.1 Policy model** — resources, groups, access rules (who → what), default-deny.
- **S7.2 Policy enforcement** — evaluate on connection + per-peer route filtering (via agent).
- **S7.3 Device posture (basic)** — require known device, block untrusted.
- **S7.4 Policy UI** — rule builder in dashboard.

## EPIC 8 — Site-to-Site Networking

- **S8.1 Gateway/site model** — register site gateways (each a `tunnex-node` agent), subnet routing.
- **S8.2 Route propagation** — advertise/accept routes between sites via WireGuard, reconciled by agents.
- **S8.3 Site management UI** — add site, topology view, health.

## EPIC 9 — OpenVPN Support (port from existing Bolster stack, not greenfield)

- **S9.1 OpenVPN server mgmt in node agent** — port `openvpn-auth-oauth2` patterns + `genclient`-style PKI into the agent; managed process, cert/PKI, config gen. Reference the Bolster handover doc as the spec.
- **S9.2 OpenVPN profiles** — `.ovpn` export, per-user certs, revocation (CRL) — same identity-binding rule as S3.3.
- **S9.3 Protocol selection** — org/server chooses WireGuard or OpenVPN; clients support both.

## EPIC 10 — Kubernetes Integration

- **S10.1 Helm chart** — deploy full tunnex stack to a cluster; values for secrets, ingress, storage.
- **S10.2 Operator + CRDs** *(enterprise)* — `TunnexPeer`, `TunnexRoute`; reconcile WG peers/routes as k8s resources — **reuses the S3.1 reconcile loop design**.
- **S10.3 Cluster gateway** — expose in-cluster services to tunnex clients via Zero Trust policies (agent as in-cluster gateway).

## EPIC 11 — Production Hardening

- **S11.1 Metrics** — Prometheus metrics, health/readiness (logging already in EPIC 0).
- **S11.2 Backup/restore** — DB + master key **+ node-agent state (WG private keys on each gateway)**; documented restore.
- **S11.3 Rate limiting & security headers** — API abuse protection, TLS via nginx, secrets hygiene.
- **S11.4 Docs & install guide** — self-host quickstart, upgrade path.

---

## Recommended Build Order
EPIC 0 → 1 → 2 → 3 (WG core loop) → 4 (dashboard) → 5 (CLI) → 6 (Electron) → 7 → 8 → 9 → 10 → 11.

## First Story to Execute: **S0.1 + S0.2 (Foundation + one-command boot)**
Deliverable: a `git`-ready monorepo where `docker compose up` brings up postgres, redis, a Go API `/healthz` (structured logging + request IDs), a node-agent stub (`NET_ADMIN`, WG UDP port), Mailpit, and a React dashboard shell reachable through nginx.

Critical files (S0.1/S0.2):
- `go.mod`, `apps/api/cmd/server/main.go`, `apps/api/internal/http/router.go` (chi + `/healthz`), `apps/api/internal/log` (slog + request-ID middleware)
- `apps/node/cmd/agent/main.go` (agent stub + registration handshake placeholder)
- `apps/web/` Vite + React + Tailwind app shell
- `docker-compose.yml`, `deploy/docker/{api,node,web,nginx}.Dockerfile`, `deploy/nginx/nginx.conf`
- `.env.example`, `Makefile`, `pnpm-workspace.yaml`, `turbo.json`, root `README.md`

## Verification (S0.1/S0.2)
1. `cp .env.example .env && docker compose up -d` → all services healthy (`docker compose ps`).
2. `curl localhost/healthz` → `200 {"status":"ok"}` through nginx; response carries a request ID that appears in structured logs.
3. Browser `http://localhost` → Tunnex dashboard shell loads; Mailpit UI reachable on its port.
4. `docker compose down -v` → clean teardown, no orphaned volumes.

## Resolved Decisions (recap)
- React + Vite SPA (reused by Electron) · single-domain multi-tenancy · control/data-plane split from day one.
- OpenAPI-first contract with codegen. CLI before Electron; cert procurement starts when EPIC 5 begins.
- Logging in EPIC 0; metrics in EPIC 11.
- **Open-core:** multi-tenant schema in core, org-creation limit in open build; enterprise boundary established at **S1.1**; SSO/policies/operator gated.
