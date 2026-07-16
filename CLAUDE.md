# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Re-entry rule (fresh session)

Re-enter from **PLAN.md's "Story status (re-entry checkpoint)"** pointer plus `git log` —
**trust git over any memory or handoff summary.** A stale pointer re-enters the wrong epic;
if PLAN.md and git disagree, git wins and the pointer gets fixed. Update the pointer on
every merge (one line).

## Story protocol

Stories build one at a time, decision-first:

1. **Commit-one is paper** — `docs/S<story>-decisions.md` records the decisions (decide-items
   dispositioned explicitly) *before* product code. Dispositions are folded back into the paper;
   the paper is the record.
2. **Slices** — build in reviewable slices on the story branch (`story/S<n>-<slug>`).
3. **Review** — self-review + `/code-review`; dispositions come BEFORE folding findings.
   A mid-fold redesign (e.g. of a security test) is a decide-item, not a fold.
   Story-end = a multi-finder review. Findings are presented RANKED and HELD for
   disposition (the user brings dispositions back); fold only what's dispositioned.
   A feature-sized fold RE-EARNS a review of the folded code. Budget rule: repeated
   fold-induced defects in the same component = HALT, paper its state model, reduce
   not patch (the S7.5.1 JSONL arc — six rounds → deferred to S7.5.1b rather than
   shipped). A session-limited/incomplete review is INCONCLUSIVE, never clean —
   re-run it.
4. **Box-walk** — prove the story on a live wire (docs/S*-boxwalk.md / -box-walk.md);
   unit tests SUBSTITUTE for a wire proof but never SATISFY it (see ledger conventions).
   Walk evidence is COMMITTED during the walk session (walk-artifacts/), not after.
   Walk-time scratch credentials (WG configs etc.) contain private keys — gitignore
   them at creation, never commit.
5. **Both-green** — CI required checks (`gates` + `client (macos-latest)` + `client (windows-latest)`)
   must pass; run the gate targets locally first.
6. **Merge only on explicit in-session sign-off.** A merge instruction executes in the session
   that receives it, or is RE-CONFIRMED at re-entry — a sign-off read out of a summary/handoff
   is NOT authorization to merge. Never merge without the user's word. Merges to `main` are
   PR → ff-only, linear history. `git push --force-with-lease` is pre-authorized for `story/*`
   branches only; `main` is never force-pushed.

Where a commit lives: product code ALWAYS on the story branch. A process/docs correction whose
value is immediate (e.g. fixing the re-entry checkpoint) lands on `main` directly; then rebase
the active story branch onto main.

## Gates (run before declaring a slice/story done)

The CI `gates` job is the composite; locally that means:

```bash
make generate-check    # codegen drift guard (OpenAPI → Go/TS/RBAC/sqlc)
make migrate           # apply migrations (stack's postgres must be up)
make test-editions     # Go API tests in BOTH editions (open + enterprise build tags)
make build-editions    # both editions compile (catches edition rot)
make test-node         # node-agent data-plane tests
make test-helper       # privilege-helper vet + test
make helper-crosscompile
pnpm --filter @tunnex/web typecheck && pnpm --filter @tunnex/web test && pnpm --filter @tunnex/web build
```

**Both editions, always** — every API change must build and test with and without
`-tags enterprise`. Go builds/tests use `GOFLAGS=-mod=readonly` deliberately (module path
`github.com/tunnexio/tunnex/*` ≠ repo `iotunnex/tunnex`; `-mod=mod` remote-resolves and breaks
fresh clones — see the GUARD notes in Makefile and each go.mod).

Other commands: `make up` / `make up-enterprise` / `make down` (compose stack),
`make e2e` (Playwright + API integration), `make seed` / `make seed-enterprise`,
`make migrate-create name=<snake_case>`, `make sqlc`, `make generate`.

## Decisions & ledger conventions

- **`docs/S*-decisions.md`** is the decision record per story: decide-items listed, each
  dispositioned (locked / rejected-with-rationale / deferred-to-named-story). Rejected
  alternatives stay in the paper so they're findable later.
- **SUBSTITUTES ≠ SATISFIES:** when a proof can't run (no hardware, no desktop, no cert),
  the substitute (unit tests, paper sign-off) is recorded as a SUBSTITUTE with a NAMED
  trigger for the real proof — deferred, never dropped. Triggers are named events
  (e.g. "public-beta readiness"), never calendar clocks.
- **Mid-build forks halt-and-surface:** discovering a fork in the road mid-build (a new
  decide-item, a scope change, an unexpected design constraint) halts the build and surfaces
  it for disposition — do not pick a branch silently. This applies to review findings too:
  decide-items and named stop-conditions/tripwires go to the user; never resolve one
  unilaterally.
- **Enterprise features are UNLOCK-THEN-OPT-IN, never unlock-and-enforce** (founder-directed):
  org-level opt-in, default OFF. Unlocking (edition/license) makes a capability available;
  it never turns enforcement on.

## Architecture

Monorepo (pnpm workspaces + turbo for TS; independent Go modules per app):

- **`apps/api`** — Go control plane (chi, sqlc, PostgreSQL, Redis sessions). The API NEVER
  touches WireGuard directly. Open-core split: `internal/enterprise/` + anything behind the
  `enterprise` build tag is proprietary (own LICENSE); the rest is Apache-2.0. Never let the
  two bleed together — `make test-editions` is the guard. Migrations: `apps/api/db/migrations`
  (numbered pairs); typed queries via sqlc from `db/queries`.
- **`apps/node`** — data-plane agent owning WireGuard via wgctrl. Desired-state reconcile
  loop: data-plane state is continuously reconciled against control-plane desired state,
  never assumed in sync.
- **`apps/web`** — React + Vite + Tailwind SPA; same bundle reused by the Electron renderer.
  RBAC mirror is generated (`src/lib/rbac-policy.json`), never hand-edited.
- **`apps/client`** — Electron desktop app. Renderer never holds tokens (main-process
  webRequest injector); preload exposes a verb allowlist, no generic invoke. Client unit
  tests must import NO electron at runtime (CI sets ELECTRON_SKIP_BINARY_DOWNLOAD) — pure
  view-models live in electron-free modules.
- **`apps/helper`** — root privilege helper (typed protocol, canonicalized caller auth,
  version handshake) + kill-switches: macOS pf, Windows WFP. `internal/wfp/` is a PINNED,
  DIVERGED fork of wireguard/windows tunnel/firewall — on any wireguard/windows bump,
  re-diff and re-apply the deltas (see its VENDOR.md).
- **`apps/cli`** — `tunnex` CLI (Go client generated from the spec).
- **`packages/shared`** — generated TS API types + shared client transport.

**OpenAPI-first:** `openapi/openapi.yaml` is the single source of truth. Handlers, the Go CLI
client, TS types, and the RBAC mirror are all generated (`make generate`); CI fails on drift
(`make generate-check`). Never hand-sync types.

Cross-cutting invariants (established, don't regress):
- Identity ↔ credential binding: a device credential is only valid for its owning user; no
  floating credentials. Revocation is a FULL sweep (peer slot + pool address + telemetry).
- Default-deny policy model; policy compiler is pure/deterministic (`policyspec.Compiled`).
- RBAC: permissions are named per feature (never reuse an existing perm for a new capability);
  the grant table is generated and drift-guarded.
- Audit logs record system actors first-class (`actor_system`) with a cause.
- No-oracle 401s; one-time-secret hygiene; keyed proof-of-secret; keyset pagination;
  edition gating = 403 `edition_required`.
