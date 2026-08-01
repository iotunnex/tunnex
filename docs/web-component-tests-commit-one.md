# `story/web-component-tests` — COMMIT-ONE

**PAPER. NOTHING BUILT.** Branch cut from `main`, component test tier ONLY, no redesign.

**It is slice one of the UI redesign** (`docs/UI-REDESIGN-registration.md`, Item B decide-item 2: *the component
test tier lands FIRST or in the same story, never after*), pulled forward onto the EXISTING web app so the
redesign lands on a guarded surface rather than creating one.

**No conflict with EPIC 13:** this touches only `apps/web`, and specifically **not** `openapi/openapi.yaml`,
which is where S13.1's change lives. It cannot collide with a future redesign branch either — it adds test
files rather than changing components.

---

# TWO PREMISE CORRECTIONS, STATED BEFORE THE DECISIONS

## 1. THE RUNNER DECISION IS INHERITED, NOT OPEN

`apps/web/package.json` already carries **`vitest` + `@testing-library/react` + `jsdom`**, with a `test` script
already wired into the gate set (`pnpm --filter @tunnex/web test`, CLAUDE.md). **Commit-one does not choose a
runner. It inherits one.** Re-opening that choice would be work with no finding behind it.

## 2. "ZERO COMPONENT COVERAGE" WAS WRONG — IT IS ONE FOOTHOLD AND EIGHTEEN SCREENS

**The registration's wording was the founder's and it was imprecise; corrected here rather than carried
forward.** `apps/web/test/` holds **11 files, 190 tests, all passing**:

| tier | files | what they are |
|---|---|---|
| **pure view-model** | 10 | `nodepick` · `healthview` · `policyview` · `postureview` · `sitesview` · `hubsetview` · `k8sview` · `authroute` · `deviceexport` · `enrollcommand` |
| **component (renders)** | **1** | `devicespage.test.tsx` |

`src/pages/` holds **18 screens**. So the accurate statement is **one foothold and eighteen screens** — and the
foothold's own header already says it is *"the foothold for the registered component-test-tier ledger item, not
a retroactive suite for the whole app."*

**Why the distinction matters:** the pure tier is real coverage of the RULES. What is missing is coverage of
whether the screens USE them — which is exactly the gap the foothold was written to close.

---

# D1 — WHAT "COVERED" MEANS: **THE WIRING, PLUS THE FAILURE PATH**

## D1(a) — the wiring, not the rendering

**A screen is covered when a test asserts that the decision the USER GETS matches the rule the pure tier
tests.**

The argument is the foothold's own justification, quoted because it is the whole case:

> *"Slice 3 extracted `defaultDeviceNode` into `src/lib`, which made the RULE testable. But a pure test of the
> rule passes just as happily while the page still reads `nodes[0]` — nothing asserted that the component uses
> the fix. That is the vacuous-check trap one tier up: the guard tests the extracted decision, not the decision
> the user actually gets."*

**EXPLICITLY NOT COVERAGE:** snapshot tests · `expect(render(<X/>)).toBeTruthy()` · "renders without crashing".
**Those are the render-floor version of a vacuous check** — they cannot fail for the reason the surface actually
breaks, which puts them in `docs/laws.md`'s existing family rather than outside it.

**The shape, from the foothold:** fleet state in → assert the *outbound call* carries the right value. Given a
fleet whose oldest gateway is revoked, the POST that creates a device must carry the ACTIVE gateway's id.

## D1(b) — **AND ITS FAILURE PATH** (founder-added clause)

**A screen is also covered when its FAILURE path is asserted.**

**The `loadOne` law is web-specific, and its violation mode is a REASSURING EMPTY STATE** — a screen that
renders perfectly and tells the user nothing. A wiring test that only walks the happy path **misses the exact
defect class this surface produces.**

So each covered screen asserts **both**:

1. **wiring** — the right value reaches the outbound call
2. **failure** — a failed load renders the failed-load triad, **NOT an empty list that reads as "you have none"**

**This clause is what makes the tier worth gating.** Happy-path-only wiring tests would pass on a screen that
silently swallows every error, which is the defect the redesign is most likely to reintroduce while moving
components around.

---

# D2 — WHICH SCREENS FIRST: ORDERED BY **DISAGREEMENT WITH THE BACKEND**

## THE FOUR WEB-SIDE WALK FINDINGS — VERIFIED AGAINST `walk-artifacts/S11/walk-record.md`, NOT ASSUMED

The founder's expectation was recorded as *"to be VERIFIED not accepted."* Verified:

| finding | what it was | web-side? |
|---|---|---|
| **WF-S11-7** | an **unrendered health kind** — a producer with no consumer. Cited across `docs/S13.1-decisions.md` as the canonical *"a surface added without censusing its consumers"* instance | **YES** — the UI never rendered a kind the backend emitted |
| **WF-S11-9** | *"gateway revoke exists in the API and never existed in the UI"* — fold landed in `apps/web/src/components/Gateways.tsx` | **YES** |
| **WF-S11-10** | a **revoked** gateway badged *"certificate expired — re-enroll this gateway"*. Root: `Gateways.tsx` **never suppressed health badges for revoked rows the way `Devices.tsx` always has** (`d.status !== "revoked" && …`) | **YES** — two web components disagreeing with each other |
| **WF-S11-10b** | the label was fixed, the **presence** was not: kinds summed to **4 on a fleet of 3**, because `FleetHealthCounts` walks `ListNodes` (`SELECT * FROM nodes WHERE org_id = $1`, no `revoked_at` filter) while *preflight's* query does filter | **YES** — the UI counted rows the backend did not consider live |

**The founder's expectation was correct on all four.**

## WHAT THEY SHARE — and it drives the order

**None of these is a rendering bug. Every one is a surface DISAGREEING WITH THE BACKEND about what exists or
what counts.**

- WF-S11-7 — the backend says a kind exists; the UI does not know it
- WF-S11-9 — the backend offers an action; the UI does not
- WF-S11-10 — one component thinks revoked rows count; its sibling does not
- WF-S11-10b — the UI counts four things where three exist

**So the order is by WHERE DISAGREEMENT IS MOST CONSEQUENTIAL, not by screen size or traffic:**

| # | screen | why here |
|---|---|---|
| **1** | **Gateways** (within `Sites.tsx` / `Gateways.tsx`) | **three of the four findings landed here.** Revoked-vs-active is the disagreement axis, and it is the one that produced a *confident wrong instruction* to undo a security action |
| **2** | **Devices** | already has the foothold — **extend it to the failure path**, which it does not yet cover. Also the sibling that got revoked-suppression RIGHT, so it is the reference implementation for screen 1's rule |
| **3** | **Access** | policy rules disagreeing with the compiled artifact is the highest-consequence disagreement in the product: a rule shown as active but not compiled is a silent authorization gap |
| **4** | **Kubernetes** | WF-S11-7's own territory — the unrendered health kind. The census in D3 is what stops it recurring |

**Users / Audit / Settings and the rest follow, ordered the same way, at the paper's discretion.**

---

# D3 — GATING: **A CENSUS, NEVER A PERCENTAGE**

**A coverage percentage is the gameable number.** It rises when someone tests something easy and says nothing
about whether the surface that breaks is guarded.

**Instead: assert that EVERY SCREEN IN A NAMED LIST HAS A WIRING TEST AND A FAILURE-PATH TEST.**

**The precedent is in this repo and it works:** `TestEveryHealthKindReachesItsMirrorSurfaces` — the same census
shape, minted for the same class of defect (a producer whose consumers were never enumerated), which is
WF-S11-7 exactly.

```
screen 19 is added  →  the census fails BY NAME  →  nobody has to remember
```

**THE LIST IS THE ARTIFACT.** Adding a screen without a test is a **red**, not a lint warning and not a drop in
a number.

**Two things the paper must settle:**

- **the list's source of truth** — enumerate `src/pages/*.tsx` at test time, or maintain an explicit list? The
  first cannot go stale but catches non-screens; the second is honest but must itself be guarded from
  forgetting. **Recommend: enumerate, with an explicit allow-list of non-screens (`Login`, `Signup`,
  `VerifyEmail`, `AcceptInvite`, …) that the census prints so an exemption is visible rather than silent.**
- **the tier's own vacuity guard** — a census that passes because it enumerates zero screens is the third
  instance of today's class. **The census must assert a MINIMUM COUNT it knows independently**, so an empty
  enumeration fails.

---

# WHAT THE PAPER OWES BEFORE ANY CODE

1. **The named list**, with exemptions stated and justified.
2. **A worked example of each half** — one wiring test, one failure-path test — as the pattern the rest copy.
3. **The census's own red** — prove it fails when a screen is added without a test. **`scripts/mutate.sh` now
   has a working self-test and can enforce this** (it was dead on arrival until 2026-08-01; see `docs/laws.md`).
4. **The gate's placement** — the existing `pnpm --filter @tunnex/web test` already runs in CI, so the census
   rides an existing gate rather than adding one. **Confirm that is still true when the census lands.**

# WHAT THIS BRANCH DOES NOT DO

- **No redesign.** No new components, no token extraction, no visual change.
- **No `openapi/openapi.yaml` change.** That is S13.1's file until EPIC 13 merges.
- **No `apps/client` work.** Item A ruled the client gets its own components; its test tier is that story's
  problem, not this one's.
