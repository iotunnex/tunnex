# EPIC 15 walk — LEG 1 ✅ PASS · LEG 4 ⛔ NOT OBSERVED

**Run 2026-08-04 against `main` `7454ce27` (content `b292a51c`), enterprise stack rebuilt from merged main,
schema 67.**

---

## ORDERING DECISION — **LEG 4 BEFORE LEG 2. NOT PROVISIONAL.**

A provisional Leg 2 verdict is the weaker option: it sits in the record as *"Leg 2 passed"* with the
dependency invisible, and the deck already states that a Leg 4 failure **invalidates Leg 2 retroactively**.

> ## **A PASS THAT DEPENDS ON AN UNVERIFIED PRECONDITION IS NOT A PASS, AND THE DEPENDENCY IS INVISIBLE IN
> ## THE RESULT.** Ordering costs one leg's sequence. The alternative costs the meaning of the result.

Leg 1 is independent of both and ran first, as directed.

---

## ⭐ LEG 1 — **PASS.** Three states, one credential, on the wire.

**The leg that decides whether the enrolment refusal ever arms.**

| state | action | observed |
| --- | --- | --- |
| **1 — REFUSED** | mint `walk-leg1`; call `GET /api/v1/organizations` with the `tnxm_` bearer | ⛔ **HTTP 401** |
| **2 — ASSIGN** | `PUT .../machine-credentials/{id}` — the picker's own endpoint | **HTTP 204**; owner resolves to `owner@demo.tunnex.local` |
| **3 — AUTHENTICATES** | **same token, same call** | ✅ **HTTP 200** |

⛔ **THE REFUSAL WAS PROVEN AT USE, NOT INFERRED FROM THE SCREEN.** A `422` on the assign path and a refusal
at use are different code paths — `MachineAuth` versus the handler's pre-check — and that distinction is the
entire reason this leg was owed. State 1 is a bearer call, nothing else.

### The 200 is non-vacuous, and the flip is ownership

An empty `[]` body could in principle be an unauthenticated path, so the authenticated state was re-checked
on org-scoped endpoints that return real data:

| endpoint | after assignment |
| --- | --- |
| `organizations/{id}` | **200**, real org row |
| `organizations/{id}/members` | **200**, real roster |
| `organizations/{id}/resources` | **200**, real resources |

**And the control — a SECOND credential, minted and left UNOWNED, on those same three endpoints: 401, 401,
401.** ⚠ Needed because there is no un-assign: without it, "state 3 differs from state 1" could have been the
endpoint rather than the ownership. It is the ownership.

### ⛔ CONSEQUENCE

**The D14 restore proof (`S15.0 §15`) is DISCHARGED.** The cure has now been watched working.
**This licenses arming the enrolment refusal** — a separate, deliberate one-line commit that must edit
`enrolment_refusal_test.go`, so it cannot happen by accident. ⚠ **Not done here; no code changed.**

### ⚠ Collected, not chased — two findings from inside a passing leg

1. **`GET /api/v1/organizations` returns `[]` for a machine principal.** The handler resolves via
   `ListOrganizationsForUser(p.UserID)`, and a machine principal has `UserID == uuid.Nil` **by design**
   (D4's separation). So an operator **cannot enumerate its own org** through the endpoint whose whole
   purpose is enumerating orgs — while org-scoped reads work fine. Not a Leg 1 failure; a real gap.
2. **The walk minted two credentials that now exist on the rig** (`walk-leg1`, `walk-leg1-control`), one
   owned and one deliberately unowned. Left in place as walk evidence.

---

## ⛔ LEG 4 — **NOT OBSERVED.** The subject does not exist on this rig.

**Leg 4 asks whether an agent's traffic is FORWARDED rather than locally-originated** — an agent inside the
gateway's netns would sail through Leg 2 proving nothing.

**Measured, and the reason is structural rather than incidental:**

| check | result |
| --- | --- |
| nodes enrolled on the rig | **5**, all named `gw`, all `active` |
| of those, **attributable** (owner set) | ⛔ **zero** — every one predates migration `0066` |
| agent-kind device rows belonging to a real node | ⛔ **zero** |

**An owned agent is the precondition for an agent `/32`.** `allocateAgentDevice` runs only when the redeemed
token carries an issuer, and every node here enrolled before the issuer column existed. **So there is no
agent peer to observe separateness for.**

⚠ **Both containers are separate netns** (`tunnex-node-agent-1` and `tunnex-api-1`, distinct PIDs on the
`tunnex_default` bridge) — **but that answers the wrong question.** On this rig the node-agent **is** the
gateway; Leg 4 is about a peer connecting **to** it from off-box. Reporting the container separation as a
pass would be the fixture-fidelity trap in its purest form.

**To run Leg 4:** enrol a **fresh** agent post-`0066` with a join token that carries an issuer, then confirm
its `/32` peer handshakes from off-box (`wg show`). ⛔ **Leg 2 does not run until this lands** — per the
ordering decision above.

---

## ⚠ CORRECTION — the "Talking:" item is NONE of its three claims

The item was carried as three observations under one name: *registered on Gateways* · *seen on Settings* ·
*absent from source*. Resolved by asking which claim is true rather than by hunting:

| where | occurrences of `Talking` |
| --- | --- |
| `apps/web/src`, `apps/client/src`, `packages` | **0** |
| the **SERVED** bundle (`index-BnNTvxeh.js`) | **0** |
| the entire repo (excluding `node_modules`, `.git`, `dist`) | **0** |

⛔ **It is not in the product.** Not in source, not in the shipped bundle, nowhere in the tree. So it is
either a string removed by an earlier change, or **it was never ours** — a browser, extension, or other
application's UI seen over the top of the screen.

⚠ **The register row must be corrected either way** — carrying "registered on Gateways" implies a location
that does not exist, and the next person will go looking for it. **The honest row is: sighted, unlocatable in
the current build, cause undetermined.**

---

## ⚠ COLLECTED — test residue is indistinguishable from product state

The rig showed **10 `kind='agent'` device rows**, which momentarily read as product state. They are
**residue from `TestAgentDeviceRowsAreCapExempt`** — orgs named `a-<hex>`, devices `agent-<hex>`, no
assigned IP, all created today. The integration harness writes to the **shared dev database** and does not
clean up.

> ## **A WALK MEASURES THE RIG, AND THE RIG CONTAINS EVERY TEST THAT HAS EVER RUN AGAINST IT.** This is the
> ## same class as the Entra stale-seal ambiguity: **the review environment lying to the reviewer**, where a
> ## seeded or leftover fault is indistinguishable from a real one.

⚠ It nearly produced a wrong reading of Leg 4 — *"10 agent devices exist, so agents are getting `/32`s"* —
which is false. Collected, not chased.
