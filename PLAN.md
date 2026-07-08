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

---

## Story status (re-entry checkpoint)
**Update this on every merge (one line) — a stale pointer re-enters a fresh session in the wrong epic.**
Current: **S4.7 (fresh-user onboarding) IN PROGRESS — reopened EPIC 4's onboarding gap as its own
story. Commit one = the onboarding state-machine decision (below). S5.1 (`tunnex` CLI) HELD until
S4.7 merges + Pawan's Round-2 walk lands its friction list.**
Next after S4.7: the natural S5.1 work — CLI `login` (browser + deep-link callback, with a
device-code / localhost `127.0.0.1:<port>` callback fallback for headless), fetch config,
`wg-quick up/down` wrapper. S5.1 decide-before-code (TBD, HELD): device-code vs localhost-callback
selection; client-side config storage location + file permissions; how the CLI authenticates
against a session model built for browser cookies.
Still pending: **Round-2 manual testing walk** (fresh-org + Entra SSO against a real tenant) —
wanted right after S4.7 merges, BEFORE S5.1 locks CLI flow assumptions; its friction feeds S5.1.
Ops (Pawan, long lead — START NOW): code-signing procurement — Apple Developer ID (~$99/yr, days)
+ Windows EV cert (~$300-500/yr, 1-3wk validation). Hard-blocks S6.5 packaging; nothing in S5.1
blocks on it, but the validation clock starts at application.
Done through (merged to `main`): **EPIC 0–2, EPIC 3 (S3.1–S3.6), and EPIC 4 COMPLETE — S4.1
(shell) · S4.2 (auth) · S4.3 (dashboard) · S4.4 (users & roles) · S4.5 (org settings + SSO) ·
S4.5b (CIDR resize) · S4.6 (audit viewer).** Next epic: EPIC 5 (CLI client). If this pointer
disagrees with the handoff doc / git log, TRUST GIT (`git log --oneline -15`) and update this line.

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

Conventions named: gateway **join token = one-time secret** (S4.5 config-download ceremony — amber
callout, "I've saved it" gate, no route back, keyed fingerprint in logs/audit, never the raw token);
audit rows same-tx, actor-attributed, secret-free; guards auto-arm (401-walk picks up any new gated
op, RBAC matrix, deliberate-red one-line per new guard).

Prove: fresh-org empty-state render set (Playwright, all three router branches); enrollment e2e
(join-token → agent joins → node appears — real compose agent if the harness allows, else mocked
ceremony + a deferred-ledger entry).

## EPIC 5 — CLI Client (dogfood & de-risk before Electron)

- **S5.1 `tunnex` CLI** — `login` (browser + deep-link callback), fetch config, `wg-quick up/down` wrapper. Validates the client↔API↔agent protocol in days and unblocks dogfooding. **Headless acceptance:** when no browser/URL-scheme is available (servers, CI, site gateways), fall back to **device-code flow or localhost callback** (`http://127.0.0.1:<port>/callback`).
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
