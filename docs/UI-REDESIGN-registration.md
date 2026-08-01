# UI REDESIGN + DESKTOP-CLIENT SPLIT — REGISTRATION

**REGISTERED 2026-08-01, founder-directed, DURING the EPIC 13 box-walk. PAPER ONLY.**

> ## ⛔ THIS IS A REGISTRATION, NOT A STORY. NOTHING HERE IS BUILT OR STARTED.
>
> Both items are **decide-items awaiting a commit-one**. Neither has been ruled. A future session reading this
> file has a scope inventory and a question list — **it does not have permission to write code against it.**
>
> **ITEM A IS NOW RULED (2026-08-01) — see its section. ITEM B REMAINS A DECIDE-ITEM.**
>
> Item A reversed a locked decision (`PLAN.md`: *"React + Vite + Tailwind SPA; same bundle reused by the
> Electron renderer"*). It was recorded as a decide-item first and argued on paper before being ruled, which is
> the required order for reversing a lock.

**SOURCE ARTIFACT:** the Claude Design wireframe — 12 dashboard screens plus a desktop-client section — **held by
the founder**, not in this repo. Anything below that cannot be traced to the wireframe or to shipped code is
noted as such.

**SEQUENCING (founder-ruled):** EPIC 13 merge → **Item A ruling** → UI redesign → EPIC 11 remainder / BETA
BUNDLE → S12.1 → beta.

---

# ITEM A — THE DESKTOP CLIENT AS A SEPARATE UI

## What is locked today, and what would change

`PLAN.md` locks: **one bundle, two hosts.** `apps/web` is a React + Vite + Tailwind SPA, and the Electron
renderer loads the same build. S6.2 added the runtime branch — `window.tunnex` present → `setApiOrigin` +
bearer transport + *"Sign in with your browser"*.

**The proposal: separate them.** Recorded as a decide-item.

## THE CASE FOR (founder)

**A desktop VPN client and a multi-tenant admin console are different products.**

The client needs: connect/disconnect · tunnel status · assigned IP · split-tunnel · posture state · tray.

The client does not need: the audit viewer · the access-rule builder · K8s clusters · org settings · the ops
page.

**Today a user installs a VPN app and gets an admin console with a Connect button in it.**

## THE WIREFRAME ALREADY SPECIFIES THE CLIENT — this is the scope, captured

**State taxonomy, in full:**

| state | note |
|---|---|
| `CONNECTED` | |
| `CONNECTING` | |
| `DISCONNECTED` | |
| `REVOKED` | **loud** |
| `POSTURE_BLOCKED` | |
| `MIGRATE_FAILED` | copy: *"reconnect to retry"* |
| `AWAITING ADMIN APPROVAL` | |
| `HELPER OUTDATED` | |
| `KILL-SWITCH ENGAGED` | |
| `EXPIRED CREDS` | re-login |

**Tray vocabulary:** solid = connected (handshake fresh) · pulsing = connecting / re-key · grey = disconnected ·
**red badge = revoked or kill-switch, plus an OS notification.**

**The rule it renders:** *"Status is derived from handshake liveness — never green while the tunnel is dead."*
That is S6.3's already-shipped rule, drawn.

**Also specified:** byte counters in/out, duration, packets · Connect / Cancel / Disconnect with in-flight copy
(*"linking peers…"*, *"tearing down tunnel…"*) · split-tunnel toggle.

**MFA policy, stated as UI:** *"MFA touches the client only via browser re-auth: expired credentials →
'Sign in with your browser', never an in-app password field."* This is S5.1/S6.2's loopback flow expressed as a
UI rule.

## EVERY STATE MAPS TO SHIPPED BEHAVIOUR — and that must be VERIFIED, not assumed

Claimed backing: S6.3 (helper + kill-switch) · S6.4 (revocation-aware teardown, tray, notifications) · S7.3
(approval gate) · S7.5.3 (posture) · S7.5.5 (MFA-by-browser).

**AT COMMIT-ONE, CHECK EACH ONE AGAINST CODE.** Anything without a backing mechanism is **render-floor** and is
either cut or explicitly marked roadmap. A UI that can draw a state the product cannot produce is the
render-floor violation this repo already has a law for.

## ✅ RULED — 2026-08-01. THE THREE QUESTIONS ARE ANSWERED. THESE ARE DECISIONS, NOT OPTIONS.

### A1 — THE DESKTOP CLIENT IS **CONNECT-ONLY**

A tray app plus a window: **connect / disconnect · tunnel status · assigned IP · split-tunnel · posture state**,
and the ten-state taxonomy above.

**NOT in the client: no audit viewer · no rule builder · no org settings · no K8s · no user management.**

**Two reasons, both recorded:**

1. **Every comparable product ships connect-only with a web console. The audiences barely overlap** — the person
   who connects a laptop and the person who writes access rules are not usually the same person, and when they
   are, they are not doing both at the same moment.
2. **SECURITY, and this is the load-bearing half.** The client holds a `tnx_` bearer in the OS keychain, injected
   by the main process. **Connect-only means an unlocked laptop exposes a VPN client. Admin-capable means an
   unlocked laptop is a live admin console for the whole org.** The blast radius of a stolen unlocked machine is
   decided entirely by this ruling.

### A2 — ADMIN ACTIONS OPEN THE **SYSTEM BROWSER**. No dashboard is rendered in Electron.

**This EXTENDS an existing rule rather than inventing one.** S5.1's `/cli-auth` already completes authentication
in the system browser, and the wireframe already states that **MFA touches the client only via browser re-auth**
— never an in-app password field.

**One rule, no exceptions:** anything beyond connect-and-status leaves Electron and opens the browser.

### A3 — **OWN COMPONENTS, SHARED TOKENS**

The client gets **its own component set**: roughly five screens with a different interaction model, and the
dashboard's tables, filters, modals and pickers would be **dead weight** in it.

**Colours, typography and spacing move to `packages/shared`** so the two products read as one product.

**Divergent components, single visual identity.**

## CONSEQUENCE — the dashboard redesign's screen list SHRINKS

**The redesign DROPS connect / tunnel / tray entirely.** Those screens belong to the client and are designed
once, there.

**The client build is small** — five screens, own components, shared tokens.

**Neither product designs the other's screens. That was the whole reason Item A had to be ruled first**, and it
is now discharged: Item B's screen list can be fixed without reserving space for a connect flow.

## COST — now settled by A3

`packages/shared` holds **generated types only** today. A3 rules that **design tokens** (colour, type, spacing)
move there — a bounded, additive change — while **components deliberately do NOT**. That avoids the expensive
branch (hoisting a shared component library serving two different interaction models) and accepts the cheap one
(two component sets that look identical because they read the same tokens).

---

# ITEM B — DASHBOARD UI/UX REDESIGN (its own epic, arc-sized)

## Scope

**12 screens:** overview · gateways · sites · access · devices · users · flows · audit · cli · settings · k8s ·
ops. **Plus:** a command palette · edition/role toggles · density modes · toasts with undo.

**REDUCED BY ITEM A's RULING (2026-08-01): connect / tunnel / tray are NOT in this list.** They belong to the
desktop client, which is connect-only and has its own components. The redesign reserves no space for them.

## IT IS FAITHFUL TO THE PLAN — it renders shipped laws as UI

This is the reason it is worth building from rather than restarting:

- the **failed-load triad** on every list (the `loadOne` law)
- *"Client-reported, not attestation"* (S7.5.3)
- *"Not a ClusterIP DNAT — enforcement keys the pre-DNAT VIP"* (S10.3's C1)
- the **withheld destructive control** → *"edit the CR"* (S10.2)
- the one-time join token **shown once**
- **append-only** audit
- **verbatim** refusals

## IT CLOSES FOUR REGISTERED GAPS

1. **domain capture** — API since S2.5, never had a UI
2. **the CLI-sessions panel**
3. **the flow-log viewer** (S7.5.1b)
4. **group-member surface** (Deck-D)

## COMMIT-ONE DECIDE-ITEMS — in the order they constrain each other

### 1. RE-SKIN or RE-ARCHITECTURE

New tokens over existing components, or a new component model? **Tenfold cost difference, and it decides
everything below it.** Rule this first.

### 2. COMPONENT TEST TIER — lands FIRST or in the same story, NEVER after

**The S11 ledger: zero component coverage on the web app, and 4 of 15 walk findings lived there.**

A redesign is **the largest change that surface will ever take, landing on the least-guarded code in the repo.**
Deferring the test tier to "after the redesign" means the redesign is unguarded exactly when it is most
dangerous.

### 3. RENDER-FLOOR AUDIT, PER SCREEN — every panel names its endpoint or is marked roadmap

**TWO KNOWN VIOLATIONS ALREADY IN THE WIREFRAME:**

- **"Fleet risk" on the gateways screen** — risk scoring is a **Tier-3 name in the competitive ledger,
  explicitly NOT BUILT.**
- **"Site-Link Throughput" with a Jul 13-18 axis** — that is a **rate time-series.** S8.3 ruled metrics **L1 =
  cumulative-since-handshake ONLY**, *"no rate graphs, no sampling implied"*; time-series is **S11.1's** job.
  **That chart is L3 drawn as L1.**

**AUDIT THE REST THE SAME WAY:** System Health · Peer Connection Status · Network map · HA Hub Set · the ops
replica/leader/backup panels.

### 4. BULK MULTI-SELECT ON DESTRUCTIVE VERBS

**Bulk revoke is a different audit and confirmation problem than single revoke.** New security surface; needs
its own ruling, not an inherited one.

### 5. THEME × PALETTE × DENSITY

**Three toggles multiply the visual test surface.** Scope hard or cut to one.

### 6. EDITION GATING BEHIND ONE SEAM

The wireframe reads edition from `/meta`. **S12.1 replaces build-tag gating with a runtime `LicenseManager`.**

**If every gating decision routes through ONE hook, S12.1 rewrites the hook and nothing else.** This is why the
redesign **does NOT need to wait for S12.1** — and why the seam is **binding**, not advisory.

## ONE COPY FIX — RECORD IT NOW SO IT CANNOT SHIP

The wireframe has `editionTag: 'Free plan · cloud-hosted'` for the open edition.

**BOTH EDITIONS ARE SELF-HOSTED. The difference is FEATURES, not HOSTING.** *"cloud-hosted"* contradicts the
entire wedge — fully self-hosted, zero SaaS in the trust path, air-gappable — **and it would end up in a launch
screenshot.**

---

# SEQUENCING AND ITS INTERACTIONS

**EPIC 13 merge → Item A ruling → UI redesign → EPIC 11 remainder / BETA BUNDLE → S12.1 → beta.**

**The redesign does NOT wait for S12.1**, on the strength of decide-item 6.

**CONTENT-FREEZE INTERACTION:** the site's screenshots come from this UI. The BETA BUNDLE's emit-point **(b)** is
*"EPIC 11 close → CONTENT FREEZE."* **Redesigning BEFORE the joint launch is better than after** — otherwise the
launch ships screenshots of a UI that is about to change.

# TOOLING NOTE — for whoever builds this

**Visual iteration belongs in Claude Design; implementation belongs in Claude Code.** Iterating on renders inside
Code burns budget on loops Design does natively. Bring a settled wireframe to the implementation session.
