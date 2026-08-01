# EPIC 14 — UI REDESIGN

**OPENED 2026-08-01, founder-directed. Promotes `docs/UI-REDESIGN-registration.md` from a registration to an
epic. Build starts now.**

> ## WHAT IS ALREADY RULED — carried forward, NOT re-litigated
>
> The registration was argued and ruled before this epic opened. **These are decisions. A future session does
> not re-open them; it implements against them.**

| ruling | where it came from |
|---|---|
| **The desktop client is a SEPARATE product, CONNECT-ONLY** — tray + window, no admin surface. Admin actions open the SYSTEM BROWSER. **Own components, SHARED TOKENS.** | Item A, ruled 2026-08-01 |
| **This is a RE-ARCHITECTURE, not a re-skin** — measured, not judged | decide-item 1, ruled |
| **The wireframe is a VISUAL specification ONLY** | measured |
| **SEMANTIC, ACCESSIBLE MARKUP IS A HARD REQUIREMENT** | consequence 1 |
| **The component tier's FIVE QUERY RULES bind every test** | consequence 2 |
| **17 screens, not 12** | corrected by measurement |
| **RESPONSIVE IS NEW DESIGN WORK** — there is nothing to adapt | measured |

## THE MEASUREMENTS THAT SETTLED IT — re-recorded so the epic stands alone

From `docs/design/TUNNEX-wireframe-v2.html` (2.9 MB, committed), counted by occurrence (`grep -o | wc -l`),
**not** `grep -c` — the file is 405 lines and line-counting undercounts by orders of magnitude:

| measure | count | what it means |
|---|---|---|
| `<div` | **1,018** | against **1** `<button>` |
| `<table` · `<label` · `<nav` · `<select` | **0 · 0 · 0 · 0** | there is no semantic markup to inherit |
| **`aria-` anywhere** | **0** | a WCAG 2.1 AA failure on its face |
| inline `style=` | **2,134** | mostly escaped (`style=\"`) |
| `className=` vs `class=` | **0** vs **109** | **rendered HTML embedded in JS string literals — not React source** |
| `@media` | **1**, and it is `prefers-reduced-motion` | **ZERO width-based breakpoints** |
| `min-width:1280px` on the ROOT | **1** | **a desktop-only contract, asserted positively** |
| `clamp()` | **0** | nothing fluid |
| `backdrop-filter` | **242** | layered glassmorphism — a rendering model |

**THE ARTIFACT CANNOT BE IMPORTED, EXTENDED, OR REFACTORED INTO THE APP. IT CAN ONLY BE READ AS A PICTURE.**
Take its **LAYOUT, HIERARCHY and COPY**. Take **none** of its DOM.

**And below 1280px it does not reflow — it overflows.** Responsive behaviour is not underspecified in the
artifact; it is **positively excluded**. Every screen's breakpoint behaviour is new design work.

---

# SLICE ORDER — BOTTOM OF THE STACK FIRST, AND THE REASON IS NOT THE CLOCK

| slice | scope | imports generated types? |
|---|---|---|
| **S14.1** | design tokens · theme system · accessibility foundations | **NO** |
| **S14.2** | layout shell — nav, responsive grid, breakpoints | **NO** |
| **S14.3** | primitives THAT DO NOT EXIST YET — command palette + keyboard routing, toasts with undo, density (if it survives), table/list primitives with semantic markup | **NO** |
| **S14.4+** | screens, in an order argued at the time | **YES** |

**WHY THIS ORDER.** S14.1-S14.3 import **no generated types**, so they cannot conflict with S13.1. Screens do —
and by the time S14.4 starts, S13.1 is merged.

**THE DEPENDENCY WAS NEVER THE CLOCK.** It is that **both branches edit `apps/web`**, and **S13.1 changes the
types `apps/web` imports**. Sequencing the type-free slices first removes the conflict entirely rather than
scheduling around it.

# TWO DECIDE-ITEMS THE FOUNDER OWES — they gate S14.2/S14.3, NOT S14.1

1. **Is mobile the FULL dashboard, or a TRIAGE SUBSET?** Approving a device queue and reading gateway health
   work on a phone. **An access-rule builder with source, destination, port scope and expiry does not — and a
   bad mobile rule builder is WORSE THAN NONE, because it is a security surface where a mis-tap grants access.**
2. **Does DENSITY survive five breakpoints?** **5 widths × 3 themes × 2 densities = 30 visual states per
   screen**, ×17 screens = **510**. At mobile width *compact* and *cozy* are arguably the same decision made
   twice — the viewport has already made it.

**S14.1 starts without them. Ask again when S14.2 opens.**

# WHAT THIS EPIC INHERITS FROM `story/web-component-tests`

**That branch is CI-green at `00a736d` (PR #44) and its HANDOFF section is the binding contract.** It carries:

- the **five query rules**
- the **census** — 8 covered, 11 exempt with reasons inline, asserted `toBe` not `>=`, so a new screen fails
  **by name** and the number moves deliberately
- the **ceiling** — ~13 accountable screens after this epic (`subnets` · `cli` · `flows` · `ops` · `license` ·
  `onboarding`), so the census total is **a ledger of today, not a target**
- the **shedder constraints** — `Sites → subnets`, `Settings → cli + license`
- the **`Loaded<T>` contract**, and that **widening it silently converts a compile-time guarantee into a
  discipline nobody audits**

> ## ⛔ THE TIER'S PURPOSE, WHICH IS THIS EPIC'S METHOD
>
> **THE REDESIGN IS A REFACTOR PERFORMED UNDER A GREEN SUITE.** Not rewritten and re-tested afterwards.
>
> **A test that has to be rewritten to pass is a SIGNAL THAT THE REDESIGN CHANGED A DECISION** — either a bug,
> or a deliberate change that must be RECORDED. **It is not test debt.** Rewriting it destroys the only signal
> that says so.

**TWO REGISTERED FINDINGS carry in:** the **SSO failed-load** finding (registered, NOT fixed, ranked
destructive — an admin reconfiguring against a live IdP may overwrite a working config because a transient 500
said "not configured") and the **Sites revoked-badge** guard (fixed on that branch under its one named
exception).

# STILL OPEN FROM THE REGISTRATION — decide at each slice's commit-one

- **bulk multi-select on destructive verbs** — a different audit and confirmation problem than single revoke
- **theme × palette × density** — must be ruled WITH the responsive item, since they multiply
- **edition gating behind ONE seam** — so S12.1 rewrites a hook and nothing else
- **the copy fix:** `'Free plan · cloud-hosted'` is wrong. **Both editions are self-hosted**; the difference is
  features. It contradicts the wedge and would reach a launch screenshot.

# TOOLING

**Visual iteration belongs in Claude Design; implementation in Claude Code.** Iterating on renders inside Code
burns budget on loops Design does natively. Bring a settled wireframe to the implementation session.
