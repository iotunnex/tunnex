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

**Merge mechanics (confirmed S6.0b):** merges to `main` are ff-only + linear history. As of S6.0b,
`main` has GitHub branch protection REQUIRING the CI checks `gates` + `client (macos-latest)` +
`client (windows-latest)` (the `e2e` job is opportunistic, NOT required); `enforce_admins=false` so
an admin (iotunnex) can still push, but the social sign-off gate is now mechanized for PRs. The
standard flow: story branch → PR → CI green (required checks) → user sign-off → ff-merge → push.
CI is the CONTINUOUS invariant proof; the human sign-off is still required on top (CI green ≠ auto-merge).

**Force-push standing authorization (S6.3):** `git push --force-with-lease` is pre-authorized for
`story/*` branches ONLY (e.g. after a rebase onto main) — no per-push ask needed. `main` is NEVER
force-pushed (protected + linear). This covers only the working story branches, whose history is
expected to be rewritten before merge.

---

## Story status (re-entry checkpoint)
**Update this on every merge (one line) — a stale pointer re-enters a fresh session in the wrong epic.**

**CURRENT (2026-07-14): EPIC 6 COMPLETE + `v0.1.0` CUT; IN EPIC 7 (Zero Trust Access) — S7.1/S7.2/S7.3 MERGED, NEXT S7.4.**
- **EPIC 6 done:** S6.1–S6.6 + S6.7 + S6.8/9/10 (+ S3.7) all MERGED. The mid-epic stories were spun
  up live during full-tunnel hardening and are defined ONLY here (reconciled against git log, so the
  checkpoint never points at ghosts): **S6.8 = quit continuity** (graceful helper Down on app quit +
  fast orphan dead-man — internet no longer dead ~60s after quit); **S6.9/S6.9b = Windows full-tunnel
  guard** (server-side CLEAN refusal of Windows full-tunnel until DNS parity + kill-switch persistence
  landed — LIFTED at S6.7); **S6.10 = Windows full-tunnel DNS parity** (API-verified DNS on the wintun
  adapter, empty-DNS refusal, atomic DNS↔kill-switch coupling). **S6.6 (zero-build deploy) MERGED
  (PR#13) and `v0.1.0` tagged** → multi-arch ghcr images + `install.sh`/`.sha256` release assets published.
  Only **S6.5b** (signing/notarization/auto-update) deferred — named trigger, not a gap. Remaining S6.6
  proof: the clean-VPS acceptance box-proof (`docs/S6.6-acceptance.md`), Pawan's box test.
- **EPIC 7 PULLED AHEAD DELIBERATELY** (chosen over EPIC 8/11 after EPIC 6 closed). Sequential:
  **S7.1 policy model → S7.2 enforcement → S7.3 device posture → S7.4 policy UI.** **S7.1 MERGED
  (PR#14, fe67e28)** — allow-only default-deny model + pure deterministic compiler (`policyspec.Compiled`),
  enterprise-gated CRUD, migration 0018 (incl. the group_members→memberships cascade FK from the F1 fix).
  Enforcement + the on-the-wire default-deny proof are S7.2 (ledgered: AffectedNodeIDs direct test +
  member-removal as the 4th recompile+push trigger). **S7.2 MERGED (PR#16, ac74123)** — enforcement
  box-proven 8/8. **S7.3 MERGED (PR#17, 5e9838a)** — device posture (approval gate + F1-part-3 org-wide
  push + migration reduction arc), box-proven on a live two-gateway wire incl. the 3b cross-gateway
  discriminator (see the re-entry checkpoint). **NEXT: S7.4 (policy UI + differentiated health surface +
  enterprise-e2e stack) decision-first.** See `docs/S7.1-decisions.md`, `docs/S7.3-decisions.md`.
- **LEDGER re-points (recorded at S7.1 sign-off):** triggers formerly anchored to **"EPIC 6 close"**
  (S3.7 decision-review revisit; beta re-decide) are re-pointed to the named trigger **"public-beta
  readiness"** (never calendar clocks). **EPIC 7 is the trigger to build the deferred ENTERPRISE-E2E
  STACK** → unblocks the **S4.5** secret-payload Playwright assertion (GET sso payload carries no
  client_secret material) + the **S4.5b** orphan-render check; both **ledgered into S7.x scope**.
- **DEFERRED CLIENT-WIRE-SMOKE (S7.3 device posture — SUBSTITUTES ≠ SATISFIES, named not dropped):**
  the S7.3 desktop legs are DESKTOP-ONLY and could not run on the headless box-proof VM (no Electron):
  (1) connect a **pending** device on a real mac/win desktop → stable "Awaiting admin approval…" state,
  helper NEVER armed (no admin prompt / no `utun`/WFP adapter), **no spurious "revoked"** across ≥60s;
  (2) trigger a **legacy re-mint** (strip `orgId` from a stored config) → one-time "device replaced" +
  fresh mint; (3) force a migration revoke-fail with OS notifications muted → the new **`migrate_failed`
  legible state** shows in the window/tray ("Couldn't replace device — reconnect to retry"), NOT a bare
  "Disconnected". The **66 client unit tests SUBSTITUTE** (connect-gate, ApprovalMonitor, `migrateLegacyConfig`
  revoke-first, `trayStateFor migrate_failed`) **but do NOT satisfy** the wire proof — same discipline as
  the S4.5 secret-payload + S6.3 packaged-residue deferrals. **Trigger:** the S6.5-class packaged-client
  smoke OR the next real mac/win desktop session, whichever lands first.

**History (EPIC 6 detail):** **S6.5a PACKAGING MERGED (PR#6, 7228d29)** — unsigned macOS `.pkg` (install-time helper via
postinstall, /Applications-pinned, self-uninstall watchdog) + Windows NSIS `.exe` (SCM service, sidtype
unrestricted, Add/Remove uninstall); universal helper; Gatekeeper/SmartScreen install docs; SHA256SUMS;
CI packages the win `.exe` NATIVELY (fixes the cross-built uninstaller). macOS proofs ALL PASS live
(install/connect/ping, residue, tray); Windows install/connect/device-to-device PASS live. Full review
folded (10 findings: 2 security-critical — pf-anchor double-escape defeating the kill-switch + apostrophe
root-shell-injection in the in-app install; + teardown/lifecycle). **NEW GAP LEDGERED → S6.6:** the
Windows WFP full-tunnel kill-switch is **NOT fail-closed on process death** (pcap leaked — wireguard-windows
uses `FWPM_SESSION_FLAG_DYNAMIC`, filters auto-delete on process exit). macOS pf is persistent (proven);
Windows is not. **NEXT: S6.7 (Windows kill-switch persistence)** (the merged S6.5a docs call this "S6.6" — RENAMED to
S6.7 because S6.6 is already Zero-build deploy) — non-dynamic WFP session + fixed provider
GUID + explicit enumerate-and-delete DisableFirewall + reboot/CleanStale recovery, decision-first + box-
proven + reviewed; AND **S3.7 (gateway egress NAT) APPROVED, build after S6.5a merge** (nftables-via-Go-
netlink, probe-every-reconcile, JSONB nodes.capabilities, gateway_no_egress refuse, IPv6 NAT66 best-effort,
device-to-device productized, DoD deletes poc-gateway-nat.sh + compose ip_forward). Full-tunnel usability
needs BOTH S3.7 (egress) + S6.6 (kill-switch). **Prior: EPIC 6: S6.1/S6.2/S6.0b · reconcile-idempotence hotfix
(a8c5344) · S-POC-fixes (copy-button/APP_BASE_URL/invite-rework, PR#3) · **S6.3 TUNNEL CONTROL MERGED
(PR#4, 1b36067)** — root privilege helper (typed protocol, canonicalized caller-auth, version-upgrade
handshake) + macOS **pf** & Windows **WFP** kill-switch backends + **bounded fail-closed** (startup
self-heal + 90s dead-man + graceful Down) + split-default/endpoint-exclusion routing + desktop Connect
UI + dev-install/uninstall (first-class uninstall) + native-lifecycle design. Whole-branch multi-finder
review folded (10 findings, 2 deliberate-reds). macOS kill-switch **PROVEN LIVE** (kill -9 pcap: zero
cleartext + auto-recover). DEFERRED live proofs (ledgered): Windows WFP pcap + windows endpoint paths →
**S6.5a**; packaged residue smoke → Windows **S6.5a** / macOS-SMAppService **S6.5b** (needs signing);
gateway-NAT/full-tunnel egress → **S3.7** (parked, deletes poc-gateway-nat.sh). **S6.4 CONNECTION UX MERGED
(PR#5, 011bb09)** — app-side only (helper/kill-switch untouched): revocation-aware teardown
(`RevocationMonitor` — self-scheduling poll, only-while-up, throw→keep+capped-backoff, fire-once → loud
banner/tray/notification), change-server/sign-out (`DesktopSettings` via existing verb allowlist),
split-tunnel toggle (re-mints on split↔full with full-sweep revoke; `gateway_no_egress` pre-mapped for
S3.7), tray + notifications. High-effort multi-finder folded — ROOT FIX: per-window services → **app-level
singletons, window a detachable null-safe view** (tunnel now SURVIVES window close — the point of a tray;
kills the macOS dock-reopen "second handler" crash + closed-window controller/monitor leak); + #1
`deviceExists` throws on empty-orgs 200 (a replica-lag blip no longer false-revokes a live device). Client
51 tests. **NEXT: S6.5a (UNSIGNED packaging — .dmg/.exe + Gatekeeper/SmartScreen workarounds; needs NO
certs, nothing ops-side blocks it).** First green run went 4/4 (gates + client mac + client
win + e2e) after fixing: `.env` in CI, a Windows path-fixture, `-mod=readonly`, and THE real gates
bug — `.gitignore`'s unanchored `secrets/` had silently kept apps/api/internal/secrets SOURCE out of
git (fine locally, broken on every fresh clone). Remote: github.com/iotunnex/tunnex (public); pushed
as the iotunnex account. Merged in EPIC 6: S6.1 (client shell) + S6.2 (renderer transport — desktop
tenant-functional) + S6.0b (CI). **Distribution: S6.5 SPLIT — S6.5a (unsigned packaging) ships in
EPIC 6; S6.5b (code-signing + notarization + auto-update ON) is DEFERRED, trigger = public beta OR
first outside-circle distribution (NOT a calendar clock). Windows EV needs a legal entity that does
not yet exist → entity formation is additive lead time; interim = individual Apple Developer ID.**
**Ops (Pawan): domain purchase tunnex.io PENDING — blocks real-deployment APP_BASE_URL / SSO
redirect URIs / outbound email, and the B2 domain-capture walk item.** S3.7 parked at paper. Beta
deferred — re-decide at EPIC 6 close.
Ledgered: CLI-code GC → S11, rate limits → S11.3, user-scoped credential surface → security review /
CLI-sessions panel; S3.7 gateway-NAT parked (trigger = EPIC 6 close or beta).
**External DB/Redis support (DECIDE-BEFORE-CODE, parked; see docs/S6.6-decisions.md):** install.sh
accepts `TUNNEX_DATABASE_URL`/`TUNNEX_REDIS_URL` (URL-wins; bundled compose stores move behind a
profile), bootstrap skips credential-gen + validates/migrates/fails-loud when externally set. **Decide-
item = master-key externalization** (env override vs volume) — the master key NOT being in the DB is the
durability trap an RDS customer hits (lose the volume → lose the key → DB-encrypted data undecryptable).
The env seam MUST be SHARED with the **S10.1 Helm values** (compose + K8s must not diverge). Full polish
(TLS/sslmode docs, profiles, RDS runbook) parked, **trigger = first customer request OR S10.1**.
**POC FRICTION LEDGER (WS2, triaged 2026-07-09):** item 1 → **S6.6 zero-build deploy** (SB.1/SB.2
shrink); items 2+3 → **S-POC-fixes** (started next); item 4 → **S6.4** (in-app change-server/sign-out);
item 5 (**dev-install: codesign-after-cp on Apple Silicon fixing Killed:9 + auto-detect the Electron
path for `TUNNEX_INSTALL_DIR`**) → fold into `scripts/macos-dev-install.sh` (not customer-facing);
item 6 (**join-token env-vars-must-be-inline gotcha**) → the gateway ceremony shows the COMPLETE
runnable command incl. `docker compose up -d --force-recreate node-agent`, not just the vars; item 7
(**client Node >=20 engine warning**) → pin/enforce or fix compat. ALSO surfaced + already fixed:
the `.env` `cat >>` duplicate-key trap (compose used the first value) — the S6.6 install.sh writes a
clean `.env` (no append). **Item 8 (NEW, FIXED in S-POC-fixes): invite accept was broken end-to-end —
the web had no `/accept-invite` route, so the email link dropped the token and the invited user was
sent to create-org instead of joining the inviting org.** Fixed: web AcceptInvite page + public route.
**Delivery + auth decisions (superseding an initial auto-login attempt):** CreateInvitation returns the
raw token so the dashboard shows a COPYABLE accept link (shared OneTimeSecretModal) — the SMTP-less
delivery path (POC hit "no email": dev mailer only tees to logs/Mailpit); email stays best-effort. The
accept does **NOT auto-login** — because the link is now admin-visible, minting a session from it would
let a link-holder land in an existing invitee's account (impersonation). Invitee sets a password (new
user) / keeps existing (never reset), then **signs in explicitly** and lands in the org. Item 3's
APP_BASE_URL fix still matters for the emailed link; the UI link uses the browser origin.
**REPO VISIBILITY — DECIDED: stays PRIVATE until the beta milestone.** Rationale: pre-beta there is
no external audience, and private keeps the unfinished/unsigned client + evolving security surface out
of public view; the cost is Actions runner QUEUING (private repos share a small pool + a 2000-min/mo
budget, macOS 10×/Windows 2×) — accepted for now. History is already secret-clean + Entra IDs scrubbed,
so flipping public is safe whenever the beta trigger (same as S6.5b) fires. TRIGGER to go public =
public beta.
**RESOLVED DECISIONS:** (a) **LICENSE — LANDED on `main`:** root **Apache-2.0** (Copyright 2026
Tunnex) + `NOTICE`; `apps/api/internal/enterprise/LICENSE` = proprietary **source-available**
(reference-visible, commercial agreement for production, NO redistribution); README **Licensing**
section citing the `test-editions` build-tag guard; `CONTRIBUTING.md` (external PRs paused pending
CLA/DCO). **Copyright held under the pre-entity project name "Tunnex" — on entity formation, execute
a written assignment from the individual authors to the entity and reaffirm the notices; TRIGGER =
entity formation (the SAME event S6.5b already requires for the Windows EV cert). One event now
closes BOTH the EV blocker and the copyright cleanup.** (b)
**Go module path — DECIDED: defer to the VANITY path (`tunnex.io/…`) on domain purchase**; interim
keep-as-is, now GUARDED by a `-mod=readonly` note in each go.mod + the Makefile so the flag can't be
innocently dropped pre-rename.
**SECURITY LIMITATION (S6.3, named):** the privilege helper's INTERIM caller-check on unsigned builds
is executable-path-inside-install-dir verification — WEAKER than code-signing identity pinning. Blocks
a non-admin local process from driving the root helper; does NOT stop an already-admin attacker or a
path-spoofing race. Wire protocol carries `auth_mode` so this upgrades to `code_signing` at S6.5b
without a break. TRIGGER to retire = S6.5b (signing + notarization).

**AVAILABILITY LIMITATION (S7.2, named — gateway cold-start deny-until-first-fetch):** a gateway that
starts (crash / upgrade / reboot) BEFORE its first successful desired-state fetch renders a deny-all
forward chain regardless of the org's Zero Trust mode — including for OFF / open-build orgs. This is
INHERENT to the boundary, not a bug: the gateway cannot learn its mode without reaching the control
plane, so the only safe default before it knows is fail-closed. The alternative (serve blanket mesh on
cold start) would let a reboot-during-CP-outage turn an ENFORCING org into an open mesh — a breach, not
an outage. **Exposure:** a gateway reboot that COINCIDES with a control-plane outage → an off-mode org's
forwarded traffic is denied until the CP returns. **Bounded + self-healing:** the very first successful
fetch flips the state (`policyReceived`) and restores mesh/grants; no manual step. NB this is scoped to
the NODE cold-start only — the control-plane policy-error path IS scoped by mode (finding #2: off orgs
served mesh), so a CP/DB blip does not blackhole off-mode orgs while their gateway is already running.

**NAMED LIMITATION (S7.3, migration compound-edge — [0] recorded-as-CLOSED):** the client's legacy-config
migration (a pre-`orgId` v0.1.0 profile → one-time re-mint) can, on the compound edge
`legacy × persistent-revoke-failure × OS-notifications-muted`, leave the user on a repeating soft
`migrate_failed` state ("Couldn't replace device — reconnect to retry") rather than auto-completing. This is
BOUNDED by construction (config kept, terminal-per-connect, no raw reject, no unbounded loop) and now
LEGIBLE in the window/tray (the fifth-touch emit CLOSED the silent-"Disconnected" residual [0]); a working
revoke on any later connect self-heals it. The smallest population this product will ever have (a capped
legacy upgrader whose self-revoke persistently fails with notifications off); the four-reduction ceiling was
deliberately not spent chasing it further. Wire-observation of the desktop states themselves is the ledgered
client-wire-smoke (SUBSTITUTES≠SATISFIES). Recorded per the escalation doctrine: name the edge, don't keep
touching working-enough code.

Done through (merged to `main`): **EPIC 0–2, EPIC 3 (S3.1–S3.6), EPIC 4 COMPLETE — S4.1 (shell) ·
S4.2 (auth) · S4.3 (dashboard) · S4.4 (users & roles) · S4.5 (org settings + SSO) · S4.5b (CIDR
resize) · S4.6 (audit viewer) · S4.7 (onboarding funnel) · S4.8 (Round-2 walk fixes) · EPIC 5 / S5.1
(tunnex CLI) · EPIC 6 S6.1 (client shell) + S6.2 (renderer transport — tenant-functional).**
**RE-ENTRY CHECKPOINT — S7.3 MERGED (PR#17, merge sha 5e9838a)** — device posture: an org-level
approval gate (org setting `device_approval` default-off, enterprise-gated; `device:approve` owner+admin;
self-approve DISTINCTLY audited `device.self_approved`) + **F1-part-3 org-wide push** (device Create /
Revoke / Approve / Reject ALL push org-wide, not own-node — Revoke→org-wide is the SECURITY fix for the
address-reuse privilege leak) + the migration-surface **reduction arc** (4 reductions + 1 legibility emit:
scan deletion → one-time reconnect → revoke-first → outcome-degrade → `migrate_failed` legible state).
**BOX-PROVEN ON A LIVE TWO-GATEWAY WIRE (2026-07-14):** Legs 1/2/3/4 green (pending=no-peer/no-ping/no-rule;
approve push Δ0.21s<5s; reject→IP-freed→reused; flip-ON grandfathers 0% loss) + **Leg 3b F1-part-3
cross-gateway discriminator** — revoking a device homed on G2 stripped its stale `saddr S daddr T` grant
from the NON-hosting gateway G1 in **0.236s** (own-node push would leave it → the loop would hang) + reused
IP → `default_drop`, leak closed. G2 (2nd node-agent) LEFT STANDING as a live two-gateway env for S7.4 +
the deferred client-wire-smoke + dogfooding. Client legs (connect-gate / re-mint / `migrate_failed`)
ledgered SUBSTITUTES≠SATISFIES (66 client unit tests substitute; wire proof deferred → packaged-client
smoke OR next desktop session). 5 review/confirm passes total; the collapse-arc's terminal form
(degrade-on-outcome-not-error-type) recorded as the S7.4 first-reach heuristic. EPICs 0–6 COMPLETE + EPIC 7:
S7.1 + S7.2 + S7.3 MERGED. **S7.4a (Zero Trust admin UI) MERGED (PR#18, merge sha 7402e5b)** — the Access
page (rules builder + mode toggle w/ count-confirm + FOLDED-IN device-approval queue), web-only consumption
of the S7.1–S7.3 backend; box-walked on the live two-gateway enterprise env (mode+count · post-hoc affected ·
create · approve · delete · **D-a5 edit gap-free — WIRE-PROVEN `1→2→1` on the nft ruleset** (create-before-
delete; never `1→0→1`) · notices legibility [Amendment-A unit-covered via the `sectionRender` [291] red;
live-force optional] · failure leg [E is client-side `loadOne`, unchanged by the hotfix] · member gating). Review
arc = story-end → fold-1 (loadOne legible-loads) → fold-2 (pure `accessView` gating + compose-not-compete) →
round-3 (Esc drop) → budget-escalation → **notices reduction (single-source-of-truth `staleRuleIds`)** →
clean. **HOTFIX MERGED — `fix/audit-nil-metadata` (PR#19, 28a388e):** audited DELETE 500 (audit_logs.metadata
nil→NULL 23502) fixed; surfaced by S7.4a's walk (first wire-delete of an audited entity). **S7.4b
(differentiated health surface) MERGED (PR#20, merge sha 6aa0fad)** — Option X built: `policy_degraded_kind`
advisory over the authoritative `policy_degraded` bool, from ONE compute (`PolicyHealthForNodes`); the
CP-owned `policy_desync_since` (0021) stamped at report-ingest (single-writer `trackDesync`, CP clock) +
`policy_reported_at` (0022) as the REPORT-freshness clock; `desync_unknown` a first-class honest state;
T=F=2R=60s. Box-walked on the two-gateway wire (boot-log · converging no-false-alarm · desync_unknown via
`docker stop g2`+forced-mismatch · matched-silent→healthy · bool/kind flip together = the collapse live).
Review arc: story-end (9, incl. the kind-less-alarmed-than-bool class) → fold (collapse + real freshness clock
+ log-not-swallow) → confirm (4, all hygiene/accept) → clean. **S7.4c (enterprise-e2e enabler, UN-DEFERRABLE) —
BUILT + BOX-VERIFIED, awaiting review + merge sign-off.** Commit-one dispositioned in `docs/S7.4c-decisions.md`
(D-c1 separate job · D-c2 two-tier: S4.5 payload BLOCKING Go httptest in gates + opportunistic Playwright ·
D-c3 seed-enterprise composes on `seed` · D-c4 VERIFIED wrinkle-evaporates: orphan check is a pure DB read, no
CI agent · one PR). Delivered: `cmd/seed-enterprise` + `make seed-enterprise` (sealed SSO config + gateway node
row + device holding a pool IP), blocking `TestGetSsoConfigPayloadCarriesNoSecret`, `settings.enterprise.spec.ts`
(real S4.5 payload + live-shrink S4.5b 409), `e2e-enterprise` CI job. Local box-walk on a fresh enterprise
stack: both seeds green, both Playwright legs PASS, skip-guard skips them in the open job, both editions build.
S4.5 + S4.5b ledgers flipped SUBSTITUTE→SATISFIED (sha finalized at merge). If this pointer disagrees with the
git log, TRUST GIT (`git log --oneline -20`) and update this line.

## Armed Guards (living inventory — "what protects us")
Each has been demonstrated to *fail* on a real violation during its story's DoD.
Seed for the eventual SECURITY.md.
- **Query-lint / org_id** (`db/querylint_test.go`) — tenant-owned-by-default (tables derived from migrations, `globalTables` allowlist); every tenant table query must scope by `org_id`.
- **Query-lint / deleted_at** — soft-delete tables must filter `deleted_at IS NULL`.
- **Trigger schema check** (`db/schema_test.go`) — every `updated_at` table has the `set_updated_at` trigger.
- **audit_logs append-only** — DB triggers reject UPDATE/DELETE/TRUNCATE; actor FK to `users` enforces attribution.
- **audit metadata never-NULL** (hotfix `fix/audit-nil-metadata`; `TestAuditedDeletesPersistMetadata`, red-on-main) — `audit_logs.metadata` is `NOT NULL`; the policy `writeAudit` helper must default a nil meta to `[]byte("{}")`, never a nil `[]byte` (pgx sends nil as SQL NULL → 23502). Demonstrated-red: it used `var raw []byte`, so EVERY audited DELETE (`group.deleted`/`resource.deleted`/`policy.rule_deleted`) 500'd + rolled back — undeletable rules/groups/resources — surfaced only when S7.4a's UI first deleted an audited entity on the wire. **BOX-PROOF CONVENTION (new):** every audited MUTATION CLASS (not just create) gets one wire execution in its story's box proof — a create-only proof let this live across S7.1/S7.2/S7.3. **UNIT-TEST GAP:** the policy integration suite tested create/mode/push but never an audited delete; the red test closes it. **LEDGER (S11-class, swallowed-500 logging gap):** the handler wrapper maps a raw error → `500 internal_error` WITHOUT logging the wrapped cause — the http_request line showed only `status:500`, so the DB error (23502) was invisible until reproduced via the DB directly. The `internal_error` path MUST log the wrapped cause WITH the `request_id` (diagnosis-from-logs, not from a repro). → S11 (production hardening / observability). **REVIEW-PASS WAIVER (recorded, NON-PRECEDENT):** merged `fix/audit-nil-metadata` (PR#19) on CI-green without a multi-finder review pass — scoped to THIS hotfix only (1-line change, red-proven on the real schema, wire-confirmed 23502, sweep-complete). Not a precedent for feature work.
- **Codegen drift guard** (`make generate-check`) — spec/generated code can't diverge.
- **Edition build+test** (`make build-editions` / `test-editions`) — open and enterprise builds both compiled & tested; neither rots.
- **e2e correlation** (Playwright) — SPA→API `X-Request-Id` chain asserted end-to-end.
- **RBAC matrix** (`rbac_test.go`) — executable privilege-escalation spec.
- **Restart-persistence + fail-loud secrets** (S0.3) — master key never silently regenerates.
- **Reconcile idempotence** (`reconcile_test.go` `TestReconcileIgnoresRoamedEndpoint` + `wg_dataplane_e2e.sh`
  stability sample across ≥2 intervals) — the node-agent dirty-check keys on stable identity (pubkey +
  allowed-ips), NOT the roaming endpoint, so steady-state reconcile is a byte-stable no-op; and
  `wg syncconf` echoes the key + port so it can never wipe the interface. Demonstrated-red: the POC
  itself (wg0 key→`(none)`, port randomized every cycle) was the failing case. Gated in CI via
  `make test-node`.
- **Edition build-constraint isolation** (S7.1; `go list -deps ./apps/api/cmd/server | grep -c
  enterprise/policy` == 0, asserted in CI) — the open build's server binary must NEVER link the
  `//go:build enterprise`-tagged policy engine. Demonstrated-red: the enterprise policy package linking
  into the open `cmd/server` (neutral DTOs live in `internal/policyspec`; the boundary is the guard).
- **Policy schema cascade FK** (S7.1) — deleting a group / resource / membership cleans its dependent
  policy rules + group memberships via `ON DELETE CASCADE`, so no rule can reference a vanished subject
  or destination (no dangling grant). Demonstrated-red in the S7.1 policy-model tests.
- **Canonical-hash twin goldens** (S7.2; `policyspec` hash_test.go ≡ `nodepolicy` nodepolicy_test.go,
  identical fixtures + expected hex in BOTH modules — the cross-module drift guard) — the compiled-policy
  hash the control plane computes must byte-match what the agent computes. Demonstrated-red: the first
  impl hashed the RULESET TEXT (node-local masquerade subnet the control plane can't reproduce) →
  permanent false staleness.
- **Multi-node push-target** (S7.2; `TestDeactivatePushesOrgWideNotJustUserNodes`) — a member
  deactivation must push EVERY active org gateway, not just the ex-member's own device-nodes.
  Demonstrated-red (F1-part-2): the /32-sweep was proven at the model layer but the push TARGETING was
  not — on a multi-gateway org a node hosting another user's device that referenced the ex-member as a
  policy destination wouldn't be pushed <5s.
- **Fail-closed cold-start** (S7.2; `TestNeverReceivedIsDenyAllNotMesh`) — a gateway that has never
  received a policy renders DENY-ALL regardless of mode, never the blanket mesh. Demonstrated-red: a
  restart re-armed the blanket mesh under enforcing (fail-OPEN) until the first fetch.
- **Refuse unknown / half-spec, never widen** (S7.2; `TestRenderAllowHalfSetPortRangeFailsClosed` +
  `TestRenderAllowUnknownProtocolFailsClosed` + `TestValidateResourcePortsBothOrNeither`) — the
  compiler/renderer skip a malformed AllowEntry (→ default-deny), never widen on it; validation rejects
  it at the API. Demonstrated-red TWICE: a half-set port range widened to all-ports; an unknown protocol
  widened to all-protocols. (Checklist line for every new AllowEntry field.)
- **ProtocolVersion equality** (S7.2; `TestProtocolVersionConstantsAgree`) — `nodes.ProtocolVersion` ==
  `policyspec.ProtocolVersion`, so a fail-closed fallback artifact's canonical hash can't fork from the
  compiler's. Demonstrated-red: the two independent constants (both 1) diverging would false-alarm every
  enforcing gateway on the fallback path.
- **policy_degraded gap-state red** (S7.2; `TestPolicyDegraded` stuck-enforcing case) — a gateway that
  failed to apply an off/mesh ruleset and is still enforcing a DISABLED policy (applyErr set,
  failingSince empty, synced-would-be-true) MUST read `policy_degraded=true`. Demonstrated-red: this
  exact green-while-blackholing state survived review passes 2, 3 AND 4 across the 3→2-field staleness
  surface before the collapse to one conservative field closed it.
- **Device active+pending accounting convention** (S7.3; `CountDevicesForUserCap` + its pin test) — a
  `pending` device is EXCLUDED from enforcement (peer + compiler filters key on `status='active'`) but
  INCLUDED in resource accounting: the per-user cap, the IP pool, and node-sweeps all count active+pending.
  Demonstrated-red: cap counting active-only let a user enroll past the cap by stacking pendings (a free
  DoS on the address pool); the fix counts both. The taxonomy: **exclude from what grants access, include
  in what consumes resources.**
- **Partial-unique-index ⊇ allocator domain** (S7.3; migration 0020 widened `devices_org_ip_key` to
  `status IN ('active','pending')`) — the partial unique index on `(org_id, assigned_ip)` must cover EVERY
  status the allocator can hand a live IP to. Hazard: an index narrower than the allocator's domain (index
  on `active` only, allocator also assigns to `pending`) lets two pending devices collide on one IP with no
  DB guard. Checklist line for any new status that can hold an `assigned_ip`.
- **F1-part-3 org-wide push on every membership-changing lifecycle event** (S7.3; device
  Create/Revoke/Approve/Reject → `PushOrgNodes`, wire-proven Leg 3b) — any device event that changes
  compiled policy membership pushes EVERY active org gateway, not the device's own node. Demonstrated-red
  ON THE WIRE: revoking a device homed on G2 left its stale `saddr S daddr T` grant on the non-hosting
  gateway G1 under own-node push; org-wide push strips it (0.236s box-measured). Revoke→org-wide is the
  SECURITY fix (address-reuse privilege leak: a reused IP would inherit the revoked device's grants).
  Generalizes the S7.2 multi-node push-target guard to the device-lifecycle surface.
- **Two-layer pending exclusion** (S7.3) — a `pending` device must be dropped from BOTH the peer set
  (`ListActivePeersForNode` → no wg peer/tunnel) AND the compiler input (no grants) by the same
  `status='active'` filter. Box-proven Leg 1: pending = no wg peer + no ping + no allow rule. Single-layer
  exclusion (peer-only) would arm a tunnel with no policy (or vice versa).
- **openapi-fetch no-throw legibility — `loadOne` (S7.4a; `apps/web/src/lib/api.ts`, class-guard tests
  in `policyview.test.ts`)** — openapi-fetch is a STANDING FOOTGUN: it returns `{data:undefined, error}`
  on a non-2xx (does NOT throw) and REJECTS on a network failure, so a component that reads only `data`
  renders a REASSURING EMPTY state for a real failure (a failed rules load → "No rules"; a failed members
  load → a false "not an admin" lockout). **SANCTIONED CALL PATTERN:** a raw `api.GET` in a component whose
  emptiness is user-meaningful (a list, a role, a count gating a destructive action) is **review-refused** —
  route it through `loadOne`, which collapses both failure paths into a discriminated `Loaded<T>` so the
  caller renders a legible "failed — retry", never absence. Demonstrated-red: the S7.4a story-end review's
  dominant cluster — 6 sections each swallowing their fetch error into a reassuring default (the exact
  failure-must-be-legible invariant, applied to referents but not the loads themselves). **Carry into S7.4b
  (the health-badge fetch) and every later web surface.**
- **Terminal-migration outcome-degradation** (S7.3; client `migrateLegacyConfig` revoke-first + the
  ipc bare-catch degrade + `migrate_failed` synth state; reds in `deviceconfig.test.ts` + `uxwiring.test.ts`)
  — a legacy-config migration has EXACTLY TWO bounded outcomes, degraded on OUTCOME not error type: completed
  → `migrated`; failed-for-any-reason → config KEPT + the legible `migrate_failed` down-state. Structurally
  NO path from a failed migration to a raw renderer reject or an unbounded loop. Demonstrated-red across the
  reduction arc: revoke-first fixed a cap-lockout; the bare-catch removed the raw-reject; the `migrate_failed`
  emit removed the silent-"Disconnected" on notif-muted machines. The doctrine (collapse N error paths to one
  outcome-degraded down-state) is the S7.4 first-reach heuristic.

## Edition Model — Open-core (resolved)

> **⚠️ SUPERSEDED (pending EPIC 12 / S12.1 decision review):** the build-tag edition split
> below is **superseded IF the commercial-upgrade flow (EPIC 12) is built.** Requirement: an
> open-build customer pastes a license key into the RUNNING deployment and enterprise features
> unlock — **no rebuild, no redeploy.** That is impossible with the build-tag split (enterprise
> code isn't compiled into the open binary), so **EPIC 12/S12.1 refactors to a single binary,
> runtime license-gated** (GitLab-EE model). **CONSEQUENCE, accepted knowingly:** enterprise
> source then ships inside the open binary — readable, and the license check is patchable (it's
> open source). Piracy isn't prevented; honest commercial compliance is made easy, backed by
> license law. This **invalidates the test-editions "enterprise-not-in-open-binary" property** —
> S12.1 replaces that guard with a runtime-gating guard. **PARKED:** not built until after the
> public beta; decide-before-code review required at S12.1. Until then the build-tag model below
> stands as-is.

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
  **DoD — REMOVE THE CRUTCH: S3.7 replaces and DELETES `scripts/poc-gateway-nat.sh`** (the throwaway
  POC NAT) and folds the `sysctls: net.ipv4.ip_forward=1` POC line in `docker-compose.yml` into the
  agent's own probed setup. The hand-hacked POC egress must not outlive the real feature.
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
- **S4.5 Org settings & SSO config UI** — connect Google/Microsoft, domain-capture rules. **Delivered (org settings + SSO config only; CIDR resize split to its own story):** SSO secret is WRITE-ONLY (GET returns a keyed HMAC fingerprint, never the secret — no `client_secret` field in the response type); config writes are audited (`sso.config_updated`, actor-attributed, secret-free metadata); open builds refuse SSO-config endpoints with 403 `edition_required` (the established precedent, not 404); the client RBAC mirror is now GENERATED from the Go grant table (drift = red build). **Deferred tests — SATISFIED (S7.4c, sha TBD-at-merge):** the payload-level "GET has no secret" assertion now runs BOTH as a BLOCKING enterprise Go httptest (`TestGetSsoConfigPayloadCarriesNoSecret` in `make test-editions` — a security assert must gate, not sit behind continue-on-error) AND as an opportunistic enterprise Playwright leg (`settings.enterprise.spec.ts`, E2E_EDITION=enterprise, seeded SSO config → real 200 payload: fingerprint present, no `client_secret`). The old open-edition 403-gate check (settings.spec.ts:25) is retained + demoted-with-pointer as the OPEN substitute. ~~blocked because the e2e stack builds the open edition~~.
- **S4.5b CIDR resize** (split from S4.5) — resize the org WG pool. **Delivered:** `PUT /organizations/{orgId}/pool-cidr` (edition-neutral — allocator is core/open); grow-superset / shrink-subset only (else `illegal_resize`), identical CIDR = idempotent 200, `< /30` = `cidr_too_small`; canonical (masked) CIDR stored/audited. Shrink that would strand allocations → structured 409 `{orphan_count, orphans[≤20]{device_id,name,assigned_ip,reason}}`, reason = `out_of_range | reserved_collision` (ipalloc.Orphans, reserved-collision-aware, single-read so check == 409 objects). Check runs UNCONDITIONALLY (check-anyway) — provably empty on a valid grow, a backstop if a non-Allocate writer breaks the invariant. Atomic + audited (`org.cidr_resized`, no row on no-op) under the shared per-org `LockDeviceKey`; `TestResizeAllocationRace` proves the lock excludes a concurrent allocation (red-without-lock demonstrated). **Deferred test — SATISFIED (S7.4c, sha TBD-at-merge):** the 409 orphan-list UI render now runs UN-MOCKED against a live shrink in `settings.enterprise.spec.ts` — `seed-enterprise` seeds a device holding `10.99.0.200`, a shrink to `/25` strands it, and the REAL 409 body renders (device name + `out_of_range`). Verified D-c4: the orphan check is a pure DB read (`ListActiveDeviceAllocations`), so a plain seeded node ROW satisfies the device's `node_id` FK — NO enrolled agent needed. The MOCKED render (settings.spec.ts) is retained + demoted-with-pointer as the OPEN substitute. ~~needs an enrolled gateway the open e2e stack lacks~~.
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

- **S6.0b CI pipeline (verification gates + client build matrix)** — IN PROGRESS. The repo's gates
  ran only via manual `make`/`turbo` (no `.github/workflows`); the Electron client adds a macOS +
  Windows surface a human can't reliably cover. **Scope:** GitHub Actions on push/PR —
  (i) a **Linux `gates` job** running the existing make gates (codegen drift, both-edition tests,
  web typecheck+build); RED BLOCKS MERGE. (ii) a **`client` matrix** (macOS + Windows runners):
  `pnpm install` (electron provisioned via the onlyBuiltDependencies allowlist), client
  typecheck + unit tests + build — none LAUNCH Electron (no display needed), so the matrix is
  display-free; RED BLOCKS MERGE. (iii) **full e2e in CI is OPPORTUNISTIC** — included as a job, but
  if it resists (runner resources / flakiness) it drops to nightly/non-blocking (ledger). (iv)
  **playwright-electron** is NOT in scope now — launching Electron needs xvfb + the built app + the
  stack, not "trivially cheap"; ledgered for later. Must land before S6.5; recommended before S6.3.

**S6.0b LEDGER — CI first-run fixes (root-caused).** The blocking jobs (gates + client) were red on
first runs; all three causes fixed: (1) `.env` absent → `cp .env.example .env` before DB steps;
(2) a Windows-only path fixture in the client test (resolve the root per-platform); (3) THE big one —
`GOFLAGS=-mod=mod` made `go build`/`go test` RE-RESOLVE the module graph on the cold CI cache and,
because the module path (`github.com/tunnexio/tunnex/...`) is NOT the real repo
(`github.com/iotunnex/tunnex`), it ran `git ls-remote https://github.com/tunnexio/tunnex` → exit 128.
It surfaced only after the repo went public (the mismatched path suddenly looked resolvable) and did
NOT reproduce locally (populated cache). Fix: `-mod=readonly` (go.sum is committed + complete) on the
apps/api build/test/seed commands + the api/migrate/node Dockerfiles — go then trusts go.mod/go.sum
and never remote-resolves. **`e2e` stays non-blocking `continue-on-error` by design** (full-stack,
heavier/flakier; opportunistic per mandate), not because it's broken — with the readonly fix it is
expected to pass. Gates + client are the blocking gates.

### S6.0b decide-before-code (COMMIT ONE, for review): deliberate-red representation in CI
The story-protocol proves each new guard by a DELIBERATE RED — comment out the guard, watch its test
fail, record the one-line failure in the commit — then restore green. **Decision: CI runs the GREEN
suite only; deliberate-reds stay MANUAL, dev-time, recorded in commit messages.** A red is produced
by committing *broken* code (a removed guard); CI cannot host that without a permanently-failing job,
and a "red on a branch that removes the guard" is exactly what a human does locally, not a committed
artifact. What CI guarantees instead: the GREEN test each red proves (401-walk, RBAC matrix, the
sweep tests, the no-oracle byte-identical test, CORS no-credentials, bearer session_required, …) runs
on every push and RED BLOCKS MERGE — so a regression that would re-open the hole fails CI even though
the *red demonstration* isn't itself a CI job. The deliberate-red remains the AUTHOR's proof the test
detects the violation; CI is the CONTINUOUS proof the invariant still holds. (Argue if a subset of
reds should be encoded as committed "guard-present" assertions — none proposed; the green tests
already assert the positive invariant.)

### S6.3 Tunnel control — DECIDE-BEFORE-CODE (privilege helper gets a FULL review round)
Scope: start/stop a WireGuard tunnel from the desktop app — embed a userspace WireGuard
(`wireguard-go` on macOS, `wintun`/`wireguard-nt` on Windows) and configure the interface, which
needs elevated privilege. The **privilege helper is the heavyweight, security-critical item** and
its architecture must be reported for review BEFORE any code, covering FOUR decide-items:
1. **Minimum surface** — exactly what the helper does (bring an interface up/down with a specific
   config; set routes/DNS) and, more importantly, what it REFUSES. No arbitrary config path, no
   shell, no generic "run as root" — a typed, minimal verb set mirroring the preload allowlist
   posture (S6.1). The helper is the privileged trust boundary; its surface IS the attack surface.
2. **Caller authentication** — how the helper knows it is talking to the REAL Tunnex app and not
   another local process (code-signing identity / audited client requirements on macOS XPC; a
   signed-peer check on the Windows service pipe). A root helper that trusts any local caller is a
   local-privilege-escalation primitive.
3. **Install/uninstall lifecycle per platform** — macOS `SMAppService` (or a LaunchDaemon) register/
   unregister; Windows service install/remove — idempotent, clean uninstall (no orphaned root
   daemon), and how it is signed/notarized (ties to S6.5).
4. **Why NOT wireguard-tools-as-root** — argue the baseline explicitly: why the app does not simply
   shell out to `wg-quick`/`wg` under sudo/elevation (auditability, surface, credential prompts,
   packaging), justifying the dedicated minimal helper instead.
Standard protocol otherwise: decide-items reported for review → build → multi-finder + the security
review → e2e where the harness allows + human smoke for tunnel-up/down.

#### S6.3 COMMIT ONE — privilege-helper architecture (PROPOSED, for review before any code)

**HEADLINE TENSION (decide first):** robust caller-authentication (item 2) and a trusted
daemon/service (item 3) BOTH rest on code-signing, which is now DEFERRED to S6.5b. So a *cryptographically*
authenticated helper cannot be fully realized on unsigned builds. Two paths — pick one:
- **(A) Build now, auth hardens later.** Ship the helper + its typed protocol on unsigned dev/S6.5a
  builds with an INTERIM caller check (install-time admin consent + client-path/bundle check), and land
  the crypto identity-pinning when S6.5b signs. Tunnel works early; the helper is only *fully* trusted
  once signed. RECOMMENDED — keeps EPIC 6 moving; the interim helper is not internet-exposed and is
  installed only by explicit admin action.
- **(B) Pull macOS signing early.** Use the individual Apple Developer ID (no legal entity needed) to
  sign the macOS app+daemon NOW so the macOS helper gets real XPC code-requirement pinning immediately;
  Windows helper still waits on the entity/EV. Splits the platforms but maximizes macOS security first.

**(1) Minimum surface.** The helper is a SEPARATE privileged process (native Go/Swift/C — NOT Electron,
NOT Node), exposing a TYPED verb set only: `TunnelUp(cfg)` · `TunnelDown()` · `Status()`. `cfg` is a
STRUCTURED, VALIDATED WireGuard config passed over IPC (never a file PATH — dodges TOCTOU/arbitrary-read):
own private key, peer pubkey, endpoint host:port, allowed-IPs, address/CIDR, DNS, MTU — each field parsed
+ rejected if malformed (valid base64-32 keys, parseable CIDRs, well-formed endpoint). REFUSES: arbitrary
interface name (one app-owned name, e.g. `utun-tunnex` / a fixed wintun adapter), arbitrary routes/DNS
beyond what the validated cfg implies, any exec/shell/file-path/"run binary", more than one concurrent
tunnel. The verb set IS the attack surface — same allowlist posture as the S6.1 preload.

**(2) Caller authentication.** macOS: helper = a LaunchDaemon exposing an XPC service; pin the peer with
`xpc_connection_set_peer_code_signing_requirement` (audit-token → SecCode → Tunnex Team ID + designated
requirement). Windows: helper = a Windows service; IPC over a named pipe with a tight ACL; resolve the
client PID (`GetNamedPipeClientProcessId`) → verify the client image is the signed Tunnex exe
(`WinVerifyTrust` + path). BOTH depend on signing (see the headline tension) — on unsigned builds the
interim is bundle-path + explicit-install consent, upgraded to crypto pinning at S6.5b. A root helper
that trusts ANY local caller is a local-EoP primitive; that is the failure mode we design against.

**(3) Install / uninstall lifecycle.** macOS: `SMAppService.daemon` (macOS 13+) registers a LaunchDaemon
bundled at `Contents/Library/LaunchDaemons/` — one admin auth on first tunnel use; `unregister()` on app
removal; idempotent; NO deprecated `SMJobBless`. Windows: register the service via the SCM through a
ONE-TIME elevated install action (runs as LocalSystem); uninstaller STOPS + DELETES the service (no
orphaned LocalSystem daemon); idempotent check-then-create. Both binaries must be signed/notarized for the
OS to load them (→ S6.5b).

**(4) Why NOT wireguard-tools-as-root (the rejected baseline).** Not `sudo wg-quick up <file>` because:
(a) surface — `wg-quick` is a root shell script invoking `ip`/`route`/`resolvconf`; a config file handed
to root is a fuzzy, injectable surface vs. a fixed typed verb set; (b) UX/security — `sudo` either
password-prompts every connect (bad) or needs a NOPASSWD sudoers entry (a standing root hole any local
process can abuse); the helper authenticates the CALLER once at install instead; (c) cross-platform —
`wg-quick` is unix-only; Windows needs the service/wireguard-nt model regardless, so a unified helper
abstraction is required anyway; (d) versioning — embedding `wireguard-go`/`wireguard-nt` pins a known-good
implementation rather than depending on a possibly-absent/old system `wireguard-tools`; (e) TOCTOU — a
file-path arg to root invites time-of-check/use races; structured IPC config avoids it.

Report: decisions above for review (esp. the A/B signing-tension call) BEFORE any code.

**COMMIT-ONE AMENDMENTS — PATH A APPROVED (build now, harden at S6.5b), with:**
- **Interim caller-check (unsigned builds) = executable-path-inside-install-dir verification.** The
  helper resolves the connecting client's executable path (macOS: audit-token → PID → path; Windows:
  `GetNamedPipeClientProcessId` → image path) and requires it to live INSIDE the app's install dir
  (`/Applications/Tunnex.app/…`, `C:\Program Files\Tunnex\…`). RECORDED AS WEAKER-THAN-PINNING —
  THREAT MODEL: it stops an unrelated local process from driving the helper, but does NOT stop a
  process that can write into / replace a binary in the install dir (needs admin already) or a
  path-spoofing race; a non-admin local attacker is blocked, an admin-level one is not. Real crypto
  identity pinning lands at S6.5b (mode upgrade, below).
- **Wire protocol carries `version` + `auth_mode` from day one.** Every request/response header
  includes a protocol version and the auth mode in force (`path_check` now, `code_signing` at S6.5b).
  So S6.5b hardening is a MODE UPGRADE negotiated on the existing protocol, NOT a breaking change —
  the app and helper agree on the strongest mutually-supported mode; the helper REFUSES to downgrade
  below its configured minimum once signed.
- **Fail-CLOSED on helper death (CONFIRMED, no deviation).** If the helper dies / the IPC channel
  drops while a tunnel is up, tunnel traffic FAILS CLOSED — the tun interface + its routes are torn
  down (or a kill-switch route/deny stays installed) so NO traffic silently falls back to the
  cleartext default route (no leak). The UI surfaces the drop LOUDLY (disconnected + reason), never
  a silent degrade. Rationale: a VPN client that fails OPEN leaks the exact traffic the user meant to
  protect; closed-on-failure is the only defensible default. (Any future opt-in "allow fallback" would
  be an explicit, off-by-default user choice — not in scope here.)
- **PLAN ledger:** the interim path-check posture is a NAMED SECURITY LIMITATION (below), trigger to
  retire = S6.5b crypto pinning.

**S6.3 ConfigProvider — DECIDE-BEFORE-CODE (D2-honoring; report for review before the config commit).**
The `TunnelController` needs a `ConfigProvider` that yields the device's WireGuard `TunnelConfig` in
MAIN. It MUST honor Round-2 walk decision **D2**: the config is served EXACTLY ONCE at device creation
and is NEVER re-fetchable, so the client must OWN device creation (as the CLI does) — it cannot "fetch
the config for a device." Proposed decisions:
1. **Own creation, once.** First tunnel-up with no stored device → the desktop CREATES a device via
   the API (bearer, in MAIN — reusing the S5.1/S3.4 device-create-returns-config flow) and captures
   the config at that moment. It NEVER attempts to re-fetch an existing device's config (the API
   forbids it). Subsequent ups reuse the stored config.
2. **Secure storage, key-never-in-renderer.** The WG PRIVATE KEY + config persist via Electron
   `safeStorage` (macOS Keychain / Windows DPAPI) — the SAME refuse-by-default posture as the S6.1
   credential (no plaintext-on-disk unless an explicit `--allow-insecure-credential-storage`). The key
   flows API → MAIN (safeStorage) → helper (IPC); it NEVER enters the renderer. This deliberately
   AVOIDS the browser flow's mistake (a plaintext key in ~/Downloads) that D2 called out.
3. **One device per install**, named from the hostname (with a disambiguating suffix); the device id is
   persisted alongside the config.
4. **Lifecycle on logout — CONFIRMED DELIBERATE (logout revokes the device).** Clearing the
   credential (auth:logout) ALSO clears the stored tunnel config and BEST-EFFORT revokes the device
   server-side. ARGUMENT (one line): the local WG config is cleared on logout exactly like the bearer,
   so leaving the server-side peer alive would ORPHAN it (dangling peer + stale telemetry) — logout
   revokes to complete the full-sweep; re-login creates a fresh device (D2: no re-fetch).
5. **Loss = recreate, never re-fetch.** If safeStorage is cleared/unavailable, a NEW device is created
   (old one is orphaned → the logout/GC sweep or an admin reap handles it); consistent with D2.
6. **Server-URL change — RESOLVED: NO auto-revoke.** The stored config is ORIGIN-KEYED (like the
   bearer) and NEVER used cross-origin — a URL change simply means the new origin has no config yet
   (a fresh device is created on next connect there). The old-origin device is NOT auto-revoked
   (avoids destroying a working config on a fat-finger URL edit / temporary switch); instead the UI
   SURFACES the orphaned old-origin device with a "remove or switch back" affordance, and remove does
   a best-effort revoke against the OLD ORIGIN ONLY (never the current one). This is the deliberate
   divergence from S6.2's force-relogin-on-URL-change: the credential is discarded, but a device
   (server-side state + a stored config) is worth preserving/surfacing, not silently reaping.

**S6.3 KILL-SWITCH DESIGN — BEFORE-CODE (review item at story end; pcap-verified smoke step).**
THE INVARIANT: fail-closed must require NO LIVE CODE to act. The app is unprivileged (can't fix
routing); the helper can be `kill -9`'d, which runs NO cleanup handlers. So fail-closed CANNOT be a
`FailClosed()` method that runs on death — it must be KERNEL-RESIDENT STATE the helper ARRANGES AT
`Up` that BLOCKS cleartext egress and PERSISTS however the process exits. **Death itself is the
enforcement.** Only a graceful `Down` removes it. This corrects the current Supervisor: `Up` installs
the persistent block; `Down` removes it; the live `FailClosed()`/`OnPeerLost()` path is a fast-teardown
CONVENIENCE for the alive-process case, NOT the guarantee. On next helper start a STALE block from a
prior crash is reconciled (adopt on reconnect, or an explicit user-driven clear so a crash can't
permanently black-hole the internet — but the DEFAULT post-crash state is blocked).
- **macOS:** a `pf` (packet filter) anchor installed via `pfctl` at `Up` — rules that block all
  outbound except to the WG endpoint + via the utun. `pf` rules are kernel-resident and survive helper
  death; graceful `Down` flushes the anchor. (Route-only blackholing is fragile across utun teardown;
  pf is the durable mechanism.) RULESET REQUIREMENTS (folded, S6.3-17): (1) pf enabled via
  reference-counted `pfctl -E`, token RELEASED with `pfctl -X` on Down (never a global `pfctl -d`) —
  smoke asserts ENFORCEMENT (a blocked ping), not rule presence; (2) `set skip on lo0` (loopback
  exempt — also protects the app's own 127.0.0.1 callback); (3) DHCP + NDP pass (UDP 67/68, DHCPv6
  546/547, ICMPv6/NDP) — a DELIBERATE, threat-model-argued exception so a long session doesn't lose
  its lease/neighbor state (exposure = a local-segment attacker spoofing DHCP/RA, out of scope for an
  egress kill-switch and a pre-VPN risk anyway); (4) `block drop out all` covers inet AND inet6 (NDP
  explicitly passed) — the smoke kill-switch pcap includes a v6 probe. The named anchor must be
  REFERENCED from pf.conf to be evaluated — the SMAppService/installer adds `anchor "tunnex"` (removed
  on uninstall); the enforcement-based smoke catches a non-referenced anchor.
- **Windows:** WFP (Windows Filtering Platform) filters in a PERSISTENT sublayer at `Up` — the same
  mechanism the official WireGuard Windows client uses for its kill-switch ("block untunneled
  traffic"). WFP filters are kernel-resident and persist past process death; graceful `Down` removes
  the provider/sublayer.
Backend contract (corrected): `Up(cfg)` = tun + routes + ARRANGE the persistent pf/WFP block;
`Down()` = remove tun + REMOVE the block (restore routing); `FailClosed()` = alive-process fast path
that tears the tun and ASSERTS the block is present (it already is from Up). SMOKE (both platforms):
`kill -9` the helper mid-tunnel; a pcap on the physical NIC proves ZERO cleartext to a tunneled dest
AFTER the kill — with the helper process GONE, so nothing but pre-arranged state can be enforcing it.

**RECOVERY MODEL — BOUNDED FAIL-CLOSED (mini-smoke-surfaced; implemented + tested).** The design above
("death = enforcement, only graceful Down removes it") is correct for BLOCKING but originally had NO
RECOVERY PATH: an abnormal exit (kill -9 / crash) left the kernel-resident block with nothing to release
it, so a FULL-TUNNEL helper death STRANDED THE HOST (reboot required — the first mini-smoke did exactly
this, against the no-egress parked-S3.7 gateway). Fail-closed is now **"death = enforcement, BOUNDED by
the dead-man interval."** Three recovery mechanisms, all landed with tests (`TestSupervisorSelfHeal`,
`TestSupervisorDeadMan`): (1) **STARTUP SELF-HEAL** — the helper flushes a stale `tunnex` anchor +
releases a PERSISTED (root-only `/var/run/tunnex/pf.token`) `pfctl -E` reference BEFORE serving, so a
KeepAlive restart un-strands; (2) **DEAD-MAN TIMEOUT** (`DeadManDefault` = 90s) — if the owning app
stops heartbeating past the window (crashed/wedged), the LIVE helper auto-releases the block; (3)
graceful `Down` (unchanged). **MAX CLEARTEXT-LEAK WINDOW after an un-recovered crash = the dead-man
interval (~90s) — a DELIBERATE trade: an UNBOUNDED block bricks the host, worse than a bounded post-crash
leak window on a machine whose VPN is already down.** ROUTES (RC2): full-tunnel now installs the
WG-standard SPLIT-DEFAULT (`0.0.0.0/1`+`128.0.0.0/1`, `::/1`+`8000::/1`) — more specific than the
physical default so it takes precedence WITHOUT destroying it; on teardown/crash the halves vanish with
the utun and the physical default resurfaces automatically (no capture/restore, no stranding). **WINDOWS
WFP MUST INHERIT THIS BOUNDED MODEL** — WFP filters have the IDENTICAL latent persist-with-no-releaser
bug. The WFP backend must implement the same `CleanStale` (startup sweep of stale filters by a well-known
provider/sublayer GUID) and be driven by the same dead-man, or it will strand Windows hosts identically.
Build it bounded from day one — do not port only the arming half.

**KILL-SWITCH VALIDATION STATE (2026-07-09, after the POC mini-smoke sessions):**
- **PROVEN LIVE (real macOS hardware):** (a) full-tunnel routing loop FIXED — endpoint host-route
  via the physical gateway, `tx` steady not runaway; (b) HOST-STRANDING RECOVERY confirmed live via
  Ctrl-C graceful Down (network returns, no reboot) — RC1/RC2 work on real hardware, not just in unit
  tests; (c) generator emits both AFs; (d) dev-install one-shot (codesign + Electron-path auto-detect
  + stale-config self-heal).
- **PROVEN (unit):** self-heal + dead-man release, both paths independently (`TestSupervisorSelfHeal`,
  `TestSupervisorDeadMan`); split-default mapping (`TestRouteTargets`).
- **PROVEN LIVE (2026-07-09, on real macOS) — GATE CLEARED:** the `kill -9` pcap PASSED. Full-tunnel
  up, `kill -9` the helper, `en0` capture over the dead window: BOTH pcaps (v4 `1.1.1.1` + v6
  `2606:4700:4700::1111`) showed **0 packets** while ~30 ping attempts fired — the kernel-resident pf
  anchor blocked every one with the helper PROCESS GONE ("death = enforcement"). BONUS: the manual
  recovery command errored (zsh inline-comment), yet the host STILL recovered — the KeepAlive restart +
  startup `CleanStale` self-healed AUTOMATICALLY (RC1 self-heal now live-proven, not just unit-tested).
  No strand, no reboot. **WFP is UNBLOCKED.** (Windows WFP still needs its OWN Windows-side proof at its
  story-end — a macOS proof validates the PATTERN, not WFP's kernel mechanism — but the bounded model
  is now confirmed sound on real hardware, so WFP is built against a proven pattern.)
- **PARKED AS ITS OWN STORY:** gateway NAT / full-tunnel real internet egress (the `rx=92` container
  double-NAT issue) is **S3.7** — do NOT hand-hack it live; the POC's manual iptables was a throwaway.

**S6.3 native deps (pinned; license check):** macOS tun/device = `golang.zx2c4.com/wireguard`
(wireguard-go) — **MIT**, compatible under our Apache-2.0 open edition (permissive → permissive, OK).
Windows = `golang.zx2c4.com/wireguard/windows` / `wireguard-nt` + `wintun` — WireGuard-NT/Wintun are
**MIT**-ish (WireGuard) with the Wintun redistribution note; wintun.dll is bundled per its license.
Exact commit/tag pins recorded in `apps/helper/go.mod` when the backends land; the license check
(MIT-under-Apache = fine; note Wintun's redistribution terms in NOTICE) is a story-end review item.

**S6.3 NATIVE LIFECYCLE — DESIGN (install/UPGRADE/UNINSTALL; uninstall is first-class).**
- **Mechanism per platform.** macOS: **SMAppService** — the app bundle ships
  `Contents/Library/LaunchDaemons/io.tunnex.helper.plist`; the Electron main calls
  `SMAppService.daemon(...).register()` (install/upgrade) and `.unregister()` (uninstall). Windows: the
  helper is a **Windows service** (SCM) — the packaged installer registers/starts it; uninstall stops +
  `sc delete`s it. Both REQUIRE the packaged app (signed `.app` / installer) — see substitutes below.
- **UNINSTALL IS A FIRST-CLASS, VERIFIED DELIVERABLE (steer).** The dev-install left `/etc/pf.conf`
  modified with no restorer — the production lifecycle must NOT repeat that class. Uninstall removes,
  per platform, ALL of: the daemon/service registration; the helper binaries; the socket/pipe; on
  macOS the `pf.conf` anchor reference **RESTORED FROM THE INSTALL BACKUP** (`/etc/pf.conf.tunnex-bak`)
  + the pf token file; on Windows **all WFP objects by our provider GUID** (`firewall.DisableFirewall`);
  and leaves **zero routes/rules**. The **story-end smoke's uninstall-residue checks are the acceptance
  test** — the lifecycle is built to pass them. Dev path already updated: `macos-dev-uninstall.sh`
  restores the pf.conf backup + cleans the token + checks split-default-route residue.
- **VERSION UPGRADE PATH (steer).** The helper is the long-lived root daemon; the app upgrades it (a new
  app version registers its bundled helper). `NegotiateVersion` (protocol.go, tested) makes the handshake
  actionable: **app newer than helper → `helper_outdated`** (app re-registers/upgrades the helper via
  the lifecycle, then retries — the normal path); **app older than helper → `client_outdated`** (REFUSE;
  a stale app must not drive a newer helper — a downgrade-refused ratchet mirroring the auth-mode one).
- **SUBSTITUTES vs SATISFIES (steer — honest split).** PROVABLE NOW (pre-packaging, this story):
  uninstall COMPLETENESS + residue logic (pf.conf restore, WFP `DisableFirewall`-by-GUID, socket/token
  removal); the version-handshake upgrade errors (unit-tested); the backend `CleanStale`/`Down` removal
  ops the uninstall relies on. DEFERS TO S6.5a (needs the packaged `.app`/installer): SMAppService
  `register`/`unregister` and the Windows-service install exercised END-TO-END, and the packaged
  install→run→UNINSTALL residue smoke. **The dev-install scripts remain the unpackaged-dev mechanism
  ALONGSIDE the production lifecycle.** **TRIGGER SPLIT (resolved at S6.3 sign-off — a proof's trigger
  must be a milestone that can actually RUN it):** the **Windows** service install→run→uninstall residue
  smoke runs on the UNSIGNED S6.5a package (a user-mode service installs without code-signing; SmartScreen
  click-through) → **trigger = S6.5a**. **macOS SMAppService** register/unregister REQUIRES a code-signed
  app bundle (SMAppService validates the signature) → it cannot run on the unsigned S6.5a package →
  **trigger = S6.5b** (signing). The uninstall REMOVAL/residue LOGIC (pf.conf restore, WFP
  DisableFirewall-by-GUID, socket/token removal, zero routes) is already dev-proven and rides S6.5a on
  both platforms; only the macOS SMAppService *registration* e2e waits for S6.5b.
Deps landed so far: `golang.org/x/sys` (caller-path), `github.com/Microsoft/go-winio` v0.6.2 (MIT —
Windows SDDL pipe).

**S6.3 Windows pipe — TWO-LAYER intent (endorsed):** the pipe SDDL gates CONNECTION (who may open
the pipe: SYSTEM/Admins full, Authenticated Users connect+rw so the unprivileged app can reach it);
the caller-path check gates TRUST (which PROCESS may drive the helper: image inside the install dir).
Access ≠ authorization — both layers required. EDGE (refuse-path): if the client process dies between
connect and resolution, `OpenProcess`/`QueryFullProcessImageName` error → the resolver returns an
error → the Server refuses the caller (fail-closed, correct). Add an explicit test when Windows tests
are runnable.

- **S6.1 Client shell** — Electron app, reuse React renderer, secure IPC, auto-update scaffold.
  **MERGED** (7 commits; smoke-verified on macOS). Delivered: `apps/client` Electron main+preload;
  `app://` (standard+secure, strict escape+symlink+realpath, CSP) serving the `apps/web` bundle;
  hardened window (contextIsolation/sandbox on, nodeIntegration off, navigation locked); preload
  verb-specific allowlist (`auth.*`/`config.*`/reserved `tunnel.*`, no generic invoke, main
  validates inputs); S5.1 login reused in main (system browser + single-shot loopback →
  `safeStorage` keychain, refuse-by-default + `--allow-insecure-credential-storage`); bearer
  attach-on-request on the exact minting origin only; `/healthz`-validated main-process server
  config with force-relogin-on-change; first-run setup screen; `electron-updater` scaffolded inert
  (`AUTOUPDATE_ENABLED=false`). 17 unit tests over the pure security core.

### S6.1 paper decisions (COMMIT ONE — decided on paper, for review before any code)

New surface (`apps/client`, Electron main + preload + the reused SPA renderer). Nothing exists yet;
this commit is the contract, not code. Grounded in S5.1: the CLI credential flow (system browser →
`127.0.0.1:<port>/callback` → PKCE code → `tnx_` bearer, header-borne, no cookies) already exists and
the desktop client REUSES it wholesale.

**(a) Auth = reuse the S5.1 credential flow via the SYSTEM browser + loopback.** The Electron MAIN
process runs the same single-shot loopback listener the CLI does, opens the user's DEFAULT browser to
`/cli-auth` (never an embedded `BrowserWindow`/webview), receives the one-time code, exchanges it for a
`tnx_` bearer credential. **No embedded-webview login, no cookies in the client.** Rationale: an
embedded webview can capture credentials and is refused by Google/Microsoft for OAuth; the system
browser + loopback is the audited S5.1 path and gives SSO/MFA for free. Deviation would have to be
argued — none proposed.

**(b) Renderer reuse = the built SPA bundle, pointed at a CONFIGURED server, authed by BEARER.** The
existing `apps/web` build (locked: "same bundle reused by the Electron renderer") is loaded in the
renderer via a custom `app://` protocol (not `file://` — file URLs break same-origin/fetch
assumptions and are a security footgun). What DIFFERS from the browser SPA: (i) no nginx same-origin —
the API base URL is configured (a server field, persisted), so `createTunnexClient("/")` becomes
`createTunnexClient(serverURL)`; (ii) auth is the bearer credential injected from main via the preload
bridge (the SPA's client attaches `Authorization: Bearer`), NOT the cookie session. The SPA's
existing client-layer header hook (from S4.8) is the natural seam. Confirm in review: whether the SPA
needs a small "transport mode" switch (cookie for web, bearer for desktop) or the client factory just
takes an optional token.

**(c) IPC security posture = locked-down by default; the preload bridge is the ONLY privileged
surface.** `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`; the renderer gets
node/OS access through NOTHING except a minimal `contextBridge.exposeInMainWorld` allowlist (get the
configured server, get/refresh the bearer, trigger login/logout — and later the S6.3 tunnel
up/down/status calls). No remote module. This allowlist IS the S6.3 tunnel-control precursor: privileged
WireGuard actions will be added as explicit IPC channels, never direct renderer access.

**(d) Auto-update = electron-updater SCAFFOLDED but INERT until S6.5b — inertness now INDEFINITE by
design.** Wire `electron-updater` (config + a placeholder feed URL) so the plumbing exists, but do
NOT call `checkForUpdates` / enable it: macOS auto-update (Squirrel.Mac) requires a signed + notarized
app and simply cannot function unsigned, and shipping an unsigned auto-updater is a security
anti-pattern. Scaffold-don't-enable. Because signing moved to the DEFERRED S6.5b (trigger = public
beta / first outside distribution), the updater — and macOS auto-update specifically — stays inert
INDEFINITELY until that trigger fires; S6.5a ships unsigned with NO auto-update. This is deliberate,
not an oversight.

**(e) Credential storage = OS keychain via Electron `safeStorage`, NOT the CLI's 0600 file.** The
desktop client stores the `tnx_` credential encrypted through `safeStorage.encryptString`
(Keychain / DPAPI / libsecret), never a plaintext-ish file — a desktop is a shared, GUI environment
where a `0600` file is weaker than the OS keychain. Argue in review: the CLI's
`~/.config/tunnex/credential.json` convention stays correct for headless/CLI; the desktop client and
CLI hold SEPARATE credentials (both independently revocable) — no shared store, no interop
requirement. Caveat to handle: `safeStorage` on Linux can fall back to plaintext when no keyring is
present — detect and warn/refuse rather than silently downgrade.

**RESOLVED (review, approved) — the four sub-questions + two additions:**
- **`app://` protocol:** standard + secure registration (`registerSchemesAsPrivileged`
  `{standard:true, secure:true}`) serving the in-bundle SPA; STRICT in-bundle path resolution — any
  path escaping the bundle dir is rejected (escape-rejection is a tested unit, not a comment).
- **SPA auth:** a token-taking client factory extending the S4.8 middleware seam, but the **raw token
  NEVER crosses into the renderer** — an attach-on-request bridge (main injects `Authorization: Bearer`
  on requests to the configured API origin), NOT a `getToken`. The token lives only in main + the
  keychain.
- **Server URL:** persisted in a **MAIN-PROCESS config file** (electron-store or equiv.), never
  renderer storage — it's where the auth flow + updater point, so it's main's concern; the renderer
  consumes it via `config.getServerUrl` over the bridge. First run shows a server-URL prompt screen;
  the URL is validated by hitting **`/healthz` before it is accepted**. **Changing the server URL when
  a credential exists FORCES re-login** (revoke local + clear the keychain entry) — a stored
  credential must never be sent to a server it was not minted against (the desktop cousin of the
  loopback exact-binding discipline).
- **Preload API = verb-specific, promise-based, minimal allowlist** — `auth.{login, logout, status}`,
  `config.{getServerUrl, setServerUrl}`, and a **reserved-but-empty `tunnel.*`** namespace for S6.3.
  **NO generic `invoke(channel, args)`** (that makes the allowlist decorative). Main **validates every
  method's inputs** (never trust the renderer, same posture as never trusting the browser). This list
  IS the (c) allowlist and doubles as the audit surface.
- **Linux `safeStorage` no-keyring fallback = REFUSE by default**, with an explicit
  `--allow-insecure-credential-storage` opt-out (a flag + a VISIBLE UI state, never a config default —
  "warn" gets clicked through, and a plaintext `tnx_` on disk without even the CLI's 0600 discipline is
  strictly worse). Acceptable alternative offered: refuse keychain-less persistence but allow
  **device-code login per session** (credential in memory only) — slower but honest.

- **S6.2 Client auth / renderer transport switch** — make the desktop app FUNCTIONAL against a
  tenant: the SPA (still "control plane unreachable" after S6.1 because it targets same-origin
  `app://`) must call the CONFIGURED server with the bearer, and the desktop must expose login/logout
  in the UI (no more devtools-console-only). **DECIDE-BEFORE-CODE (commit one, for review):**
  - **(1) How the SPA learns it is in desktop mode + its server base URL.** The web SPA uses
    `createTunnexClient("/")` (same-origin cookie). In Electron there is no same-origin server. Options
    to decide: (a) the preload exposes `config.getServerUrl()` (already built) and a tiny bootstrap in
    the SPA switches the client's base URL to it when `window.tunnex` exists; (b) main rewrites a
    build-time base-URL constant. Lean (a) — runtime, no bundle fork, reuses the existing bridge; argue
    if (b).
  - **(2) Transport = bearer, not cookie — where the switch lives.** The S4.8 client-header seam +
    the main-process `attachBearer` injector (S6.1) already add `Authorization: Bearer` on requests to
    the server origin. So the SPA in desktop mode must (i) point its base URL at the server origin and
    (ii) NOT rely on cookies. Decide: does the SPA client factory take an explicit "desktop transport"
    (base URL + no credentials:'include'), or does main's injector + a base-URL swap suffice with the
    SPA unchanged? The token must STILL never enter the renderer (S6.1 invariant) — the injector stays
    the only thing that sees it.
  - **(3) Login/logout UI + auth state in the renderer.** The SPA needs a desktop-aware entry: when
    `window.tunnex` exists, the Sign-in screen offers "Sign in with your browser" (calls
    `auth.login()`), and the app reflects `auth.status()` (logged-in/expired/secureStorage). Decide the
    minimal SPA change vs a desktop-only shell around it, and how an expired credential (local, no
    server oracle) surfaces (a re-login prompt).
  - **(4) SSO parity.** S6.2's title includes SSO. Confirm SSO needs NOTHING desktop-specific — the
    `/cli-auth` browser leg already completes any local-or-SSO login in the system browser before the
    loopback code is minted (the S5.1/Part-B proof), so desktop SSO is free. State it, or surface the
    gap.
  - Guards: any new endpoint auto-armed by the 401-walk + RBAC; the token-never-in-renderer invariant
    gets an explicit assertion.

**COMMIT ONE — decisions confirmed (pre-positions folded, no deviation; build proceeds directly):**
- **(1) Desktop detection + one-bundle runtime branching.** `window.tunnex` presence IS the desktop
  signal — one SPA bundle, runtime branch (no build fork). A bootstrap in `main.tsx` awaits
  `config.getServerUrl()` and calls `setApiOrigin(origin)` in `@tunnex/shared` BEFORE React renders,
  so every request (incl. the first `/auth/me`) targets the configured server. Web path unchanged
  (origin unset → same-origin `/`).
- **(2) Main-process exact-origin bearer injection; residual acknowledged.** The S6.1 `attachBearer`
  (bearer only when request-origin === configured-origin === `cred.server`, unexpired) stays the ONLY
  thing that sees the token. The client middleware only rewrites the ORIGIN of the request URL; it
  never touches auth. RESIDUAL (acknowledged): the renderer still *initiates* authenticated calls and
  *reads* their response bodies — unavoidable (it is the UI) and not a token exposure; the invariant
  is "token never enters renderer JS", which holds.
- **(3) No web login FORM in desktop; bridge-driven auth state; unverified consent messaging.** In
  desktop mode the SPA's Sign-in screen replaces email/password with "Sign in with your browser"
  (`auth.login()`); on success main reloads → `/auth/me` (bearer) → authed. Logout in desktop routes
  through `auth.logout()` (revoke + clear keychain + reload). The `/cli-auth` consent page (runs in
  the system browser, cookie session) messages an UNVERIFIED user clearly on `email_not_verified`
  instead of a generic error.
- **(4) SSO parity = verify-only, zero build.** The `/cli-auth` browser leg already completes any
  local-or-SSO login in the system browser before the loopback code is minted (S5.1 + Part-B proof),
  so desktop SSO needs no desktop-specific code. Confirmed, no build.
- **S6.3 Tunnel control** — start/stop WireGuard, embed `wireguard-go`/wintun (mac/win), privilege helper.
- **S6.4 Connection UX** — status, server picker, split-tunnel toggle, tray icon, notifications.
  **SCOPE NOTE (captures the revocation-POC findings so nothing is re-discovered):**
  - **Base:** connection status, server picker, split-tunnel toggle, tray icon, notifications.
  - **(item 4) change-server / sign-out UI** — the client silently reused a stale `localhost` server +
    credential; the only recovery was deleting userData files by hand (never a customer action). The
    origin-keyed config already anticipates it; S6.4 adds the UI (surface the current server + a
    switch/sign-out affordance; `config.setServerUrl` already forces re-login).
  - **Revocation-aware teardown (NEW, from the revocation POC — the real S6.4 work):** when an admin
    revokes the device, the gateway drops the peer (traffic stops, ↓0 B), but the client keeps its
    interface up and retries handshakes forever. The client must DETECT peer-gone — persistent
    handshake failure past a threshold, and/or polling its own device status — then **auto-disconnect +
    clear the now-dead config** (the config is client-owned per D2, so nothing else tells it). Has its
    own tests (revoke → client tears down within N s; config cleared; no stale "Connected").
  - **ALREADY DOWN-PAID on `story/S6.3` (commit 7e99631) — do NOT rebuild:** (a) connection status is
    derived from HANDSHAKE liveness (no green "Connected" when the last handshake is stale >180s — kills
    the "Connected — handshaking…" contradiction); (b) the assigned tunnel IP is shown ("Your IP:
    10.99.0.x") — main caches the config address and attaches it to forwarded status. These two shipped
    early because a green-but-dead status is misleading even in a demo; S6.4 builds the rest on top.
- **S6.5a Packaging (unsigned)** — `electron-builder` `.dmg` + `.exe`, `SHA256SUMS`, an install
  script, and DOCUMENTED Gatekeeper (macOS) / SmartScreen (Windows) workarounds for unsigned
  artifacts. Ships in EPIC 6. Auto-update stays OFF (see S6.5b). This is the "friends & self can
  install it" milestone.
- **S6.5b Code-signing + notarization + auto-update — DEFERRED.** Apple notarization + Windows
  Authenticode, then flip `electron-updater` ON (the scaffold is inert until here — see S6.1 (d)).
  **Trigger = public beta OR first outside-the-inner-circle distribution** (not a calendar clock).
  **Windows EV blocker:** an EV cert requires a LEGAL ENTITY that does not yet exist — entity
  formation is additive lead time on top of the 1–3 wk EV validation, so start it when the trigger
  approaches. **Interim recorded:** an INDIVIDUAL Apple Developer ID (no entity needed) can sign +
  notarize macOS early if only macOS distribution is wanted first; Windows waits on the entity.
- **S6.6 Zero-build deploy (EPIC-6 epic-end) — from the POC's #1 friction.** PRINCIPLE: a customer
  must NEVER clone the repo, build from source, edit files, or run diagnostics to get a working
  tunnel. The POC required building on BOTH server and VM. Minimum-customer-effort =
  **published prebuilt images (ghcr.io)** + a **hosted compose file** + an **`install.sh` that asks
  for exactly two things** (public address; SMTP-or-skip) and writes a clean `.env`. This pulls most
  of **SB.1/SB.2 forward into reality** — those stories **shrink accordingly** (SB.1 Helm / SB.2
  hardening keep only what S6.6 doesn't cover). Depends on the CI publishing images (extend S6.0b) +
  S6.5a for the client side. **Pipe-safe from day one** (marketing's landing-page hero is
  `curl -fsSL https://get.tunnex.io | sh` serving THIS script): `install.sh` is safe to pipe blind
  into a root shell — idempotent (re-run reuses the DB password), write-then-move `.env` (never
  half-written), non-TTY env-var overrides (`TUNNEX_PUBLIC_ADDR` / `TUNNEX_SMTP`), loopback refused at
  the source, and a **SHA256 shipped alongside the release assets** so the docs offer a
  download-verify-inspect-run path (the security-conscious default) beside the one-liner.
  **OWNERSHIP (must not drift): there is exactly ONE `install.sh` — it lives in THIS repo (produced by
  S6.6). The marketing site only SERVES it (release asset / static file); it must NEVER fork or
  hand-maintain its own copy.** `get.tunnex.io` waits on the pending domain purchase — S6.6 does not
  block on it; the script just gets a URL later.
- **S6.7 Windows kill-switch persistence (from S6.5a's live-found gap)** — the Windows WFP full-tunnel
  kill-switch is NOT fail-closed on process death: wireguard-windows opens its WFP engine with
  `FWPM_SESSION_FLAG_DYNAMIC`, so filters auto-delete when the process exits → a hard-killed helper
  releases the block → traffic leaks (pcap-confirmed on the box 2026-07-10). macOS pf is persistent
  (proven); Windows is not. **Fix:** a NON-DYNAMIC WFP session (persistent filters) + a FIXED provider
  GUID + an explicit enumerate-and-delete `DisableFirewall` (the dynamic session did all cleanup for
  free — remove it and nothing does), reusing wireguard's proven filter set. **Recovery safety net**
  (bounds the blind-implementation risk): startup `CleanStale` removes any stuck block before re-arming,
  the dead-man still bounds it, service auto-start makes reboot a recovery, + a documented `netsh wfp`
  manual escape. **Decision-first, box-proven (pcap), reviewed** — a root kill-switch primitive, treated
  like S6.3. **Trigger: before Windows full-tunnel is offered to real users** (pairs with S3.7, since
  full-tunnel usability needs BOTH gateway egress + a real kill-switch). Until then the client gates/
  caveats Windows full-tunnel.
- **S-POC-fixes (hotfix story — STARTED NEXT, before resuming S6.3 remaining).** POC friction items
  2 + 3: **(2) ceremony one-time-secret COPY BUTTON didn't work** (manual copy needed) — a real UX
  failure; **(3) verify-email link emitted `localhost` on a REMOTE deploy** (`APP_BASE_URL` left at
  its default) — bootstrap must FAIL LOUD or warn when `APP_BASE_URL` is the localhost default while
  the process is clearly non-local. Both are immediate customer-facing bugs.

## EPIC 7 — Zero Trust Access *(enterprise)*

- **S7.1 Policy model** — resources, groups, access rules (who → what), default-deny.
- **S7.2 Policy enforcement** — evaluate on connection + per-peer route filtering (via agent).
- **S7.3 Device posture (basic)** — require known device, block untrusted.
- **S7.4 Policy UI** — rule builder in dashboard. **LEDGERED (from S7.2): the DIFFERENTIATED policy-health
  surface.** S7.2 collapsed the gateway health signal to ONE conservative `policy_degraded` boolean
  (the 3-signal→2-field mapping produced gap states across 3 review passes; see docs/S7.2-decisions.md).
  The rich agent signals (`policy_error`, `policy_failing_since`, `policy_hash`) still land in the node
  capabilities JSONB. S7.4 designs the differentiated read (which-KIND-of-degraded: apply-failing vs
  stuck-enforcing vs silent-desync) + the debounced badge UX, reading that JSONB — and MAY re-introduce a
  windowed silent-desync signal (needs a server-side desync-onset, deferred out of S7.2 as new state).

## EPIC 8 — Site-to-Site Networking

**LEDGERED at S7.2 (decide-before-code for S8.1/S8.2): Zero Trust policy MUST govern site-to-site
traffic.** Sites/subnets become a policy DESTINATION KIND — extending the S7.1 model through the
VERSIONED `policyspec.Compiled` artifact (bump the version; agents gate on it), not a side channel.
**S8.2's propagated routes are compiled-policy OUTPUT, never a parallel enforcement path:** under
`enforcing`, a site subnet is reachable ONLY via an explicit grant; under `off`, the legacy mesh —
the same mode-as-compiler-input principle S7.1 locked (one code path, one artifact, mode selects
what's compiled). **Deliberate-red at S8.2:** enforcing + zero grants → a propagated site subnet is
ROUTED but DROPPED at the gateway forward chain (routing ≠ permission). Note **S10.3 already
presumes this seam** ("expose in-cluster services via Zero Trust policies") — it is load-bearing for
EPIC 10 too; do not build S8 routing without it.

**LEDGERED at S7.2 (more S8.1/S8.2 decide-before-code + a promoted story):**
- **Site-link TRANSPORT is a modeled enum field from day one** (S8.1 schema), with **wireguard the
  only implemented value**. This RESERVES the parked IPsec-interop seam (for agent-impossible
  endpoints: managed cloud VPN gateways — AWS TGW / Azure VPN GW — hardware appliances, partner
  networks) without a later migration. **IPsec itself stays PARKED** — trigger = a real
  customer/prospect with an agent-impossible endpoint AND **after EPIC 9 ships** (no third protocol
  before the second is proven). If ever built: **strongSwan managed by the node agent per the S9.1
  pattern, site-to-site ONLY** (no IPsec end-user clients), and the **tested-interop matrix is bounded
  at story-open** (strongSwan↔strongSwan + AWS/Azure managed endpoints; arbitrary-appliance interop
  explicitly best-effort — an unbounded vendor matrix is REJECTED in advance). **Routing + Zero Trust
  enforcement are transport-agnostic by design — state this in S8.1.**
- **Subnet-advertisement decisions (S8.1/S8.2):** (a) **overlapping advertisements across sites →
  REFUSE the second** (typed clean error, `gateway_no_egress`-style) in v1; precedence/longest-prefix
  semantics DEFERRED (trigger = first real customer need). **Silent ambiguity is the one forbidden
  outcome.** (b) **Advertisements require control-plane/admin APPROVAL before propagation** — a
  compromised site gateway must not hijack routes by advertising subnets it doesn't own; **approved ≠
  reachable** (Zero Trust grants still gate reachability, per the ledger above / d21cf19). (c) Manual
  route pinning DEFERRED alongside (a).
- **S8.4 Cross-site DNS — PROMOTED to an in-scope story** (below). Rationale: subnet reachability
  without name resolution is half a feature for real users, and it is the #1 competitor-comparison
  line. The device config's DNS field (S3.4) is the client-side seam; **S8.1's schema RESERVES the
  site-carries-DNS-forwarding-entries seam** as S8.4's foundation.

- **S8.1 Gateway/site model** — register site gateways (each a `tunnex-node` agent), subnet routing.
  **Reserves: link transport enum (wireguard only), site DNS-forwarding entries (S8.4). Routing + ZT
  enforcement transport-agnostic.**
- **S8.2 Route propagation** — advertise/accept routes between sites via WireGuard, reconciled by agents.
  **Advertisements need admin approval; overlaps refused (typed); approved ≠ reachable (ZT gates).**
- **S8.3 Site management UI** — add site, topology view, health.
- **S8.4 Cross-site DNS** *(promoted from candidate, S7.2 review)* — mesh name resolution (devices +
  site hosts resolvable by name across the mesh) + **split-horizon per-site forwarding** (a domain →
  that site's existing internal resolver, queries routed over that site's tunnel). Decision-first,
  **sequenced after S8.2**. Reference design: MagicDNS + split-DNS.

## EPIC 9 — OpenVPN Support (port from existing Bolster stack, not greenfield)

- **S9.1 OpenVPN server mgmt in node agent** — port `openvpn-auth-oauth2` patterns + `genclient`-style PKI into the agent; managed process, cert/PKI, config gen. Reference the Bolster handover doc as the spec.
- **S9.2 OpenVPN profiles** — `.ovpn` export, per-user certs, revocation (CRL) — same identity-binding rule as S3.3. **The `.ovpn` export is the OpenVPN client story (made first-class here + S9.3): per-OS import instructions + a QR on the download page, consumed by the OFFICIAL OpenVPN clients (OpenVPN Connect / Tunnelblick / mobile). Revocation guarantees hold FULLY (CRL + the full-sweep of §Cross-Cutting: cert/CRL revoke + address release + status clear).**
- **S9.3 Protocol selection** — org/server chooses WireGuard or OpenVPN. **"Clients support both" — DECIDED (not open), so it isn't re-litigated:**
  - **Path (a), delivered:** OpenVPN is consumed via the `.ovpn` export (S9.2) by the standard OpenVPN clients. The **Tunnex desktop client stays WireGuard-ONLY** — it is WireGuard-only BY CONSTRUCTION (embedded wireguard-go/nt, WG-typed helper verbs, pf/WFP kill-switches, WG ConfigProvider, handshake-based revocation detection); nothing in EPIC 6 or 9 builds an OpenVPN engine into it.
  - **Optional S9.x (decide-at-open):** the `tunnex` CLI wraps `openvpn` as it already wraps `wg-quick`. Small, sandboxed, no privilege-helper blast radius.
  - **Positioning line (pinned):** "both protocols" = both **server-managed with full revocation**; **WireGuard gets the native Tunnex client, OpenVPN uses standard OpenVPN clients.** NEVER "our desktop app runs OpenVPN."
  - **REJECTED (strongest deferral tier — the rejected-call-home-licensing class, NOT parked-with-trigger): native OpenVPN INSIDE the Tunnex desktop client,** unless a paying customer makes it a hard deal condition. Rationale (recorded so it isn't re-argued): a second data-plane engine inside the privilege helper — the most security-critical, most expensively-verified component (S6.3 decide-before-code + live-pcap kill-switch proofs ×2 platforms + S6.7) — would need a managed-process `TunnelUp` path (exactly the injectable-surface class S6.3 rejected), OVPN-specific kill-switch semantics, cert-based config storage, CRL revocation detection, and a permanent **2× proof burden on every future helper change** — all for a population whose migration endgame is the WireGuard client we already ship. Reference competitor (Tailscale) ships ZERO OpenVPN and wins those migrations anyway.

## EPIC 10 — Kubernetes Integration

- **S10.1 Helm chart** — deploy full tunnex stack to a cluster; values for secrets, ingress, storage.
  **Shared seam obligation:** the external DB/Redis + master-key env contract
  (`TUNNEX_DATABASE_URL`/`TUNNEX_REDIS_URL`/master-key source) is the SAME one install.sh uses — do not
  diverge from the S6.6 ledger (see docs/S6.6-decisions.md "external DB/Redis"); the master-key
  externalization decide-item is load-bearing here (external DB customers).
- **S10.2 Operator + CRDs** *(enterprise)* — `TunnexPeer`, `TunnexRoute`; reconcile WG peers/routes as k8s resources — **reuses the S3.1 reconcile loop design**.
- **S10.3 Cluster gateway** — expose in-cluster services to tunnex clients via Zero Trust policies (agent as in-cluster gateway). **Depends on the EPIC 8 ledger seam** (sites/subnets as a policy destination kind in the versioned `Compiled` artifact) — in-cluster service exposure is the same "subnet reachable only via grant" mechanism.

## EPIC 11 — Production Hardening

- **S11.1 Metrics** — Prometheus metrics, health/readiness (logging already in EPIC 0).
- **S11.2 Backup/restore** — DB + master key **+ node-agent state (WG private keys on each gateway)**; documented restore.
- **S11.3 Rate limiting & security headers** — API abuse protection, TLS via nginx, secrets hygiene.
- **S11.4 Docs & install guide** — self-host quickstart, upgrade path.

**LEDGERED (S7.2 box-proof finding #2, DEFERRED): targeted conntrack-kill on Zero Trust grant change.**
Today `ct established,related accept` (the return-path guard) lets flows ESTABLISHED under a prior
policy DRAIN until idle when a restriction takes effect — covers BOTH enabling `enforcing` AND deleting
an existing grant; NEW flows are blocked immediately, and revocation/offboarding is unaffected (wg peer
removal kills the tunnel). To terminate in-flight flows the instant a grant is removed, the agent would
delete the conntrack entries matching the removed allow. **Trigger = first customer/compliance need for
immediate flow termination on grant change.** Pairs naturally with the flow-logs candidate (S7.2 already
emits per-rule `counter`s) — the same per-rule identity drives both. Documented in docs/S7.2-decisions.md.

## ZTNA COVERAGE + GAP LEDGER (batch recorded during S7.4b; DISPOSITION AT EPIC-7-CLOSE PLANNING — no code, no story #s except as noted)
1. **Flow / access logs — PROMOTION CANDIDATE.** Argue at EPIC-7 close for EPIC-7-ADJACENT, *ahead of
   site-to-site* if needed. Seam exists: the S7.2 per-rule `counter`s. Buyer-facing property = "who accessed
   WHAT, WHEN" — compliance/sovereignty buyers treat this as the *reason to buy ZTNA*; "ZTNA without access
   logs" is the competitor's line against us. (Pairs with the conntrack-kill item above — same per-rule identity.)
2. **IdP-group sync (Entra/Google groups → policy subjects) — enterprise-gated.** Without it, policy groups
   must manually mirror the directory and decay immediately. Candidate: EPIC 7.x / EPIC 8 era.
3. **Posture DEPTH (OS version · disk-encryption · EDR-present).** S7.3 is KNOWN-device, not HEALTHY-device.
   Needs a story number + named trigger = **first compliance-driven prospect**.
4. **Per-USER grants.** Rules are group→resource only; "give Alice temporary access" has NO path. DECIDE
   (user-as-a-subject-kind vs a blessed one-user-group UX) **before the policy UI hardens the habit** (S7.4a
   shipped group-only; revisit before it ossifies).
5. **S7.4b scope note.** The differentiated-health BADGE is *enforcer-health*, NOT *access-visibility* — the
   larger visibility half is item 1 (flow/access logs). Don't let the badge read as "we have visibility."
6. **ZT-coverage: OpenVPN (S9.1 DECIDE-BEFORE-CODE — REQUIRED).** OVPN devices MUST be policy subjects in the
   SAME `policyspec.Compiled` artifact (grants are transport-agnostic); **cert-auth alone is NOT enforcement**.
   Deliberate-red at S9.1: `enforcing` + zero grants → OVPN client traffic DROPPED at the forward chain, same
   as WG. A parallel non-compiled OVPN path = a two-door breach — **rejected in advance**.
7. **ZT-coverage: DNS under enforcing (S8.4 PAPER ITEM).** Split-horizon DNS needs port-53-to-site-resolver
   reachability MODELED (a grant, or an explicit modeled exception) — else name resolution breaks silently
   under `enforcing`.
8. **ZT-coverage: full-tunnel egress under enforcing (S3.7 DECISION-REVIEW ITEM).** Decide whether internet
   egress is a policy DESTINATION KIND (an "internet" resource) or explicitly OUT-OF-ZT-SCOPE; currently
   UNDEFINED under `enforcing` (a full-tunnel device under enforcing with no egress grant = undefined behavior).

## ZTNA COMPETITIVE SCOPE — LEDGER BATCH 2 (user-directed strategic intent, 2026-07-14; PAPER only, no epic reorder executed — DISPOSITION AT EPIC-7-CLOSE PLANNING). Extends batch-1 items 1–4 from "gaps" → COMMITTED competitive scope.
**STRATEGIC FRAME (pinned):** competitive target = the self-hosted / WireGuard ZTNA segment — **Tailscale · Twingate · NetBird · Headscale** — NOT the Zscaler tier. **Win condition:** match-or-beat the segment leaders on ZTNA DEPTH while holding the unique differentiator — **fully self-hosted, zero SaaS in the trust path, air-gappable**. L7/app-aware proxying, risk scoring, continuous re-auth = Tier-3 roadmap NAMES, explicitly NOT built.

**PROPOSED EPIC 7.5 — "ZTNA Competitiveness" (insert BEFORE EPIC 8; confirm at planning):**
- **S7.5.1 Flow / access logs** — per-connection / per-grant access events, org-scoped, queryable + exportable;
  builds on the S7.2 per-rule `counter`s seam. **Starts FIRST under any beta outcome.** Decide-before-code:
  event granularity, retention/rotation (customer's disk), append-only / audit-class storage posture.
- **S7.5.2 IdP-group sync + SCIM** — Entra/Google groups as policy SUBJECTS (sync, not mirror); SCIM rides or
  splits at paper. Enterprise-gated. Decide-before-code: IdP-authoritative vs merge-conflict rules; a
  deprovisioned user gets the full S2.6/S7.2 sweep.
- **S7.5.3 Posture checks v1** — extends S7.3's gate: OS version · disk-encryption · EDR-present; block-or-warn
  per org. Decide-before-code: client-reported attestation limits named HONESTLY (spoofable by a compromised
  device — threat model stated, not oversold).
- **S7.5.4 Per-user + temporary grants** — USER as a subject kind in `policyspec.Compiled` (versioned-artifact
  bump per the S8 seam discipline) + grant EXPIRY (`expires_at` → recompile+push on lapse, org-wide push law).
  **Decide before the S7.4a UI hardens the group-only habit.**

**Tier 2 (carriers exist — confirm at session):** S8.4 internal DNS (stands) · EPIC 8 site-to-site under policy
(pre-wired, stands) · connection-events / session-lite (extension of S7.5.1) · SCIM (in S7.5.2).
**COLLISION flagged for planning (user decision, NOT pre-decided):** EPIC 7.5 vs the beta re-decide — beta at
EPIC-7-done while building 7.5 during beta, OR beta after Tier 1. Flow-logs-first is common to both paths.
**Consequences acknowledged:** EPIC 8/9/10 slide right ~one epic; the EPIC 9/10 ZT-coverage guarantees
(batch-1 items 6–8) UNCHANGED. Batch-1 items 1–4 are SUPERSEDED-BY-INCLUSION into S7.5.1–S7.5.4.

**LEDGERED (S7.2 story-end review #8/#9/#10, DEFERRED — CORRECTNESS-NEUTRAL perf pass): policy-fetch
throughput.** (#8) `CompiledForNode` recompiles the artifact on EVERY `DesiredState` fetch — cache by
policy version instead. (#9) no off-mode fast-path — off-mode orgs still walk the compile path to
produce a mesh artifact; short-circuit to the blanket-mesh artifact. (#10) redundant re-apply per
fetch — an identical `Compiled` re-renders + re-applies each cycle (the idempotence guard makes it a
kernel no-op, but it still burns an `nft` transaction); skip apply when the applied hash already
matches. None change behavior; all are throughput optimizations. **Trigger = policy-fetch load becomes
measurable.** Documented in docs/S7.2-decisions.md.

## EPIC 12 — Commercial / Licensing Infrastructure *(PARKED — trigger: post-public-beta; needs a sellable product + users first)*

**Positioning guard:** licensing MUST NOT break the "self-hosted, no SaaS in the trust path" differentiator. License verification is **OFFLINE** — the customer's deployment verifies a signed key locally against a baked-in public key; it works air-gapped and **NEVER calls Tunnex infra to function.** Any phone-home (renewal reminders, telemetry) is optional, async, and degrades gracefully — a lapsed connection to Tunnex infra **NEVER hard-fails a running VPN.** This is the sovereignty/Tailscale-differentiator constraint; a call-home validation model is explicitly **REJECTED**.

- **S12.1 Edition Model refactor (build-tag → runtime license-gate)** — decide-before-code, **supersedes the S1.1 model**. Single binary; enterprise code compiled in; a `LicenseManager` gates enterprise features at runtime on a verified key. Replace the test-editions build-tag guard with a **runtime-gating guard** (open-by-default; features light up only with a valid enterprise key). **The load-bearing story — everything else depends on it.**
- **S12.2 License key format + offline verification** — **Ed25519-signed** key (private key in the issuance service; public key baked into the binary). Key encodes `{company_domain, tier, seats, issued_at, expires_at, license_id}`. Binary verifies signature + expiry **offline**. Expiry → grace period + UI warning → revert to open features; **never a hard VPN cutoff.** In-app "paste your license key" UI + a `POST /admin/license` endpoint (owner/admin-gated, audited).
- **S12.3 In-app upgrade + trial-request affordance** — "Upgrade to Enterprise" in the open build; "Start 30-day trial" flow that requests a key from the issuance service.
- **S12.4 License issuance service** *(Tunnex-hosted infra — the ONLY hosted piece; holds billing + entitlement data ONLY, never VPN traffic/configs/user data)* — signing service (guards the private key), issues keys on paid purchase or validated trial, emails the key (support-flow delivery). Trial-per-company-domain anti-abuse: a `domain → trial_issued_at` table refuses a second trial for the same domain. **DECIDE-BEFORE-CODE:** trial gating = **DNS-TXT domain-ownership proof** (STRONG — reuses the S2.5 domain-capture verifier) vs email-domain best-effort (weak, gameable). *[Leaning DNS-TXT — S2.5 already built it; confirm at story open.]*
- **S12.5 Landing + payment** — pricing/landing page; **Stripe (US) + Razorpay (India)** — both markets from launch; purchase → issuance.
- **S12.6 Compliance pass** *(needs a real lawyer per market — NOT hand-waved)* — India **DPDP Act 2023** + US state privacy; data-residency review. **Architectural compliance win to preserve:** the hosted infra holds only billing + license data; all VPN traffic, configs, and user data stay entirely on the customer's self-hosted deployment — minimizing hosted-infra data footprint is the single biggest compliance lever. ToS/privacy policy; export-control check on crypto distribution (US EAR) for the US+India launch.

**Build-order note:** EPIC 12 slots **AFTER the public beta** (EPIC 6 → beta → 7/8/9… with 12 inserted when monetization is wanted). It is **NOT** in the near-term path. Recorded now so the S12.1 Edition-Model consequence is known before S6.6/beta build in a way that assumes build-tag permanence.

---

## Recommended Build Order
EPIC 0 → 1 → 2 → 3 (WG core loop) → 4 (dashboard) → 5 (CLI) → 6 (Electron) → 7 → 8 → 9 → 10 → 11. **(EPIC 12 = commercial/licensing, PARKED — inserted post-public-beta when monetization is wanted, NOT in the near-term path.)**

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
