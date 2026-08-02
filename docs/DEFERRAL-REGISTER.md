# THE DEFERRAL REGISTER — one line per deferral, each with a NAMED TRIGGER.

**Split out of `docs/CUT-REGISTER.md` on 2026-08-02, founder-ordered, on that file's own founding rationale:
a register works because a grep is cheap, and it stops working when it holds two different questions.**

> ## **A CUT ANSWERS "IS THIS IN SCOPE?". A DEFERRAL ANSWERS "WHEN DOES THIS HAPPEN?".**
> ## **A DEFERRAL WITHOUT A TRIGGER IS NOT DEFERRED. IT IS DROPPED, SLOWLY.**

**HOW TO USE IT:** `grep -i '<name>' docs/DEFERRAL-REGISTER.md` before assuming something is unbuilt by
choice. **HOW TO ADD:** the deferral, its trigger, why it is deferred, **where it was FOUND, and whether it
has been REVIEWED.** Provenance is part of the entry — an item whose origin nobody can name gets re-litigated
from scratch.

---

| deferral | trigger | why deferred | **found where** | **reviewed?** |
|---|---|---|---|---|
| **`site_id` on `RoutedRange`** | **an org crosses ~50 sites**, OR any story that revisits what `/routed-ranges` may carry | `/routed-ranges` is a **device-facing projection** — *ranges only, no keys, endpoints, pool or policy*. Adding an org-structure field needs a decision about whether a DEVICE should learn site topology, which is not a screen's call. Until then attribution is a per-visit fan-out | S14.7 commit-one, endpoint census | **NO** — paper only, not yet reviewed |
| **The ~50-site fan-out tripwire** (Routed Ranges `SITE` column) | **51 requests / ~9 waves at 6-per-origin.** Fires when an org's site count approaches 50 | The fan-out is correct and cheap at realistic N. It is recorded as a **THRESHOLD, not a limit**, so the next reader inherits the number instead of rediscovering it at a customer | S14.7 commit-one, after the founder asked what happens at 50 | **NO** — paper only |
| **`Modal` has no Escape / focus-trap / initial-focus / focus-return** | the next slice that touches `Modal`, or S14.8 | shared primitive, 20 call sites, and it DECLARES `aria-modal="true"` while implementing none of it | S14.5, founder-ordered measurement after I reported only *"no Escape"* from a single grep. All four behaviours then measured | **NO — REGISTERED, NEVER REVIEWED.** Not fixed, not looked at on a screen |
| **`site_link_down` is an org-level headline printed per row** | the next control-plane story touching site-link health | suppressing a server-owned verdict client-side is the one-truth violation already swept off Sites | S14.5 Sites map (N=1, meaningless), evidence upgraded S14.6 Gateways (N=6, four rows incl. the hub) | **Founder SAW it** on both screens and ruled *register, do not resolve* — the DEFECT is reviewed, the FIX is unruled |
| **The peer/device count column** (Gateways) | its own slice | spec + codegen ×3 + drift guard + both editions + query-lint + sqlc | S14.6 commit-one; founder corrected my "one cheap query" estimate | **Founder ruled it its own slice.** Scope reviewed, not built |
| **`Histogram` has no shipping consumer** | EPIC 14 close | Access Events moved REDESIGN → BUILD, so the clock got LONGER — which is how a deferral becomes permanent | S14.3 build (named Access Events as consumer); flagged S14.5 when the nav audit moved Access Events REDESIGN → BUILD | **NO** — never reviewed; the component exists only in the gallery |
| **Access screen's em-dashes** | the Access section pass | **MEASURED S14.7:** `policyview.ts:436` *"Rule status unavailable — refresh."* and `:442` *"Policy not enforced — open mesh."*, both asserted in `accesswiring.test.tsx:103,144`. **Those two assertions WILL break when that section clears its em-dashes — known in advance rather than discovered** | S14.7, censusing the em-dash blast radius across the component tier | **NO** — measured, not looked at |
| **Overview layout reflow (10th card leaves System Health alone on row 4)** | **the next Overview-touching slice, or EPIC 14 close, whichever is first** | Layout regression on a MERGED, founder-approved screen — 9 panels filled 3 exact rows; the 10th leaves System Health alone on the last row. Measured after founder acceptance (never accepted, only reported). Fix is 1 line: Kubernetes card goes last. Visual leg is advisory & Overview baselines dropped at viewport leg so nothing notices. 390 behavior UNMEASURED (checked at lg only). **That is now three Overview defects held in prose alone (65px header overflow, Menu button over <h1>, 10th card reflow), with no artifact behind any of them — that sentence is the finding, not the individual items.** | S14.8 Kubernetes card reflow measurement | **Founder-ruled, deliberately deferred.** Measured post-acceptance, registered not fixed. |
| **Global em-dash sweep & CI lint rule** | **EPIC 14 close or dedicated polish sweep** | The per-section obligation "each section clears its own em-dashes" proved decorative in practice (163→19 burn-down occurred in S14.6 global sweep while per-screen passes preserved them for test assertions). Discharged by a final global sweep clearing remaining 19 em-dashes alongside an ESLint/CI regression rule preventing re-introduction | S14.10 audit finding | **REGISTERED** — per-section rule acknowledged decorative |
| **Device fabric graph** (online / idle / blocked node graph) | **its own slice** | Center node's "1000 peers" count is blocked on the exact same gap that made PEERS its own slice at S14.6 (DB `devices WHERE node_id` count vs gateway WireGuard peer graph). Requires a dedicated peer graph query + codegen slice | S14.10 element extraction | **REGISTERED** — deferred to its own slice |
| **`TUNNEL` column (`split` / `full`) on Devices table** | **its own slice / spec update** | `full_tunnel` exists on `CreateDeviceRequest` but is **NOT emitted on the `Device` response schema** in `openapi.yaml`. Displaying tunnel mode on list items requires spec + codegen + SQL select update | S14.10 element extraction | **REGISTERED** — absent endpoint schema field |

---

## ⛔ THE "`ON CONFLICT … DO NOTHING` ON A TIME-RELATIVE FIXTURE" CLASS — its own line, because it made every device freshness state false

> ### **A FIXTURE FOR A LIVE SYSTEM WRITES `now() - interval '3 minutes'`. THAT IS THREE MINUTES AGO ONCE,**
> ### **AND THEN AGES FOREVER. WITH `DO NOTHING`, EVERY RE-SEED REPORTS SUCCESS AND CHANGES NOTHING.**

**S14.10. Two tables, both silent, and the consequence was total:**

| table | what aged | what the screen showed |
|---|---|---|
| `device_health` | `reported_at` drifted past `HealthStaleTTL` (30 min) | `health_state: unknown` for devices seeded as compliant/noncompliant, **and the sweep correctly cleared `health_blocked`** — so the device named `blocked-device` was not blocked |
| `device_status` | `last_handshake_at` | every device drifted OFFLINE |

**SO ANY SECTION REVIEWED AGAINST A RE-SEEDED STACK SAW STALE TIMESTAMPS AND NOBODY KNEW.** The seed exited 0
every time.

**⛔ AND THE FIX ALREADY EXISTED IN THE SAME FILE, THREE SECTIONS EARLIER.** `node_peer_status` was converted to
`DO UPDATE` for gateway liveness, carrying the reason verbatim:

> *"A DEMO FIXTURE FOR A LIVE SYSTEM HAS TO BE RE-RUNNABLE INTO FRESHNESS."*

**Device posture and device liveness are that same problem under different names, and never got the treatment.**
A law written down in one block does not protect the block below it.

**FIXED:** both now `DO UPDATE`. **THE STANDING CHECK:** any fixture row whose value is relative to `now()` is
`DO UPDATE`, never `DO NOTHING` — and the test is not "is it idempotent" but **"is it re-runnable INTO the state
it describes."**

---

## ⛔ THE "FIXTURE WRITES A CONTROLLER-OWNED FIELD" CLASS — two instances

> ### **SEED THE INPUTS A CONTROLLER CONSUMES, NEVER THE FIELD IT OWNS. THE RECONCILE LOOP SILENTLY UNDOES**
> ### **THE WRITE, AND THE ROW READS AS APPLIED.**

| # | field | owner | what happened |
|---|---|---|---|
| 1 | `org_hub_set.demoted` | the failover controller (`UpsertOrgHubSetDemoted`) | the fixture declared a demotion; the controller recomputes it every tick. **Seeding it also exposed a permanent wedge** (nil slice → SQL NULL → 23502 forever) |
| 2 | `devices.health_blocked` | the posture evaluator (`ReportHealth`) | the fixture set it `true`; the stale-block sweep cleared it. **`SweepStaleHealthBlocks` can only ever set it FALSE** |

**INSTANCE 2 CARRIES A HARDER CONSEQUENCE: THE INPUT IS AN HTTP REQUEST, NOT A ROW.** `SetDeviceHealthBlocked`
is called from exactly one place, `ReportHealth`, so **no SQL can produce a blocked device.** The seeder now
**registers it through the product** — logs in as the demo owner, POSTs a failing report for one fixed device id
— and counts the **server's own `blocked` verdict** into its census. Same pattern as the k3s cluster.

**REACHABILITY COST, STATED:** the seeder now needs the API up at seed time. Absence is detectable three ways —
a counted `posture_blocked: false` census field, a warning naming the consequence, and `TUNNEX_SEED_STRICT=true`
for a non-zero exit (proven to reject). `seed-fixtures` is **never run in CI** (0 references in either
workflow), so this is a local-stack concern only.

**THE SWEEP:** `fixtures.sql` was read for other controller-owned writes — see the row below.

### THE `fixtures.sql` SWEEP — every write read, three candidates, one latent instance

All 36 writes enumerated. Nine distinct `SET` columns; six are admin-owned (`dns_forwarding`, `hub_priority`,
`ovpn_enabled`, `provisioning_mode`, `revoked_at`, `site_id`) and are safe to seed. **Three are AGENT-written:**

| column | verdict |
|---|---|
| `nodes.capabilities` | ⛔ **INSTANCE 3, LATENT.** The fixture injects `ovpn_health` via `capabilities \|\| '{…}'`. The control plane REBUILDS this column server-side from the agent's typed report on every reconcile. **Measured: the injection survives ONLY because no agent reports as `gw-eu-west`** — `gw-local-1`, which a live agent does report as, carries a server-built `policy_version` and would have the injection overwritten. **One enrolment change from silent loss, with no signal.** |
| `nodes.last_seen_at` | same dependency, benign: the fixture ages `gw-ap-south` deliberately and it stays aged only because no agent reports as it |
| `nodes.endpoint` | written at enrolment, not on a loop — safe |

**NOT FIXED, REGISTERED.** The honest fix mirrors instance 2 — have the agent report `ovpn_health`, or accept
the injection with its dependency stated. **TRIGGER: the next change to which node the compose agent enrols as,
or any slice that needs a SECOND faulting OpenVPN gateway.**

---

## S14.10 DEFERRALS

| deferral | trigger | why deferred | found where | reviewed? |
|---|---|---|---|---|
| **TX/RX columns** (Devices) | **S11.1**, where throughput gets an endpoint | `rx_bytes`/`tx_bytes` are raw gauges since the last handshake — they RESET on re-handshake, so a rate or total would look like throughput and not be throughput. **The same split as Site-Link Traffic**: numbers now, rates when there is a series | S14.10 classification | founder-ruled, not built |
| **Device approval-mode toggle** | **the endpoints existing** | ⛔ `getDeviceApprovalMode` / `setDeviceApprovalMode` are **NOT IN THE SPEC** — measured. The panel was classified as five served endpoints and is THREE (`listPendingDevices`, `approveDevice`, `rejectDevice`). The org-level half is **BUILD + BACKEND, like Operations** | S14.10 scope census | ruled deferred |
| **Device Approval panel** → **S14.10b** | its own commit-one | `rejectDevice` is destructive and irreversible: `pending → revoked, assigned_ip = NULL`, freeing the tunnel address. **A mutation surface with a confirm step, not a column** — folding it into a layout pass is how a confirm dialog gets reviewed as a div. **AND it makes the Modal a11y deferral LIVE** (Escape / focus trap / initial focus / focus return — paper-only, never reviewed) | S14.10 scope question | founder-approved split |
| ~~Served `health_stale` discriminator~~ **MOOT — closed, not deferred** | — | Registered when `unknown` was believed to have THREE causes needing a server-side discriminator. **The third cause cannot occur** (see the spec-defect row), so `unknown` means only no-report or stale, and `health_reported_at` alone separates them. **Nothing is being reconstructed, so there is nothing for the server to say.** Rewritten rather than left standing: a deferral for a problem that no longer exists is a future reader's wasted slice | S14.10 item 1, closed same section | n/a |
| **S7.4c shared-DB leakage** ⚠ **UPDATED** | ⛔ **now** — it has moved an order of magnitude | Registered at `real_orgs=29`, **never revisited. MEASURED TODAY: 298.** All created 2026-08-02, all Go integration-test debris — single-letter names (`O` ×153, `S` ×51, `K` ×44), `MFA Org` ×33, and slug prefixes `wd-` (21 — my own wedge test), `gf-`, `k8s`, `mfa`, `pos`. **CAUSE: runs that PANIC skip `t.Cleanup`**, and this session produced several (the nil-map false red). **CONSEQUENCE: `seed-fixtures` refuses on `realOrgs > 0`, so the review stack now needs `TUNNEX_SEED_FORCE=true` — the guard has been turned into a formality by debris** | S14.10, seeding for review | measured, not cleaned |

---

## ⛔ THE S14.6 FIXTURE DEBT IS STRUCTURAL, NOT AN OVERSIGHT — two of its three states are CONTROLLER-OWNED

**Founder-connected, and it explains why the debt survived two sections.** The S14.6 debt names three states:

| state | field | owner |
|---|---|---|
| `ovpn_enabled: true` | `organizations.ovpn_enabled` | **admin** — seedable, and seeded |
| one OpenVPN fault kind | `nodes.capabilities → ovpn_health` | ⛔ **the agent's report.** Instance 3 of the controller-owned class — LATENT-FRAGILE, surviving only because no agent reports as `gw-eu-west` |
| a demoted hub note | `org_hub_set.demoted` | ⛔ **the failover controller.** Instance 1 |

> ### **TWO OF THREE SIT ON FIELDS A RECONCILE LOOP OWNS. THE DEBT WAS NOT FORGOTTEN — IT WAS UNSEEDABLE**
> ### **BY THE MEANS BEING USED, AND EVERY ATTEMPT LOOKED APPLIED.**

**WHAT ACTUALLY DISCHARGES IT — one of two patterns, per state:**

1. **SEED THE INPUTS THE CONTROLLER CONSUMES.** Done for the demoted note: give the members the capability the
   elector requires, leave one stale, and the controller demotes it ITSELF. Derived, not declared.
2. **REGISTER THROUGH THE PRODUCT.** Done for `posture_blocked`, which is unreachable from SQL at all — the
   seeder logs in and POSTs a real report, then counts the server's own verdict.

**`ovpn_health` HAS NEITHER YET.** Pattern 1 needs the agent to report it (no seam today); pattern 2 needs an
agent-authenticated status POST. **TRIGGER: the next change to which node the compose agent enrols as, or any
slice needing a second faulting OpenVPN gateway.**

---

## ⛔ SPEC DEFECT — `health_state: unknown` claimed a third cause that CANNOT OCCUR

`openapi.yaml` said `unknown = no report, stale report, **or the fact was reported absent**`. **The third
disjunct is impossible**, measured three ways:

```
device_health.evaluated_state   NOT NULL, CHECK (evaluated_state IN ('compliant','noncompliant'))
healthInfoFor                   reaches `unknown` only when evaluatedState is nil / reportedAt is nil / stale
the evaluator                   if f.DiskEncrypted == nil { continue }   // "absence never blocks"
```

A device that reports and cannot determine the fact has the check **SKIPPED**, evaluates **`compliant`**, and is
stored as `compliant`. It never becomes `unknown`. With a row present, `evaluatedState` is never nil.

**COST: a full build-and-revert of a third UI label**, plus five reds that were green against a state production
cannot produce. **CORRECTED IN PLACE** in `openapi.yaml`, same treatment as the `listSiteSubnets` summary and
the `policy_degraded_kind` paragraph.

**HOW IT WAS CAUGHT:** a reachability assertion on the RENDERED PAGE returned 0 matches. Unit tests passed and
the payload looked right. **A test can pin a label production can never produce; only the screen says otherwise.**

| **Derived enums in the `health_state` blind spot** | next spec defect, or EPIC 14 close | `Device.kind`, `Device.mode`, `Member.status` are PROJECTIONS with no column to compare against, so the spec-enum-vs-CHECK sweep cannot see a defect in them — the same blind spot `health_state` hid in, where the instrument said safe and the field was wrong. **The only check is the description's cause-list against the projecting function. NOT RUN** | S14.10 third-axis sweep | ⛔ unchecked |
| **`real_orgs > 0` is always overridden** | **the S7.4c leakage cleanup** | The guard now requires `TUNNEX_SEED_FORCE=true` on every review-stack seed (298 debris orgs), so **the override is habit and the guard is a prompt.** This is the "configured not to matter" class arriving at the one guard that stopped a wrong-stack write at S14.7. Two fixes: clean the debris, or teach `countRealOrgs` a slug-prefix exclusion — **proposed at S7.4c and never built** | S14.10 seeding | measured, unfixed |

| **Audit Log duplicate React keys** | **the S14.11+ Audit Log section pass** (it is a REDESIGN-bucket screen, so this is its own slice) | Seen in vitest output during S14.10 and **never chased**: `Warning: Encountered two children with the same key, '49'` from `AuditLog.tsx:17` via `DataTable` (`ui.tsx:331`). React's own words: *"Non-unique keys may cause children to be duplicated and/or omitted."* ⛔ **OMITTED IS THE WORRYING HALF — on an AUDIT LOG, a silently dropped row is a missing record of who did what.** The key is likely a sequence/index colliding across pages or a `key` derived from something non-unique. **NOT INVESTIGATED**: which field, whether rows are actually dropped, and whether keyset pagination is involved | S14.10, in test output | ⛔ unchecked |
| **Users & Roles shedding has NO DESTINATION** | **the target screens landing** (`CLI Credentials` and `Edition`, both BUILD) | `Users.tsx` was classified a "shedder": machine credentials → CLI Credentials, edition → Edition. **Both targets are BUILD-bucket and neither exists.** Shedding now removes a WORKING surface with nowhere to go — deliberately recreating the S11 finding (`gateway revoke` existed in the API and never in the UI). **RULE: the surface STAYS until its destination exists**, with this row as the trigger. Ruled in the S14.11 commit-one, never shed by default | founder-corrected, S14.11 open | ruled, not built |

| **Direct-to-`main` pointer pushes bypassing the 3 required checks — RUNNING COUNT: 7** | **EPIC 14 close** | Authorized under **Ruling 2** (a process/docs correction whose value is immediate lands on `main` directly) and **reported every single time** — but ONE PER MERGE is a standing MECHANISM, not an exception, and a mechanism that bypasses `gates` / `client (macos-latest)` / `client (windows-latest)` deserves a decision rather than a habit. **The pointer is docs-only, so the bypass buys ~8 min it would otherwise wait for; the ruling due at epic close is whether this STAYS the mechanism or the pointer MOVES somewhere that needs no bypass at all** (a release note, a generated file, or after the `gates` split makes a docs-only run ~1.7 min and the bypass nearly worthless). ⛔ **The split changes the arithmetic of this row, so rule them together** | S14.5 to S14.10, one per merge | the push that registered this row as SIX **WAS the seventh** -- the count went stale in the act of writing it, and GitHub printed the bypass verbatim (`Bypassed rule violations ... 3 of 3 required status checks are expected`). Corrected on a branch rather than with another `main` push, which would have made it eight |
