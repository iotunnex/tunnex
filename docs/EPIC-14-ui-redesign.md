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

## ⛔ EPIC-LEVEL RULE — THEMING AND EDITION GATING ARE SEPARATE MECHANISMS AND MUST NEVER MERGE

**Founder-ruled 2026-08-01. Every later slice inherits this.**

**Theme answers *"what does this look like"*. Edition answers *"does this exist at all"*.**

**A control hidden by COLOUR is rendered INVISIBLE rather than ABSENT** — still in the DOM, still announced to
a screen reader, still reachable by keyboard, still submittable. **Gone only to a sighted mouse user.** That is
a security-adjacent surface **failing open**, and the component tier would find it by role while a human review
would not.

**IT INTERACTS WITH DECIDE-ITEM 6** (edition gating behind ONE seam, so S12.1 rewrites a hook and nothing
else): **THE SEAM MUST BE A RENDER DECISION, NEVER A STYLE.** A hook that returns "don't render this" is
correct; a hook that returns a class name, an opacity, or a theme variant is the same defect wearing the seam's
name.


## ⛔ WIREFRAME ADAPTATION — founder-ruled 2026-08-01. KEEP the identity, EDIT the content.

**The visual identity is kept; the content is edited to what the product can BACK and an operator can ACT on.**

### KEEP — non-negotiable

- **dark palette, layered surfaces, the glassmorphism treatment**
- **Instrument Sans + JetBrains Mono**, with **mono RESERVED for technical values** — IPs, CIDRs, serials,
  commands, error codes. **That convention is doing real work** and is now a token-level rule, not a preference.
- **card-and-panel composition · the sectioned icon nav** (NETWORK / ACCESS / OBSERVE / OPERATE / SETTINGS) ·
  the page-header pattern with subtitle + control-plane health
- **THE COPY — carried VERBATIM.** It is the best part of the artifact, and it **states the product's laws in
  the interface**: *"Client-reported, not attestation"* · *"never green while dead"* · *"Failed — retry, never
  No rules"* · *"the destructive control is withheld, not fake — edit the CR"* · *"shown once, never again"* ·
  verbatim server refusals.

### CUT — with reasons recorded, so nobody re-adds them

| cut | reason |
|---|---|
| **Fleet risk** (Gateways) | risk scoring is an **unbuilt Tier-3 name** in the competitive ledger. Replaced by a **health-grouped gateway list** — same information, legible at a glance |
| **Site-Link Throughput as a rate time-series** (Overview) | S8.3 ruled metrics **L1 cumulative-since-handshake ONLY**, *"no rate graphs, no sampling implied."* Time-series is **S11.1's** job. Render **cumulative counters labelled as exactly that** |
| **FREE/ENTERPRISE and ADMIN/USER toggles** | wireframe demo controls. **A user cannot switch their own edition or role.** READ-ONLY BADGES, and the edition badge routes through **the ONE gating seam** |
| **Density (Cozy/Compact)** | cut per the earlier ruling — ship one density, keep the spacing scale so it could return |
| **The date-range picker** on screens that do not filter by date | it appears on nearly every screen and most ignore it. **Keep it ONLY where the data is time-ranged** — Access Events, Audit Log |
| **The floating action button** | purpose unclear; every screen already has a primary action in its header |
| **"Get started 2 of 4" as a persistent floating widget** | **make it part of the Overview EMPTY STATE.** A checklist that follows an established admin around is noise |

### REDUCE — the real risk in this design is DENSITY OF EXPLANATION

Nearly every screen carries **a chart PLUS a table PLUS two or three explanatory side panels.** That reads
beautifully and **can overwhelm at 3am when a gateway is down and someone needs one number.** S11's walk found a
lost gateway cost **four hand-run steps** — the UI's job there is to make **the next action obvious**, not to
explain the architecture.

**RULE, applied per screen at its commit-one:**

> **IS THIS SURFACE FOR UNDERSTANDING THE SYSTEM, OR FOR ACTING ON IT?**

- **ACTING** (Gateways · Devices · Access · Sites) — **the primary action and the thing that is wrong come
  first.** Explanatory panels collapse, or move to a details drawer.
- **UNDERSTANDING** (Overview · Operations · Edition · Audit Log) — keep the fuller treatment.

**DO NOT REMOVE the explanatory copy. MOVE it, so it is available and not in the way.**

### VISUALIZATIONS — keep the ones that carry meaning

**KEEP:** peer/device **donuts** (a proportion, read instantly) · **verdict-timeline histogram** (a rate over
time, which is what it says) · **sync-freshness bars** (a clock made visible) · **network map** (topology is
genuinely spatial).

**RE-EXAMINE at their screen's commit-one:** address-space heatmap · role pyramid · access-flow bipartite
curves · radial device fabric. **Each must answer: what question does this answer FASTER than a table? If the
answer is "none", it is a table.**

**EVERY SURVIVING PANEL STILL NAMES ITS ENDPOINT OR IS MARKED ROADMAP.** The render-floor audit applies to the
**reduced** set, not the original.

## ⛔ ANIMATION LIBRARY — GSAP IS **NOT** ADOPTED. Use **Motion (MIT)**. Ruled on measured licence facts.

**MEASURED, not recalled** (npm registry, 2026-08-01):

| | GSAP | Motion |
|---|---|---|
| version | **3.15.0** | **12.43.0** |
| `license` field | **`"Standard 'no charge' license: https://gsap.com/standard-license"`** | **`MIT`** |
| SPDX / OSI | **neither — a custom licence URL** | standard SPDX, OSI-approved |
| unpacked size | **6,111 KB** | **667 KB** (9× smaller) |

**Commercial use IS free** — that part of the founder's understanding is correct. **The problem is
REDISTRIBUTION, which is what this product does.**

1. **Tunnex is SELF-HOSTED. We ship a built bundle to customers who run it themselves — that is redistribution
   of GSAP's compiled code**, not internal use on a site we operate.
2. **The open edition is Apache-2.0 with a NOTICE file.** Embedding a **non-OSI, custom-licensed** dependency in
   an Apache-2.0 artifact means the recipient does **not** receive, for that portion, the freedoms the licence
   around it advertises. The GSAP terms forbid reverse-engineering and altering notices; Apache-2.0 grants
   modification. **They are not aligned.**
3. **The licence carries a COMPETITIVE-USE restriction** (tools that assist in building visual animations
   competing with Webflow). Tunnex is a VPN admin console, so it is **not triggered today** — but it is a
   custom clause requiring ongoing legal judgement, on a term the licensor can revise.

**THE FOUNDER'S OWN INSTRUCTION APPLIES: "if there is any doubt, name the alternative rather than proceeding."
The doubt is concrete, not theoretical. RULED: Motion (MIT), pinned. GSAP is not adopted.**

*(Repo precedent for this check: S6.3 pinned wireguard-go as MIT and recorded Wintun's redistribution terms in
NOTICE. Motion being MIT means **no NOTICE entry is strictly required**, but one will be added anyway for the
same reason Wintun's was — the NOTICE file is where a self-hoster looks.)*

### AND: THE ANIMATION LIBRARY IS **NEVER IN THE CRITICAL PATH**

**A dashboard that cannot render until an animation library loads is a regression on a surface admins open
during incidents.** Ruled:

- **CSS-first.** Transitions and simple motion use CSS, which costs nothing and needs no JS.
- **The library is LAZY-LOADED**, never imported by the app shell or any first-paint route.
- **Every animation degrades to "no animation"** if the library never loads. Nothing may depend on it to be
  legible, reachable, or actionable.

## ⛔ `prefers-reduced-motion` IS A GATE, NOT A COURTESY

**The wireframe's ONE media query is `prefers-reduced-motion`** — the artifact already respects it, and **an
animation library plus a re-architecture is exactly where that gets dropped.**

**ASSERT IT: with the preference set, animations must not run.** Gated as a test that fails, and **proven to
reject** — enable an animation under the preference and show the check go red. Same standard as the contrast
floor and the `ok` reservation in S14.1.

## THE MEASUREMENTS THAT SETTLED IT — re-recorded so the epic stands alone

From `docs/design/TUNNEX-wireframe-v2.html.txt` (2.9 MB, committed), counted by occurrence (`grep -o | wc -l`),
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
| **S14.3** | primitives THAT DO NOT EXIST YET — command palette + keyboard routing, toasts with undo, table/list primitives with semantic markup, **and DATA VISUALIZATION (see below)**. Density is CUT | **NO** |
| **S14.4+** | screens, in an order argued at the time | **YES** |

**WHY THIS ORDER.** S14.1-S14.3 import **no generated types**, so they cannot conflict with S13.1. Screens do —
and by the time S14.4 starts, S13.1 is merged.

**THE DEPENDENCY WAS NEVER THE CLOCK.** It is that **both branches edit `apps/web`**, and **S13.1 changes the
types `apps/web` imports**. Sequencing the type-free slices first removes the conflict entirely rather than
scheduling around it.

## DATA VISUALIZATION IS A SLICE, NOT A PER-SCREEN DETAIL — added to S14.3 (founder-directed)

**Counted across the 17 screens: roughly TEN distinct visualization types, none of them cards or tables.**
Discovering this per-screen would mean **ten independent decisions**, each made by whoever happened to build
that screen — which is how a design system acquires four charting libraries.

fleet-risk bubble plot (Gateways) · force-directed network map (Sites) · address-space heatmap grid (Routed
Ranges) · access-flow bipartite curves (Access) · radial device fabric (Devices) · role pyramid + MFA donut
(Users) · sync-freshness bars (Groups) · verdict-timeline histogram (Access Events) · actor-swimlane stream
(Audit Log) · peer donut + area chart (Overview)

**DECIDE AT S14.3's COMMIT-ONE:** charting library vs hand-rolled SVG · whether ONE primitive set covers all
ten or whether some are genuinely bespoke.

> **⛔ BINDING: EVERY VISUALIZATION IS SUBJECT TO THE RENDER-FLOOR AUDIT.**
>
> **A chart is the easiest place in a UI to draw a capability that does not exist.** *"Fleet risk"* and
> *"Site-Link Throughput"* are **already two known violations, and both are charts** — that is not a
> coincidence, it is the pattern. **Each visualization NAMES ITS ENDPOINT or is marked roadmap.**

**The adaptation ruling above already reduces this set** — four are KEPT outright, four are RE-EXAMINED against
*"what question does this answer faster than a table?"*, and two are CUT. **S14.3 decides the primitives for
what survives, not for the original ten.**

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

---

# ⛔ CARRIED OBLIGATIONS INTO S14.4+ — READ BEFORE WRITING ANY SCREEN COMMIT-ONE

**Recorded HERE, in the epic paper, rather than only in S14.3's slice report. A primitive whose trigger lives
in a transcript is dormant machinery with a story attached.**

## TWO VIZ PRIMITIVES SHIPPED WITHOUT A CONSUMER, EACH WITH A NAMED TRIGGER

| primitive | trigger | why it could not be wired in S14.3 |
|---|---|---|
| **`Histogram`** (binned count over discrete events) | **the screen that owns the window/filter controls — S14.4's ACCESS-EVENTS slice** | `/access-events` supplies `occurred_at` + `decision` (incl. `gap`), so the data exists. **The BINNING depends on a time window, and S14.3 had no basis to choose one.** Inventing a default would be a product decision made by an infrastructure slice. |
| **`NodeLink`** (topology) | **S14.4's SITES slice** | `siteLinkGraph` (S8.2) already computes the deterministic hub-and-spoke. **S8.3's Sites markup is being replaced in S14.4**, and wiring a graph into markup about to be rewritten is work done twice. |

**THE HONESTY GUARDS ARE ALREADY PROVEN ON BOTH** — failed-draws-nothing, gap-drawn-as-gap, roadmap-says-so,
numbers-as-text — so the *properties* are gated today. **What is outstanding is a consumer, not a guarantee.**

**⛔ IF EITHER SCREEN SLICE SHIPS WITHOUT WIRING ITS PRIMITIVE, THAT IS A FINDING, NOT A DEFERRAL** — the
primitive would then have shipped twice-unconsumed, which is the dormant-machinery law with a second chance
already spent.

## AND THE STANDING EXPECTATION FOR EVERY REMAINING SCREEN

**Adding semantics to a screen WILL break queries that were passing.** Thirteen screens remain.
**Each break is evidence of a pre-existing weak assertion, not of a bad improvement** — fix the QUERY, by role
and accessible name, never by narrowing to a test-id (docs/laws.md).

---

# ⛔ THE SECTION PROTOCOL — FOUNDER-RULED 2026-08-01. BINDING ON EVERY REMAINING SLICE. NO EXCEPTIONS.

> ## **1. READ THE WIREFRAME FIRST** — `docs/design/TUNNEX-wireframe-v2.html.txt`, for that specific section,
> ## **by extraction. Not a summary. Not memory. Not a previous session's notes.**
> ## **2. REMOVE WHAT IS NOT APPLICABLE** — no endpoint, no capability, or no screen behind it → **CUT IT AND
> ## SAY SO, WITH THE REASON. Cutting is a decision that gets RECORDED, never a silence.**
> ## **3. DESIGN THE SECTION** against what the wireframe actually shows — spacing, hierarchy, columns,
> ## chrome, copy. **Take layout and copy. Take none of its DOM.**
> ## **4. CODE IT.**
> ## **5. BRING IT FOR REVIEW** — the founder checks it on localhost, against the wireframe, before anything
> ## merges.
> ## **6. THE FOUNDER GIVES THE GO-AHEAD. Only then does the next section start.**

## WHY THIS RULE EXISTS — recorded plainly, because the reason is the whole point

**FOUR SLICES WERE BUILT FROM A SUMMARY OF THE WIREFRAME RATHER THAN THE WIREFRAME**, because a
session-scoped instruction of the founder's — *"do not read it for design detail"* — was never lifted and was
carried forward stale.

**THE RESULT PASSED EVERY AUTOMATED GATE:**

- **388 tests, 31 files, green**
- **CI green at a named sha**, e2e included
- **mutation-proven** — including three mutations that found real missing guards
- typecheck, build, drift guard, contrast gate, coverage census, all green

**AND IT DID NOT LOOK LIKE THE DESIGN.**

> ## **NO TEST IN THIS EPIC CAN CATCH THAT.**

Every gate we built asks *"is this correct, honest, and non-vacuous?"* — and every one answered **yes**.
**None of them can ask "does this look like the thing we are trying to build?"** jsdom has no layout engine, no
viewport, no eye. **The founder on localhost is not a courtesy review. It is the ONLY check for an entire class
of failure, and it is therefore a REQUIRED GATE.**

## DEFINITION OF DONE — AMENDED FOR EVERY SCREEN SLICE

**A slice is NOT complete until the founder has seen it running and said GO.** Gate green, CI green and
mutation proofs are **necessary and not sufficient** — the same relationship `make web-gate` has to `e2e`, one
level further out.

## ⛔ NO EM-DASHES IN RENDERED COPY (founder-ruled 2026-08-01)

**It reads as AI slop.** Use a full stop, a colon, or a comma. Applies to every user-facing string: labels,
captions, empty states, error messages, confirm dialogs, toasts.

**Also not a placeholder glyph** — a failed stat reads `n/a`, never `—`.

**Comments are exempt** (they are not rendered), but rendered copy is scanned per section.

**SCOPE, stated honestly: 163 rendered em-dashes remain across 16 screens** that predate this ruling —
`policyview` 23 · `Sites` 21 · `Access` 20 · `Gateways` 9 · `TunnelControl` 9, and a long tail. **They are NOT
swept globally**: a mass rewrite of copy across screens nobody is looking at is exactly the unreviewable change
the section protocol exists to prevent. **Each screen's section clears its own**, and clearing them is part of
that section's definition of done.

**Overview is clear as of `S14.4`.**

# ⛔ THE THREE-WAY TEST — every panel, every screen (founder-ruled 2026-08-01)

> ## **A CARD WITH AN EMPTY STATE PROMISES THAT DATA WILL FILL IT.**
> ## **A CAPABILITY THAT DOES NOT EXIST MUST NOT MAKE THAT PROMISE.**

> ## ⛔ IT CLASSIFIES **PANEL + RENDERING**, NOT THE PANEL ALONE — the two can differ, and that is the case
> ## that gets cut wrongly.

| situation | verdict |
|---|---|
| **endpoint exists, no data yet** | **BUILD IT, with an empty state.** The empty state is honest: the capability is there and waiting. |
| **subject supported, the WIREFRAME'S RENDERING unsupported** | ⭐ **BUILD THE PANEL IN A DIFFERENT FORM.** Cut the drawing, keep the subject. |
| **endpoint exists, the SPEC FORBIDS the use entirely** | **ABSENT, with the reason recorded on the panel grid.** |
| **no endpoint, no capability** | **ABSENT, marked roadmap.** |

## ⭐ THE SPLIT CASE, AND WHY THE TEST NEEDED REFINING — *Site-Link Throughput*

**The first version of this test classified PANELS. This case splits: the SUBJECT is supported and the
wireframe's CHOSEN RENDERING is not.**

- **The counters EXIST.** `HubMemberMetrics.rx_bytes` / `tx_bytes` are served today.
- **The TIME SERIES does not.** The spec calls them *"a raw gauge since the last handshake (display only,
  never summed as monotonic)"* — **the counter RESETS at every handshake**, so a 7-day line would look like
  throughput and not be throughput, at any data volume, forever.
- **S8.3 already ruled the honest form:** metrics-L1 = *"cumulative-since-handshake totals labelled as exactly
  that, no rate graphs, no sampling implied."*

**SO THE PANEL IS CATEGORY ONE AND THE CHART IS CATEGORY TWO.** Built as **`Site-Link Traffic`** — inbound and
outbound **numbers**, with the caption stating they reset at each handshake and are not a rate. **The chart
stays absent**, and the rate/time-series version is **owed to S11.1**, where it gets an endpoint.

> **CUTTING THE PANEL BECAUSE ITS DRAWN FORM WAS UNSUPPORTED WOULD HAVE SAID *"we cannot show traffic at
> all"* — WHICH IS FALSE.** A missing chart is a rendering decision; a missing panel is a claim about the
> product's capability, and the two must not be confused.

**WHY THE MIDDLE ROW IS NOT THE FIRST ROW, using the case that forced the rule.** *Site-Link Throughput* is
**not a built feature awaiting data.** `openapi.yaml` describes the byte fields as *"a raw gauge since the last
handshake, display only, never summed as monotonic."* **A 7-day rate series drawn from a counter that RESETS
ON EVERY HANDSHAKE would look like throughput and not be throughput — at any data volume, forever.** An empty
card would tell the reader to wait for something that is never coming. **Absent is honest; an empty promise is
not.** Time-series is S11.1's job and needs an endpoint that does not exist.

## ⚠ RE-CLASSIFICATION — TWO CUTS WERE CATEGORY ONE WEARING CATEGORY THREE'S LABEL

| panel | was cut as | **actually** | now |
|---|---|---|---|
| **HA Hub Set** | *"no hub/generation/pin field exists"* | **`GET /hub-set` + `HubSet{generation, members[]}` exist; `hubsetview.ts` already projects primary/standby, pins and handshake age** | **BUILT, empty state** |
| **Network map** | *"no `SiteLink` schema"* | true, **and `assembleTopology()` already exists** in `sitesview.ts` | **BUILD, empty state** |
| **Device Posture** | *"deferred to the Devices section"* | **`Device` carries `health_state` / `health_blocked` / `health_reported_at` today** | **BUILT** |
| Peer Connection Status | built from **nodes** | the panel counts **devices** — a different, larger population | **RE-SOURCED** |
| Site-Link Throughput | no endpoint | **category two** — the field exists and its own description forbids the reading | **ABSENT, reason recorded** |
| Fleet risk | Tier-3, not built | **category three** | **ABSENT, roadmap** |
| Alerts | *"composed from sources this screen does not own"* | **category one** — node health kinds and `/audit-logs` both exist | **RE-OPENED, owed** |

**THE CAUSE OF THE MIS-CLASSIFICATION, recorded because it is the third instance in one day: AN ABSENCE FOUND
BY LOOKING IN ONE PLACE.** The `Site` schema was searched for hub fields, none were found, and the capability
was declared missing — **while the hub set was its own endpoint and its own schema all along.** The first two
instances were caught by the assistant; **this one the founder caught**, which is worse.

**BINDING: every cut on every screen is re-checked against the three-way test before that screen ships.**

# ⛔ OWED BEFORE THE EPIC CLOSES — THE PLAYWRIGHT VIEWPORT LEG

**Registered at S14.2, trigger fired at S14.4 (the first screen slice). STILL UNBUILT.**

**The case for it stopped being theoretical on 2026-08-01.** A spacing override silently re-keyed 128 use
sites across 17 screens; a donut rendered at a quarter size; **`tsc`, 422 tests, the drift guard, the contrast
gate, the coverage census and CI-with-e2e all reported green.** The defect was found by the founder looking at
one screenshot.

**AND ONLY ONE SCREEN WAS BEING LOOKED AT.** The other sixteen were equally broken and equally silent, and
would have stayed silent until their own section arrived — **weeks later, with the cause buried.**

> **A HUMAN GATE IS A SPOTLIGHT. A SCREENSHOT DIFF ACROSS EVERY SCREEN AT EVERY BREAKPOINT IS THE ONLY
> INSTRUMENT THAT COVERS THE SCREENS NOBODY IS CURRENTLY REVIEWING.**

**IT DOES NOT REPLACE THE FOUNDER'S REVIEW** — it cannot judge whether a design is right, only whether it
CHANGED. **The two answer different questions**, and the epic needs both: the founder for *is this correct*,
the diff for *did anything move that nobody asked to move*.

## ⚠ REGISTERED — THE CONTROL-PLANE HEALTH INDICATOR RENDERS TWICE ON OVERVIEW

**Found by the viewport leg** (a Playwright strict-mode violation: `control plane operational` resolved to two
elements).

- the **shell footer** has carried it since S4.x
- **S14.4's System Health panel** now shows the same value

**NOT RESOLVED IN THE VIEWPORT LEG.** A visual suite must not become the place where product decisions get made
quietly — the test is scoped to the panel and the duplication is registered.

**THE LIKELY ANSWER, founder-confirmed as the reasoning but NOT as the disposition:** the README's layout is
**sidebar + topbar + page body, with no footer at all**, and System Health is the designed home for this value.
So the footer indicator is probably redundant. **But removing it is a SHELL change touching all 18 screens**,
and the shell's footer is also where `e2e` asserts the SPA issues `GET /healthz` — so it needs its own
disposition, not a drive-by deletion.

**FOUNDER RULING (2026-08-02): REGISTER, DO NOT RESOLVE.** *"It is a product decision, not a test fix, and a
visual suite must never be the place a product decision gets made quietly."*

**TRIGGER: the next shell-touching section.**

## ⚠ REGISTERED — THE DRAWER `Menu` BUTTON OVERLAPS THE PAGE HEADING AT 390

**Found by reading the harvested 390 baseline before committing it.** The button is
`absolute left-4 top-4` (`AppShell.tsx:156`), positioned over the page body rather than reserving space in it.
At drawer width it lands **on top of the `<h1>`** — on Overview the word `Menu` sits across `Overview`.

**IT IS ON EVERY SCREEN AT PHONE WIDTH**, like the 65px header overflow, and like that one it has been live
since S14.2. This is the leg's **third** pre-existing main defect.

**NOT FIXED IN THE LEG** — the founder's last round was funded for *harvest, land, green*, and a shell fix is
neither.

> **A BASELINE IS NOT A STATEMENT THAT THE IMAGE IS RIGHT. IT IS A STATEMENT THAT THIS IS WHAT WE HAVE.
> THE DIFFERENCE ONLY HOLDS IF WHAT IS WRONG IN IT IS WRITTEN DOWN.**

**⚠ UPDATED 2026-08-02, AND THE UPDATE IS A LOSS OF COVERAGE.** This defect *was* committed into the
`overview-390` baseline — frozen, visible, and registered. **Both Overview baselines have since been dropped**
(founder-ruled: the surface is data-determined and flaked at 621px across runs of identical code; see
`docs/laws.md`). **So nothing now holds this defect but prose.** No artifact will notice if it changes, and
nothing will fail if it is fixed silently or made worse.

**That is the price of the reduction, and it is recorded rather than glossed.** The same sentence sits in
`e2e/visual/visual.spec.ts-snapshots/README.md`, next to the baselines that remain, so a reader who never
opens this file still meets it.

**TRIGGER: the same one as the duplicated indicator — the next shell-touching section.**

### ✅ RULED OUT, and worth recording as the thing it was NOT

The 390 baseline also shows the **triage bottom bar rendered mid-page**, around y≈1176. That one is a
**FULL-PAGE CAPTURE ARTIFACT, not a defect**: `TriageBar` is `sticky bottom-0` (`AppShell.tsx:197`), so it
sticks to the viewport bottom in a real browser and renders at its flow position in a stitched full-page shot.
It is deterministic — same viewport height every run — so it neither flakes nor needs masking.

**Two anomalies in one image, one real and one not.** Reading the image is what separated them; the suite would
have committed both without comment.

## ⚠ REGISTERED — THE PLAN-POINTER PUSH BYPASSES BRANCH PROTECTION, AND IT HAS NOW DONE SO TWICE

**Founder-filed 2026-08-02: *"a recurring bypass is a convention about to stop being one."***

Both occurrences were the same act — updating PLAN.md's re-entry checkpoint directly on `main` after a merge,
which the story protocol explicitly permits for process/docs corrections. Both were reported at the time.
**The reporting is not the issue; the recurrence is.**

```
remote: Bypassed rule violations for refs/heads/main:
remote: - 3 of 3 required status checks are expected.
```

**WHY IT IS WORTH A RULING RATHER THAN A HABIT.** The bypass works because `enforce_admins` is `false`. So
the protection that exists for everyone is, in practice, advisory for the one account that does the merges —
and *"docs-only"* is a judgement made by the pusher, in the moment, with nothing checking it. **The exact
push above could have carried product code and the output would have read identically.** That is the gap: not
that this push was dangerous, but that **nothing distinguishes a safe bypass from an unsafe one except the
intent of whoever typed it.**

**TWO DISPOSITIONS, NEITHER TAKEN NOW (founder: *"do not decide it now"*):**

**(a) GIVE THE POINTER A PATH THAT DOES NOT BYPASS** — either a docs-only CI path that satisfies the required
contexts, or land the pointer **inside the next PR** instead of directly. The second costs nothing and
removes the bypass entirely; its only downside is that the pointer lags a merge by one PR, which is precisely
the staleness the re-entry rule exists to prevent.

**(b) WRITE THE BYPASS INTO THE PROTOCOL AS A NAMED EXCEPTION**, with its rationale and its limits stated
(which files, which branch, what may never ride along). **If it is going to keep happening, it should be a
rule rather than a repeated judgement call.**

**TRIGGER: the next branch-protection change.**

## ⚠ OWED — OVERVIEW NEEDS A RE-LOOK WITH SEEDED DATA (deferred 2026-08-02, NOT waived)

**THE FOUNDER APPROVED A DEFECTIVE RENDERING, AND THE CORRECTION HAS NOT BEEN SEEN BY ANYONE.**

`Donut`'s `neutral` slice referenced `var(--tnx-ink-600)`, which **does not exist**. CSS resolves an
undefined custom property to the INITIAL value, so **every neutral slice has rendered BLACK since S14.3** —
including on Overview, reviewed on localhost and passed.

**THE FIX IS BOUNDED, AND THE BOUND IS WHY THIS IS A DEFERRAL AND NOT A BLOCKER:**

- `--tnx-ink-600` had **exactly one** reference in the codebase: `TONE_VAR.neutral` in `viz.tsx`
- `TONE_VAR` has **exactly two** consumers, both inside `Donut`: the arc `stroke` and the legend swatch
- **so the entire delta is: neutral donut slices change from black to `#858582`.** Nothing else moves
- `test/tokenrefs.test.ts` now proves no fourth reference exists

**WHY DEFERRED RATHER THAN CLOSED:** the founder's stack has **zero devices**, so the Peer Connection Status
donut renders its EMPTY STATE, not an arc. **He would be looking for a difference that cannot appear.**

**TRIGGER: the next review that has seeded data.** Fold it in there rather than scheduling a look that
cannot see anything.

> **A BOUNDED FIX IS STILL AN UNREVIEWED CHANGE. THE BOUND SAYS HOW MUCH TO RE-LOOK AT — NOT WHETHER TO.**
