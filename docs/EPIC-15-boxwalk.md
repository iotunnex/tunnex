# EPIC 15 — the full product walk. **THE DECK.**

**Re-entry:** `main` at `b292a51c` (PLAN pointer `7454ce27`). S15.0, S15.1, S15.2 merged.
**Status: PAPER ONLY. NO LEG HAS RUN. NO CODE CHANGES.**

⛔ **THE BETA-BUNDLE CALL COMES AFTER THIS WALK — not before it, and not as part of it.** Nothing in this
deck is a readiness argument; it is a list of things that are either observed or not.

---

## 0. WHAT THIS WALK IS, AND THE LAW THAT SHAPES IT

This is a **full-surface product walk**, not an epic walk. EPIC 15 gave it five legs; the founder ruled the
pass covers the product, **including surfaces the epic never touched** — §7.

### ⛔ THE HUMAN GATE LIMIT LAW GOVERNS THIS DOCUMENT

> ## **A REVIEW IS ONLY VALID OVER WHAT THE DATA MAKES VISIBLE, AND ITS SILENCE ABOUT THE REST IS
> ## INDISTINGUISHABLE FROM APPROVAL.**

So the fixture coverage is stated **per leg, before the walk starts** (§1), and **every state that cannot
be seeded is named with a substitute and a trigger** (§8). A state that is neither observed nor declared
becomes, by the end of the walk, something everybody believes was checked.

⚠ **This deck was written from measurement, and two things in it could not be verified from source:**

| claim | status |
| --- | --- |
| `hashAllow` is `{SrcIP, DstCIDR, Protocol, PortLow, PortHigh}` — `dst_kind` never reaches the artifact | **VERIFIED** — `policyspec/hash.go:15-21` |
| `Resource` is port-scoped (`cidr` + `protocol` + `port_low/high`) | **VERIFIED** — `openapi.yaml` |
| attribution rides an **agent-stamped** `src_device_id` from the artifact's `/32`→device map; the CP joins device→user and **never** `src_ip` | **VERIFIED** — `accesslog/ingest.go:40-50` |
| the **"Talking:" stray box** | ⚠ **NOT FOUND IN SOURCE.** `grep` over `apps/web/src` and `apps/client/src` returns nothing. Carried as a **surface item the founder points at during the walk**, not as a located defect. It may be a runtime string, an Electron surface, or a memory of an older build — the walk resolves which |

---

## 1. FIXTURE COVERAGE — **STATED BEFORE ANY LEG RUNS**

| leg | what the fixture can produce | what it CANNOT | consequence |
| --- | --- | --- | --- |
| **1 — restore proof** | nothing usable | ⛔ **a credential that authenticates.** Seeded hashes are `sha256('fixture-<name>')` — they belong to **no issued token, by construction** | needs a **real mint** + a **live consumer**. If neither can be stood up → **NOT OBSERVED** |
| **2 — deliberate red** | resources, grants, an enforcing org | a real MCP server; a real agent device with a real WG key | needs a live gateway + a real peer |
| **3 — attribution** | `access_events` rows can be seeded | ⛔ **an event produced BY the data plane.** A seeded row proves the renderer, never the pipeline | the instrument must be proven alive first — §4 |
| **4 — separateness** | nothing | the agent's process/netns topology is not a database fact | pure wire observation |
| **5 — RoleAgent** | roles, grants | — | this leg is fully seedable and is the **only** one that is |

⛔ **FOUR OF FIVE LEGS CANNOT BE PROVEN FROM SEEDED DATA.** That is the honest headline of this deck, and it
is why the walk exists rather than a fixture review.

---

## 2. ⭐ LEG 1 — THE OWED PROOF. **REFUSED → ASSIGN → AUTHENTICATES.**

**Why this leg outranks the others:** S15.1 shipped a **refusal proven** and a **cure unproven**, and that
is live on `main` now. Every existing machine credential is refused until an owner is assigned.

> ## **A GUARD THAT REFUSES, AND CANNOT BE SHOWN TO UN-REFUSE, IS A ONE-WAY DOOR UNTIL SOMEONE PROVES
> ## OTHERWISE.** D14 ruled OWNED credentials, not DEAD ones.

**Setup:** a live control plane; a consumer that authenticates with a machine credential (the GitOps
operator is the natural one).

**Procedure — three states, one credential, in order:**

1. **Mint** a credential through the UI. Capture the `tnxm_` token **once**.
2. **REFUSED** — call an operator-scoped endpoint with that bearer. **Expect 401.** ⚠ Record the *response*,
   not the row: the claim is about `MachineAuth`, not about `user_id`.
3. **ASSIGN** an owner **through the screen** — Settings → GitOps operator credentials → picker → Assign.
   ⚠ Through the screen, not by SQL: the screen is what an operator has, and D19 ruled assign-explicitly.
4. **AUTHENTICATES** — repeat the same call, same token. **Expect 200.**

**PASS:** all three observed, in that order, on the wire, with the same token.
**FAIL:** step 4 does not recover.
⛔ **NOT OBSERVED:** no live consumer could be stood up. **This is not a pass and not a fail** — and the
consequence is written now so it cannot be softened later: **the enrolment refusal stays UNARMED**
(`enrolmentRefusalArmed = false`), and S15.2 slice 2 keeps its held item.

⚠ **Nothing seeded can substitute.** This was already tried: the fixtures are display-only *by design*, and
a fixture that could authenticate would be a seeded backdoor.

---

## 3. ⭐ LEG 2 — THE DELIBERATE RED. **ROUTING IS NOT PERMISSION.**

⛔ **THIS IS THE EPIC'S CENTRAL CLAIM IN ONE LEG. If it fails, everything downstream of it is wrong too.**

**The measured premise it rests on:** `hashAllow` is **five fields** — `SrcIP, DstCIDR, Protocol, PortLow,
PortHigh` — and **`dst_kind` never reaches the artifact** (`policyspec/hash.go:15-21`). So an MCP server is
expressible as a **port-scoped `resource`** today, with no new enforcement surface. **The destination half
of this epic already shipped; the principal was the epic.**

**Setup:** enforcing mode · an MCP server on a known host:port reachable from the gateway's LAN · an agent
device with a real WireGuard peer · **zero grants**.

**Express the target as a resource:** `cidr` = the MCP host `/32`, `protocol` = `tcp`,
`port_low = port_high =` the MCP port. ⚠ **Port-scoped deliberately** — a host-wide grant would prove
reachability to a *host* and say nothing about the *service*, which is the distinction an MCP deployment
actually needs.

**Procedure:**

| step | action | expected |
| --- | --- | --- |
| 1 | zero grants; from the agent device, connect to MCP host:port | ⛔ **REFUSED** — no route, or blocked |
| 2 | add a grant: agent subject → the port-scoped resource | — |
| 3 | reconnect | ✅ **REACHES IT** |
| 4 | revoke the grant | — |
| 5 | reconnect | ⛔ **DEAD** |

**PASS:** 1 refuses, 3 reaches, 5 dies.
⛔ **THE FAILURE THAT MATTERS IS STEP 1 SUCCEEDING** — that is *routing mistaken for permission*, S8.2's
Leg 1 in this epic's domain, and it would mean the product's central claim is false.
⚠ **And step 5 is not optional.** A grant that adds reachability but cannot remove it is a one-way door;
proving only 1→3 would leave revocation untested and look like a pass.

**Adjacent port control:** with the grant in place, try **a different port on the same host**. It must
still be refused — otherwise the grant is host-scoped and the port fields are decorative.

---

## 4. LEG 3 — ATTRIBUTION END TO END

**Claim:** an agent's flow produces an access event naming **the agent principal** and **the owner behind
it**.

⚠ **`access_events` WAS EMPTY ON THE RIG, SO THE INSTRUMENT IS UNPROVEN BEFORE THIS LEG BEGINS.**

> ## **A WITNESS MUST PROVE IT WAS ALIVE ACROSS THE WINDOW IT CERTIFIES.** An empty log is not evidence of
> ## no traffic; it is evidence of nothing at all, and it reads identically to a working system with a
> ## quiet network.

**Procedure — the instrument first, the subject second:**

1. **PROVE THE INSTRUMENT.** With an **ordinary human device**, generate a flow that must be logged. Confirm
   a row appears in `access_events`. ⛔ **Until this passes, no observation about agents means anything.**
2. **THE SUBJECT.** Generate a flow from the **agent** device. Confirm an event naming the agent, and the
   owner resolved behind it.
3. **THE NEGATIVE.** Confirm the event's `src_device_id` is **agent-stamped from the artifact's `/32`→device
   map** and that the CP joined device→user — never `src_ip`. ⚠ An attribution reconstructed from source IP
   would be a racy IP-map lookup wearing the right answer's clothes.

**PASS:** 1, then 2, then 3.
⛔ **FAIL:** step 1 does not produce a row → **the flow-log pipeline is dead and Leg 3 cannot run at all**;
report it as an instrument failure, not as an attribution failure.

---

## 5. LEG 4 — THE AGENT IS GENUINELY SEPARATE

⛔ **NOT A PROCESS INSIDE THE GATEWAY'S NETWORK NAMESPACE.**

> ## **LOCALLY-ORIGINATED IS NOT FORWARDED.** A packet from a process inside the gateway's netns never
> ## traverses `FORWARD`, so it is never subject to the policy chain — and it would sail through Leg 2 while
> ## proving nothing. **This fixture-fidelity trap has already cost this repo once** (the ZT-transit
> ## exoneration at S8.4).

**Procedure:** confirm the agent device is a **distinct peer** — its own WireGuard key, its own `/32` from
the pool, traffic arriving at the gateway **from the wire**. Verify with `wg show` (a distinct peer with
handshakes) and by confirming the test traffic in Leg 2 originates **off-box**.

**PASS:** the agent's packets are forwarded, not locally-originated.
⛔ **FAIL — and it invalidates Leg 2 retroactively.** If the agent is inside the netns, Leg 2's "reaches
it" proved reachability, not permission, and the leg must be re-run from a real peer.

---

## 6. LEG 5 — `RoleAgent` CANNOT WRITE POLICY

**The split that could not ship before a second principal kind existed.** `RoleOperator` holds
`PermPolicyManage` — **correct** for a GitOps operator, whose whole job is reconciling `TunnexGrant` CRs —
and **inverted** for an agent.

**Procedure — both halves, and the second is why the first means anything:**

1. As an **agent principal**, attempt a policy write. ⛔ **Expect refusal.**
2. As the **GitOps operator**, attempt the same write. ✅ **Expect success.**

**PASS:** 1 refuses **and** 2 succeeds.
⛔ **A guard that refuses everything passes step 1 and is not a guard** — and removing `PermPolicyManage`
outright would break the operator rather than narrow the agent, which is the mistake this leg exists to
catch.

⚠ This leg is **fully seedable** and is the only one that is. It is a wire confirmation of something unit
tests already pin, and it is here because a role table is not a permission until an endpoint honours it.

---

## 7. ⚠ THE FULL-SURFACE PASS — the parts EPIC 15 DID NOT TOUCH

**This is what makes it a product walk rather than an epic walk.** At minimum, the surfaces carrying known
open items:

| # | surface | what to look at | registered as |
| --- | --- | --- | --- |
| **7.1** | **the org question** | a user in **two orgs** reaches only the oldest. `GET /organizations` returns all; the UI reads `orgs[0]`; there is **no switcher anywhere**. ⛔ Confirm on the screen, then confirm the API really does return both | delivery register **row 2**; ruling **HELD**, recommendation **B** |
| **7.2** | **pool utilisation** | **253 allocatable** on the default `/24`, org-wide, shared with every human device — and **nothing surfaces utilisation**. ⚠ Resizable-and-invisible means **the first signal is the refusal**. Look for any headroom indicator; expect none | S15.0 registered, not built |
| **7.3** | **Entra stale-seal** | Directory sync shows **ESCALATED / `credential: decrypt failed`**. ⛔ The defect is **the ambiguity, not the error** — a reviewer cannot tell a seeded fault from a live one, so every genuine escalation gets discounted | delivery register **row 3** |
| **7.4** | **the "Talking:" stray box** | ⚠ **Not located in source** (§0). The founder points at it; the walk decides whether it is web, Electron, or gone | **unregistered** — this walk is where it gets a row |
| **7.5** | the S15.2 surfaces themselves | the `unattributable` badge on a gateway with no owner; an assigned agent showing its owner | S15.2 |
| **7.6** | `users.deleted_at` | ⚠ nothing to *see* — noted so the walk does not mistake its invisibility for absence: **18 pre-armed predicates, 5 data-plane**, on a column nothing writes | delivery register **row 5** |

⚠ **7.1–7.4 are OBSERVATIONS, not tests.** They have no pass/fail; they exist so the founder sees the
product's current state with the register open beside it. **Recording "seen, unchanged" is a valid result
and is not a pass.**

---

## 8. ⛔ THE UNSEEDABLE REGISTER — declared, never skipped

Every state this walk cannot produce, with its substitute and a **named trigger**.

| state | why it cannot be seeded | substitute | trigger for the real proof |
| --- | --- | --- | --- |
| **a credential that authenticates** | seeded hashes belong to no issued token **by construction** — a fixture that authenticated would be a seeded backdoor | the S15.1 reds + the wire `422`/`204` | **Leg 1 of this walk.** If it does not run, the trigger persists and the refusal stays unarmed |
| **a real MCP server** | not part of the product | none | Leg 2. ⚠ Any TCP listener on a known port substitutes for the *enforcement* claim, but **not** for "MCP works" — say which was proven |
| **an event produced by the data plane** | a seeded row proves the renderer, not the pipeline | none | Leg 3 step 1 |
| **the agent's netns topology** | not a database fact | none | Leg 4 |
| **a soft-deleted user** | ⛔ **cannot exist** — nothing writes `users.deleted_at` | none | the commit that first writes the column. **18 predicates flip meaning that day, 5 on the data plane** |
| **a `DELETE`d user** (D26's cascade) | ⛔ **no code path deletes a `users` row** | latency measured | whoever implements user deletion — they inherit a silent gateway-deletion |
| **an owner deactivated after assignment** (D23) | seedable in DB, but the *behaviour* is the open question | none | **D23, ruled AFTER this walk** |

---

## 9. EXIT CRITERIA

**The walk is complete when every leg is PASS, FAIL, or explicitly NOT OBSERVED — and §7 is recorded as
seen.** ⛔ **A leg with no recorded outcome is the Human Gate Limit Law's failure mode**: silence that reads
as approval.

**What each outcome licenses:**

- **Leg 1 PASS** → the enrolment refusal may be armed (a separate, deliberate one-line commit with its own
  red — `enrolment_refusal_test.go` must be edited, so it cannot happen by accident).
- **Leg 1 NOT OBSERVED** → refusal stays unarmed; the trigger persists.
- **Leg 2 FAIL** → ⛔ **stop.** The epic's central claim is wrong and the beta-bundle call is not a
  conversation worth having yet.
- **Leg 3 step 1 FAIL** → the flow-log pipeline is dead; that is its own defect, ranked above attribution.
- **Leg 4 FAIL** → Leg 2 is invalidated retroactively and must be re-run from a real peer.

⚠ **And the beta-bundle call comes after all of this**, with §7's observations and §8's undischarged
triggers in hand — because a readiness call made without them is a readiness call made over what the data
happened to show.

---

**Deck ends. No leg has run. The founder drives the walk.**
