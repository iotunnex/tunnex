# OpenSSF Scorecard — BASELINE

**Same discipline as `docs/S11-security-baseline.md`: a measured result, recorded WITH its caveats, so the
number is readable by someone who did not run it.**

| | |
|---|---|
| **AGGREGATE** | **3.7 / 10** |
| tool | Scorecard **v5.1.1-45-g40bbc9c9** (`gcr.io/openssf/scorecard:stable`) |
| target | `github.com/iotunnex/tunnex` |
| measured | **2026-08-01**, locally, `main` at **`a25713e`** |
| **PUBLISHED?** | **NO** |

## ⚠ THERE IS NO PUBLIC SCORE, AND THAT IS A DELIBERATE SETTING

`security.yml` runs the job with **`publish_results: false`**, so **`api.securityscorecards.dev` returns
nothing for this repo** — it was queried and returned empty. The job runs on every push to `main`, asserts its
SARIF is non-empty (the O-1 guard), and uploads to code scanning.

**3.7 was obtained by running the scanner locally.** It is **re-earned by any change** to the repo, its
workflows, or its dependencies — like every other measured result here, it is a **dated fact, not a property**.

---

# ⛔ READ THE TRIAGE BEFORE THE NUMBER — 3.7 IS NOT "THREE-POINT-SEVEN PROBLEMS"

**Ten checks score below 10. FOUR are artifacts of being a young solo repo, ONE is a decision already ruled,
ONE is accepted with reason, and THREE are genuinely actionable.**

## FULL MARKS — 6 checks

`CI-Tests` 10 · `Dangerous-Workflow` 10 · `License` 10 · `Packaging` 10 · `SAST` 10 · `Security-Policy` 10.
`Binary-Artifacts` 9.

## GROUP 1 — ARTIFACTS OF A YOUNG SOLO REPO (4). **Nothing to fix; no code change would move them.**

| check | score | why it is an artifact |
|---|---|---|
| `Maintained` | 0 | *"project was created in the last 90 days"* — **a clock, not a practice** |
| `Contributors` | 0 | wants contributors from **2+ organizations** |
| `Code-Review` | 0 | *"0/3 approved changesets"* — **what a solo founder's PRs look like**; there is no second person to approve |
| `CII-Best-Practices` | 0 | no OpenSSF **badge** applied for — an application, not a property of the code |

**These will move on their own, or with the founder-ledger items (design partners, entity formation), not with
engineering effort.**

## GROUP 2 — ALREADY RULED, DEFERRED ON A NAMED TRIGGER (1)

| check | score | disposition |
|---|---|---|
| `Signed-Releases` | 0 | **This is S6.5b.** Deferred on its NAMED trigger — *public beta OR first outside-circle distribution* (Windows EV additionally waits on legal-entity formation). **Scorecard is re-reporting a decision already made**, not finding something new. |

## GROUP 3 — ACCEPTED WITH REASON (1). **Not a gap. Do not re-open it.**

### `Branch-Protection` — 3/10

> *"branch protection is not maximal on development and all release branches"*

**BOTH deductions are deliberate:**

- **`enforce_admins: false` is THE ADMIN ESCAPE HATCH, and it is why a solo founder can merge at all.**
  Recorded at S6.0b when protection was configured and re-confirmed at S7.5.3. With it enabled, a
  single-maintainer repo cannot land its own reviewed-by-nobody PR — the protection would lock the only person
  who can unlock it.
- **Required reviews are STRUCTURALLY UNAVAILABLE to a solo repo** — the same root as `Code-Review` 0/10 above.

**What IS enforced, and was verified immediately before the EPIC-14 merges:** `required_linear_history: true` ·
required checks `gates` + `client (macos-latest)` + `client (windows-latest)` · `strict: true` ·
`allow_force_pushes: false`.

**RECORDED SO A FUTURE SESSION DOES NOT RE-DISCOVER IT AS A FINDING** and spend time on a decision already
made. **Revisit only when the repo has a second maintainer** — at which point `enforce_admins` becomes
affordable and both checks move together.

## GROUP 4 — GENUINELY ACTIONABLE (3). **REGISTERED AS DECIDE-ITEMS. NOT BUILT.**

### 4a. `Pinned-Dependencies` — 0/10 · **DECIDE-ITEM**

> *"dependency not pinned by hash detected — score normalized to 0"*

GitHub Actions are referenced **by tag** (`actions/checkout@v4`, `ossf/scorecard-action@v2.4.0`), not by commit
SHA. A tag is **mutable**: whoever controls the action can repoint it, and a re-run picks up different code.

**⚠ THE INCONSISTENCY IS THE ARGUMENT, and it should be stated plainly: PINNING IS ALREADY A DISCIPLINE IN
THIS REPO.** `apps/helper/internal/wfp/` is a **pinned, diverged fork** of `wireguard/windows` carrying an
explicit **re-diff obligation on every upstream bump** (VENDOR.md). The project already accepts the cost of
pinning where it judged the risk real. **So the CI surface is not unconsidered — it is inconsistent with a
standard the repo already holds itself to.**

**Why it is a decide-item and not a fix:** SHA-pinning changes how CI is allowed to run and **adds a standing
maintenance obligation** (pins must be bumped, or they rot into unpatched actions — the same trade already
paid for `internal/wfp`).

### 4b. `Token-Permissions` — 0/10 · **DECIDE-ITEM**

> *"detected GitHub workflow tokens with excessive permissions"*

Wants **`contents: read` at the top level**, with write granted **per job**. A compromised action currently
inherits more than it needs.

**Why it is a decide-item and not a fix:** it changes what every job is permitted to do. Getting it wrong
breaks CI in a way that looks like a flake, and several jobs legitimately need `security-events: write` and
`id-token: write`.

### 4c. `Vulnerabilities` — 0/10, *"71 existing vulnerabilities"* · **SEE THE FULL ANALYSIS BELOW**

---

# THE 71 — MEASURED, BECAUSE A BARE NUMBER IS FINDABLE BY A PROSPECT AND WE COULD NOT ANSWER IT

**Resolved via `api.osv.dev` per advisory (2026-08-01): 27 Go + 44 npm, across 6 Go packages and 15 npm
packages.** (A few IDs are `GO-… / GHSA-…` pairs, so the two counts overlap the raw 71.)

## DIRECT vs TRANSITIVE

| ecosystem | direct | transitive |
|---|---|---|
| **Go** | **5 packages** — `golang.org/x/crypto` (14 advisories) · `x/net` (9) · `kin-openapi` (2) · `x/oauth2` (1) · `x/sys` (1) | **0 declared-indirect**; plus **`stdlib`** (1), the Go toolchain, in no `go.mod` |
| **npm** | **5** — `electron`, `postcss`, `vite`, `vitest` *(all devDependencies)* · **`react-router-dom`** *(the ONLY runtime direct one)* | **10** — `esbuild`, `tar`, `js-yaml`, `brace-expansion`, `app-builder-lib`, `builder-util-runtime`, `fast-uri`, `launch-editor`, `react-router`, `vite-plus` |

## ⛔ THE REAL FINDING IS NOT 71. IT IS THAT **44 OF THEM HAVE NO REACHABILITY CHECK ANYWHERE — AND NO DEPENDENCY SCAN AT ALL.**

**`govulncheck` IS GO-ONLY BY CONSTRUCTION.** It analyses **Go** call graphs; `security.yml:41` runs it over a
matrix of exactly five Go modules. **It does not look at JavaScript or TypeScript, and never could.**

**Searched the entire `.github/` tree — the JS/TS side has NO dependency scanning of any kind:**

| candidate | present? |
|---|---|
| `npm audit` / `pnpm audit` | **no** |
| `osv-scanner` | **no** |
| `.github/dependabot.yml` | **no** |
| CodeQL `javascript-typescript` | present — but it scans **OUR SOURCE for security bugs**, not dependency advisories |
| Trivy | present — but `image-ref: tunnex-api:scan`, **the Go API image only**; not the web or client bundles |

**So the 44 npm advisories are UNMEASURED, not merely unreached.** The distinction matters: an unreachable
vulnerability has been *examined and dismissed*; an unmeasured one has *never been looked at*.

**THE HONEST SENTENCE FOR A PROSPECT:** *"Our Go dependencies are reachability-gated on every PR. Our
JavaScript dependencies currently are not."*

**MITIGATING, and it should be said in the same breath: 4 of the 5 DIRECT npm packages are devDependencies**
(`electron`, `postcss`, `vite`, `vitest`) — **build-time, not shipped in the SPA bundle**. Only
`react-router-dom` is a runtime dependency.

**NOT VERIFIED: which TRANSITIVE packages reach the shipped bundle.** That needs the build's real dependency
graph and is separate work. **Stated as unknown rather than assumed benign** — several (`tar`, `js-yaml`,
`brace-expansion`) are classic build-tool dependencies, but *classic* is not *measured*.

## THE 27 GO ADVISORIES — NO MODULE-LEVEL COVERAGE HOLE

**The repo contains exactly five `go.mod` files, and ALL FIVE are in the govulncheck matrix:** `apps/api`,
`apps/cli`, `apps/helper`, `apps/node`, `apps/operator`. Every advisory-bearing package resolves into one or
more of them — `x/crypto` → api+helper · `x/net` → helper+node+operator · `x/sys` → api+node+operator ·
`x/oauth2` → api+operator · `kin-openapi` → api.

**With `govulncheck` exit=0 on all five, those 27 are DECLARED-BUT-UNREACHABLE** — which is exactly what the
workflow's own comment claims the tool buys: *"a finding here is one we can actually fix, never inherited
noise."* **The claim holds, and it was checked rather than assumed.**

### ⚠ ONE CAVEAT ON "UNREACHABLE", because it is evidence and not proof

**govulncheck's verdict is a claim about THE CALL GRAPH IT CAN SEE.** Reflection, `cgo`, and dynamic dispatch
are where static reachability is weakest. **"Unreachable" here means "no path found by a sound-ish analyser",
not "no path exists."** Strong evidence; not a proof. Recorded so the distinction survives into whatever
public trust page quotes it.

## WHY SCORECARD AND `govulncheck` DISAGREE, AND WHY BOTH ARE RIGHT

**They answer different questions:**

- **Scorecard/OSV** asks *"does the MANIFEST declare a version with a known advisory?"* → **71**
- **`govulncheck`** asks *"is vulnerable code REACHABLE from our call graph?"* → **0**

**Neither is wrong and neither supersedes the other.** This is the repo's own
*[[unit tests prove behaviour, not reachability]]* note **running the other way**: there, a test proved
behaviour without proving the path was reachable; here, a scanner proves a *declaration* without proving the
path is reachable. **The same gap between "it is present" and "it is reached", read from opposite ends.**

---

# TRIGGERS

- **4a `Pinned-Dependencies` and 4b `Token-Permissions`** — **the next `security.yml` change, or an S11-class
  hardening pass.** Both change how CI is allowed to run; both are decide-items, not chores.
- **4c the npm reachability gap** — **unregistered until dispositioned.** The cheapest honest step is a
  manifest-level scan (`osv-scanner` or Dependabot) to make the 44 *measured*; **true reachability analysis for
  JS does not exist in the same form govulncheck provides for Go**, so parity is not achievable, only
  visibility.
- **`Signed-Releases`** — already carried by **S6.5b**'s trigger.
- **`Branch-Protection`** — **revisit only on a second maintainer.**
