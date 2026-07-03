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
- **Desired-state reconciliation:** data-plane state (WG interface) is continuously reconciled against control-plane desired state — never assumed in sync. Same pattern powers the K8s operator.
- **Structured logging + request IDs from day one** (S0.1 DoD), not retrofitted at the end.
- **Secrets encrypted at rest** under a bootstrap master key (S0.3); per-org IdP client secrets are never plaintext.

### Build Protocol (per story)
1. Implement the story on its own branch/commit.
2. Self-review + run `/code-review`; run tests; verify end-to-end.
3. Report outcome, get sign-off, then start the next story.

---

## Edition Model — Open-core (resolved)
- **Schema is multi-tenant in core.** Everything carries `org_id`; the open edition simply **does not expose creating a second org** — an API/UI limit, not a schema fork. No migration or code move later.
- **Enterprise features** (gated behind an `internal/enterprise/**` package + build tag): SSO (Google/Microsoft), Zero Trust policies, Kubernetes operator, and the multi-org limit-lift.
- **The enterprise boundary is established in S1.1**, because the first gated decision (org-creation limit) lives there — not at SSO. SSO/policies/operator plug into the same boundary as they arrive.

---

## EPIC 0 — Foundation & Scaffolding

- **S0.1 Monorepo scaffold** — layout, `pnpm` workspace, `go.mod`, Make/Turbo targets, linting, README. **DoD: structured logging (slog) + request-ID middleware + `/healthz` that logs with correlation IDs.**
- **S0.2 Docker Compose one-command boot** — postgres + redis + api + web + nginx + node-agent + Mailpit; `.env.example`; healthchecks; `make up`/`make down`. **Note the non-web bits:** node-agent needs `cap_add: NET_ADMIN` and the **WG UDP port published**.
- **S0.3 First-boot bootstrap, secrets & mailer** — entrypoint auto-generates JWT/session secrets, DB creds, WG server keys, and a **master encryption key** if absent; persists to a volume; idempotent. Sensitive per-org data (IdP secrets) stored **DB-encrypted (AES-GCM) under the master key**. **Pluggable mailer:** SMTP env vars for prod; **dev fallback = Mailpit** (compose) + log the link.
- **S0.4 DB migrations & tooling** — `golang-migrate`, `sqlc`, `make migrate`.
- **S0.5 OpenAPI contract + codegen** — author the OpenAPI spec; generate the TS client into `packages/shared`; wire request/response validation on the Go side. Source of truth for all later endpoints.
- **S0.6 Seed data + e2e test harness** — `make seed` (demo org/user); Playwright (web) + `httptest` (API) skeletons so every later story's "verify end-to-end" has rails. **DoD: seed + e2e run green on the open build using local auth only** (no enterprise/SSO dependency), so the open edition is fully testable end-to-end.

## EPIC 1 — Multi-Tenancy Core

- **S1.1 Data model + enterprise boundary** — `organizations`, `users`, `memberships`, `invitations`, `audit_logs`; org-id row scoping. **Establish `internal/enterprise/**` + build tag here; open build enforces the single-org-creation limit.**
- **S1.2 Org lifecycle** — create org, settings, slug/domain, soft-delete.
- **S1.3 Tenant context middleware** — resolve current org from session membership; enforce isolation on every query.
- **S1.4 RBAC** — roles (owner, admin, member) + permission-check middleware.

## EPIC 2 — Authentication (Google + Microsoft + Local)

- **S2.1 Local auth** — signup/login, argon2id, **email verification + password reset (uses S0.3 mailer)**.
- **S2.2 Session management** — Redis-backed cookie sessions, CSRF, logout, refresh.
- **S2.3 Google OIDC** *(enterprise)* — login + account linking; per-org SSO config (secret encrypted at rest).
- **S2.4 Microsoft Entra OIDC** *(enterprise)* — login + account linking; multi-tenant Azure app; secret encrypted.
- **S2.5 SSO provisioning & domain capture** *(enterprise, security-sensitive — extra review)* — JIT user creation + role mapping. Require **DNS-TXT-verified domain ownership**; **block public domains** (gmail.com, etc.); **domain capture is globally unique** (two orgs cannot capture the same domain); never auto-join on unverified email.
- **S2.6 Manual user management** — admin invites/creates users, resend/revoke invites, deactivate.

## EPIC 3 — WireGuard Core Loop (proves the product — before full dashboard)

- **S3.1 Node agent + control-plane protocol** — define `tunnex-node`: registration, mTLS/gRPC between API and agent, desired-state push + **reconcile loop** (agent compares desired vs. actual `wgctrl` state on an interval; heals drift). **Agent enrollment:** a one-time **join token** (generated in dashboard / compose bootstrap) is exchanged for the agent's mTLS client cert on first connect. **Revocation latency spec:** control plane **pushes** revocations (agent applies in **<5s**); interval reconcile is the safety net, not the primary path.
- **S3.2 WG server lifecycle** — interface up/down via agent, key mgmt, listen port, address pool (CIDR) per org.
- **S3.3 Peer/device management** — issue peer config, QR/download, per-user device list, revoke. **Acceptance (identity binding):** a peer config cannot be created/activated except via the owning user's authenticated session; admin-created peers are bound to a named user; revocation immediate per S3.1 latency spec.
- **S3.4 Client config generation + bare UI page** — `.conf` output (DNS, allowed IPs, keepalive) + minimal download page. **← "Tunnex is real" milestone.**
- **S3.5 IP allocation service** — deterministic, collision-free assignment from org pool. **Acceptance (edge cases):** address **release/reuse** on revocation; safe on **org CIDR resize**; no reassignment of an in-flight address.
- **S3.6 Live connection status** — handshake/last-seen, bytes tx/rx, online peers (data from agent).

## EPIC 4 — Full Web Dashboard

- **S4.1 App shell & design system** — Tunnex brand (logo assets from user), Tailwind theme, layout, nav, auth-gated routing.
- **S4.2 Login / signup / SSO screens** — all three auth paths.
- **S4.3 Dashboard home** — org overview, members, activity, live connection stats.
- **S4.4 Users & roles UI** — list, invite, edit role, deactivate.
- **S4.5 Org settings & SSO config UI** — connect Google/Microsoft, domain-capture rules.
- **S4.6 Audit log viewer** — filterable event stream.

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
