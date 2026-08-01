# EPIC 13 walk — RECORD

Branch `story/S13.1-gateway-recovery` · CP sha `9f7c56f` · edition **enterprise** · schema **64**
Rig: `docs/infra-inventory.md`. Runsheet: `docs/S13-boxwalk.md`.

**TTL: `TUNNEX_AGENT_CERT_TTL=10m` for this run — this is a REHEARSAL.** It exercises the mechanics and the code
paths; it does not exercise the shipped 48-hour behaviour. Every leg below records the TTL it ran under, and a
pass at 10m SUBSTITUTES for nothing at 48h.

---

## WF-S13-1 — no surface can choose which gateway a device is homed on, and "active" is the wrong test

**Found during staging, before any leg ran.** Severity **MEDIUM** (it fails loudly on one path and silently on
another — see below). HELD for disposition; not fixed.

### What happened

Creating a device in the UI returned:

> *the node has not reported its endpoint/key yet; ensure the agent is enrolled and TUNNEX_NODE_ENDPOINT is set*

The device form has a name and a type and **no gateway picker**. `Devices.tsx` calls
`defaultDeviceNode(nodes)` → `selectableNodes(nodes)[0]` (`apps/web/src/lib/nodepick.ts`), i.e. the FIRST node
with `status === "active"` in `created_at` order. On this fleet that is **azure-gw** — active, but its agent has
been gone six days and it **never reported an endpoint at all**. The server's own guard refused, correctly.

### Why it is not just "azure-gw should have been revoked"

**`active` is the wrong predicate for "can host a device."** EPIC 11's finding S13-1 fixed `nodes[0]` →
`active[0]`, which removed *revoked* gateways from selection. This is the same shape one predicate over: a
gateway that is `active` but has never reported, or has been offline for days with an expired certificate, cannot
serve a device either.

**And the next node in line is worse.** Revoking `azure-gw` to clear position 0 promotes **aws-gw-2** — which HAS
reported an endpoint and key, so device creation would **succeed** and home the device on a gateway that has been
offline six days with an expired certificate. The failure would be silent, and the one-time config unusable. That
is exactly what `nodepick.ts`'s own doc comment warns about:

> *"homing a device on a dead gateway produces a one-time config that can never connect, and a one-time secret
> cannot be re-issued — so the failure is not merely inconvenient, it burns the artifact."*

The module reasoned its way to the right principle and then encoded a predicate that does not enforce it.

### Two consumers, one rule, neither able to choose

| surface | selection | can the operator choose? |
|---|---|---|
| `apps/web` `Devices.tsx` | `selectableNodes(nodes)[0]` | **no** — no picker in the form |
| `apps/cli` `internal/cli/device.go:44-48` | first `n.Status == "active"` | **no** — `device create` has `--name` and `--full-tunnel` only |

This is the four-surface census's *"surfaces that CHOOSE a gateway"* row, counted at two, with the same defect in
both. On a single-gateway deployment neither is visible; on a four-gateway fleet the target is a guess.

### Suggested direction (NOT ruled)

1. Narrow selection to gateways that can actually serve: reported endpoint AND public key, unexpired certificate,
   and a recent `last_seen_at`.
2. Give both surfaces an explicit target — a picker in the web form, `--gateway` on the CLI — because on a
   multi-gateway fleet a default is a guess whatever the predicate.
3. When nothing is selectable, say which condition failed. "No gateway available" and "your gateways are all
   offline" send an operator to different places.

### Consequence for this walk — DECLARED STAGING

No product path can home a device on `aws-gw-1`, so the walk's devices are created through the API with an
explicit `node_id`. **That is a workaround for WF-S13-1, not a walk procedure**, and it is recorded here rather
than performed quietly: the zero-touch bar applies to recovery, and this is device staging, but a reader must be
able to tell which commands were the product working and which were us going around it.

---

## WF-S13-3 — Batch C's #8 fix HALF-LANDED, and its red passed because the FIXTURE simulated the missing half

**Found on the wire, at Leg 3a's cascade.** Severity **HIGH** — the approval bypass #8 was raised to fix was still
live. **Self-inflicted, in the fold that was supposed to close it.**

### What the wire showed

Revoking `aws-gw-1` cascaded its three devices correctly — `revoked_cause = 'cascade'` ✅, `assigned_ip`
preserved ✅ — and left **`revoked_prev_status` EMPTY on all three**.

### Why

The production sweep never received the column:

```sql
UPDATE devices
SET status = 'revoked', revoked_at = now(), revoked_cause = 'cascade'     -- no revoked_prev_status
WHERE node_id = $1 AND status IN ('active', 'pending') AND deleted_at IS NULL;
```

The Batch C edit used a bare `s.replace()` whose anchor read `IN ('active','pending')` while the file reads
`IN ('active', 'pending')` — **one space**. It matched nothing, changed nothing, and reported success.

### Why no test caught it — this is the important half

The red for #8 (`TestRestoreDoesNotPromoteANEVERAPPROVEDDevice`) **passed**, because the same fold "corrected" the
test fixture `revokeGatewayCascade` to set `revoked_prev_status = status` by hand. **The fixture simulated a
production change that did not exist, and the red asserted against the simulation.**

That is the fixture-fidelity law running in REVERSE. Its known form is a fixture that records LESS than
production, so a red fails for the wrong reason. This is a fixture that records MORE — so a red PASSES for the
wrong reason, which is strictly more dangerous because nothing draws attention to it.

It is also the fourth instance this session of *a patch that did not apply and reported success* — the same class
as the three mutation false-proofs that motivated `scripts/mutate.sh` and its anchor assertion. **The script was
written and then not used on this edit.**

### Fixed

1. The sweep now records `revoked_prev_status = status`, applied with an `assert old in s`.
2. **The fixture no longer restates the production query — it CALLS it** (`f.svc.q.RevokeDevicesForNode`). A
   fixture that restates production tests the restatement; calling the real query makes divergence impossible by
   construction.
3. Mutation-proven: removing the column from the sweep now FAILS the red (`a device that WAS active must come
   back active; got "pending"`). Under the old fixture that same mutation passed.

### Consequence for this run

The three devices already cascaded carry `revoked_prev_status = NULL`, so Leg 4's restore will take the
**unknown-prior-status** branch. **#8's recorded-prior-status path is therefore UNPROVEN on this rehearsal** and
is owed by the 48-hour run. The unknown branch can still be exercised here by turning the org's approval gate on
before the restore — the fail-safe direction (`NULL + approval on → pending`).

---

# LEG RESULTS — REHEARSAL RUN (10-minute TTL), 2026-07-31

CP `c417c85` (rebuilt mid-walk after WF-S13-3) · enterprise · schema 64 · agent image `dd4443ed4df0` on both hosts.

| leg | verdict | the evidence that decided it |
|---|---|---|
| **0** provenance + surfaces | **PASS** | generated fingerprint column readable in `\d nodes`; `cert_delivered ... not null | true` **visible in the schema dump**; challenge returns **200 for a serial nobody has** (anti-enumeration) and **200 for an unknown fingerprint** (both identifiers); both-identifiers and neither-identifier return **403, identical, never 400** — finding #18's fix |
| **1** PoP self-recovery | **PASS** | `agent_rekeyed identified_by="cert_serial 50d033b1…"`; **node id unchanged**; serial and fingerprint both moved; audited succession with **both key fingerprints** and `authorized_by`; **zero commands beyond `docker start`** |
| **2a** automatic handover | **PASS** | 3 refusals → `agent_rekey_exhausted` → `agent_falling_back_to_join_token`, **all within the same second, no operator action** (#5). `identities_tried` went **1 → 2**: attempt 1 wrote the pending key, attempt 2 read it back and tried the fingerprint identity (**#6 on the wire**). `pending_key_fingerprint` identical across all attempts — the key is REUSED, which is the convergence property |
| **2b** token fallback completes | **PASS** | new node id `019fb892…`, **`key_recorded = t`**, endpoint populated. Old row revoked → **the name was freed** (WF-S11-8a). Bonus: `agent_renew_scheduled_from_cert cert_expires_in=9m0s first_attempt_in=4m0s` — pass-3 claims 5/12 demonstrating themselves |
| **3a** revoked → refused | **PASS** | agent refusal **textually identical to Leg 2's**, which was refused for a different reason. **Row unchanged** — same serial, same fingerprint, still revoked. CP log names the real cause where only an operator sees it. **Every refusal `403 / 178 bytes`, for two different internal causes**, while challenges are uniformly `200 / 57 bytes` — D8/D9 MEASURED, not asserted |
| **4** operator restore | **PASS (mechanism)** + **WF-S13-4** | restored 3, re-homed to B′, audited with a human actor and `previous_node_id`. Address arithmetic defective — see WF-S13-4 |
| **5** deliberate stays dead | **PASS** | `deliberate` untouched: `revoked`/`deliberate`, still on the old gateway, absent from every restore audit row |
| **6** staleness surface | **PASS — both halves** | `static-keeps` shows **config out of date with its address UNCHANGED** — only the gateway comparison can fire that, so **F3's fix is proven in isolation**. `decoy-1`, nothing changed, shows **nothing** — the specificity half, which had never had wire evidence |
| 3b · 7 · 8 | **NOT RUN** | local legs; the refusal-surface half of 3b was covered from the CP |

## WF-S13-4 — the restore consumed one candidate's address to re-address another

**MEDIUM.** Observed at Leg 4.

| device | before | after | correct? |
|---|---|---|---|
| `keeps` | .2 | .3 | fresh was right — `.2` was genuinely held by `decoy-1` |
| `contended` | .3 | **.4** | **wrong — `.3` was free until the restore itself took it** |
| `static-keeps` | .5 | .5 | reclaimed ✓ |

`keeps` could not reclaim `.2`, so it allocated fresh and was handed **`.3` — `contended`'s own remembered
address**. `contended` then found its address taken *by the same restore* and was re-addressed to `.4`.

`RestoreCascadeRevokedDevices` seeds `used` from `ListActiveDeviceAllocations` — **live** allocations only. The
other candidates' remembered addresses are not reserved, so a fresh allocation inside the loop can consume one.

**Cost:** a user re-imports a config who did not need to. Wall 6's failure mode — one rebuild becoming a
fleet-wide user event — reduced but not eliminated. **Direction (not ruled):** seed `used` with every candidate's
`assigned_ip` before the loop, releasing each as it is assigned.

## WF-S13-1 — third surface

The restore's target picker offered **`azure-gw`**: active, expired, no endpoint, cannot serve a device. Same
predicate defect as the device-create picker and the CLI. Three surfaces now.

## WF-S13-5 (LOW) — result banner grammar

*"Restored 3 devices. 2 could not reclaim **its** original address"* — plural/singular mismatch in Slice 7's own
affordance.

## WHAT THIS REHEARSAL DOES **NOT** PROVE — owed by the 48-hour run

1. **The 48-hour behaviour itself.** Every leg above ran at `TUNNEX_AGENT_CERT_TTL=10m`. The mechanics are
   proven; the shipped lifetime is not.
2. **Leg 1's site-binding claim.** `aws-gw-1` has no site binding, so *"`site_id` survives recovery"* was
   trivially true and therefore untested. **The 48h run must use a site-bound gateway for Leg 1.**
3. **`cert_delivered` false→true.** The window is seconds — the agent authenticates immediately after promotion —
   and the sample landed after the flip. Leg 7 (local) can catch it, because there the timing is controllable.
4. **#8's recorded-prior-status path** (WF-S13-3): the cascaded rows carry NULL, so the restore took the
   unknown-prior branch and returned everything to `active`.
5. **F3's known residual.** No device had *managed + gateway changed + address unchanged*, so the case the fix
   deliberately does not cover was never isolated. `contended` had both changed and fired on the address cause.
6. **Legs 3b, 7, 8** (local): the identifier-refusal matrix, lost-response recovery in-process, and the
   save-failure retry.

---

# DISPOSITIONS (2026-07-31)

## WF-S13-2 — **WITHDRAWN**, not re-ranked

I reported that the emitted enrol command omits `TUNNEX_NODE_ENDPOINT`. **It does not.**
`remoteEnrollCommand` (`apps/web/src/components/Gateways.tsx:42-51`) emits it whenever the operator supplies one,
and omits it only when they deliberately leave it blank — which S8.2c established as **blank = NAT'd spoke**, a
gateway behind NAT with no reachable endpoint by definition. The form already says so
(`Gateways.tsx:268` *"Public endpoint (optional — ip:port peers dial)"*, `:436` *"No public endpoint set → this
gateway is treated as a NAT'd spoke"*).

`azure-gw`'s blank endpoint is therefore a correctly-recorded unreachable gateway, not a defect.

**The real defect was already WF-S13-1**, and this evidence sharpens it: the product knows a gateway is
unreachable, says so at mint time, and then **still offers it as a device target**. The knowledge exists; the
picker does not consult it.

*Recorded as a withdrawal rather than deleted: a finding that was acted on and turned out wrong is part of the
record.*

## WF-S13-1 — REGISTERED, both halves, with a trigger

### LIVE EVIDENCE, not hypothetical

**`azure-gw` is `status='active'`, has been dead since 2026-07-25, has never reported an endpoint — and is a
SELECTABLE DEVICE TARGET.** It is the first `active` row by `created_at`, so `selectableNodes(nodes)[0]` picks it,
and the walk hit exactly that: device creation refused with *"the node has not reported its endpoint/key yet"*.

It is also a **WF-S11-10c sighting**: that host runs its agent inside k3s (serving the `k8s` row), so the
Gateways list shows **two gateways for one host**, one of them a six-day-old corpse. The product knows the row is
unreachable — it renders "certificate expired" against it — and still offers it as a place to put a device.

The predicate is not merely imprecise; there is a row on a live fleet, today, that it gets wrong.

Three surfaces choose a gateway by `selectableNodes(nodes)[0]` / first-`active`, and none lets an operator
choose: `apps/web` device form · `apps/cli device create` · the restore target picker.

**Not folded, and the reason is that the obvious fix is the risky half.** Narrowing the predicate to "can serve"
(reported endpoint + key, unexpired certificate, recent `last_seen_at`) can make the selectable set **empty** on
a fleet whose gateways are all stale — turning a confusing default into a hard block, with no affordance to
explain which condition failed. That needs the explicit-target UI and the diagnostic message in the same change,
which is a slice, not a fold.

**TRIGGER: the next change to device creation, or the first support report of a device homed on a dead gateway.**

## WF-S13-4 — FOLD (next session, with a red)

The restore consumes one candidate's remembered address to re-address another. Small, contained, and it costs a
user a re-import they did not need. `used` gets seeded with every candidate's `assigned_ip` before the loop,
released as each is assigned. Red: two cascaded devices whose addresses would collide under the current
allocator, asserting **both** reclaim.

## WF-S13-5 — FOLD (with WF-S13-4)

Plural/singular in Slice 7's own banner. Trivial, and it is the sentence an operator reads after a restore.

---

## PRECEDENCE LEG — CHECKED, NOT OWED (2026-07-31)

Asked: was `Recover ranked above UseToken` trivially satisfied in Leg 1, the way the `site_id` claim was?

**No — it was genuinely exercised.** Cited:

| step | evidence |
|---|---|
| a token WAS in the environment | aws-gw-1's `docker run` included `-e TUNNEX_JOIN_TOKEN=g4Q11tOrIvjQ…`, run verbatim |
| `haveToken` is presence, not validity | `cmd/agent/main.go:45,64` — `Decide(certPEM, err, nodeName, joinToken != "", …)` |
| expired ⇒ `Recover` regardless of the token | `internal/identity/decide.go:117-118` — the expired branch returns `Action: Recover` and merely RECORDS `HaveToken` |
| and it did | Leg 1 logged `agent_rekeyed`, not `agent_enrolling` |

Legs 2a and 3a re-exercised it twice more: token present, re-key attempted FIRST, fallback only after three
refusals.

The token was **spent**, which changes nothing about the ruling — `Decide` never sees validity. Spentness would
have altered the outcome of *taking* the token path, not the *ranking* that avoided it.

**What remains unexercised is the k3s/Helm ENVIRONMENT**, not the decision: `Decide` takes no network argument and
reads only the stored certificate and `joinToken != ""`, both identical in a pod. **Recommendation: do not spend
the `k8s` control node re-testing a branch already proven three times.** If the Helm path needs covering
specifically, it belongs to the S10.3 in-cluster walk.

**RULED (2026-07-31): the leg is NOT OWED.** Recorded so the absence is legible as a DECISION rather than an
omission — the check was run, the citation chain above is the evidence, and three wire exercises were judged
sufficient. A later reader finding no precedence leg in the sheet should land here rather than infer it was
forgotten.

### Follow-up read — does the in-cluster agent PERSIST its credentials? **YES**

The skip argument was that `Decide` reads the stored certificate and token presence, both identical in a pod.
That is true of the **DECISION**; the **INPUT** depends on the volume. Had the chart mounted the state dir on an
`emptyDir`, the certificate would die with the pod, `Decide` would see none, and the in-cluster agent would take
the TOKEN path on every restart — recovery-by-proof structurally unavailable in Kubernetes.

**It does not.** Cited:

| | |
|---|---|
| `deploy/helm/tunnex-gateway/values.yaml:74-75` | `persistence: enabled: **true**` — the default |
| `templates/deployment.yaml:141-144` | `- name: state` → **`persistentVolumeClaim`** |
| `templates/pvc.yaml` | a real PVC, `ReadWriteOnce`, 128Mi |
| `templates/deployment.yaml:129-131` + `:118` | mounted at `/var/lib/tunnex-node` = `TUNNEX_NODE_STATE_DIR` |

`cert.pem`, `key.pem`, `ca.pem` and `rekey-pending-key.pem` all survive a pod restart. The chart already reasons
about it (`deployment.yaml:120-122`): *"once the node cert is on the state PVC, the agent re-attaches its identity
without it."*

**The `emptyDir` branch is opt-OUT and carries its cost beside the switch** (`values.yaml:78-79`): *"For ephemeral
clusters you can disable persistence (emptyDir); a restart then re-enrolls (needs a fresh join token) —
acceptable only for testing."*

**No limitations-table row, no chart fix registered.** Disabling persistence is a documented configuration choice
with its consequence stated — a different shape from the pre-0057 nodes, which cannot recover regardless of
anyone's choice.

**Labelled honestly: this is a CODE READ, not an observation.** The walk never ran with persistence disabled. The
volume type is unambiguous, but the claim is read from the chart — the same distinction as Leg 1's site-binding
gap, recorded rather than blurred.

---

# §B staging — WF-S13-6 OBSERVED, and the manual restart it forced

**This entry is evidence, not housekeeping.** The restart below is the operator action EPIC 13 exists to remove,
performed by hand on the walk meant to prove the epic.

| event | time (UTC) | source |
|---|---|---|
| B′'s certificate expires | **14:41:45** | `nodes.cert_not_after`, prior value |
| agent keeps running, logging `tls: expired certificate` against report / status / desired-state / watch | 14:41:45 → 15:41:10 | `docker logs`, continuous, **zero `agent_rekey_*` lines** |
| **manual `docker restart tunnex-node`** | **15:41:10** | `date -u` on aws-gw-2 |
| `agent_rekeyed` — *"recovered by proof of possession — same node, same identity, new key"* | **15:41:11.774** | agent log |
| CP confirms: same id `019fb892…`, `status=active`, new `cert_not_after` `15:51:11`, `cert_delivered=t` | 15:42 | psql |

**STUCK FOR 59 MINUTES 25 SECONDS. RECOVERED IN 1.77 SECONDS.**

That ratio is the finding. The recovery path is not slow, not fragile and not conditional — it is **correct and
instant**, and it is **unreachable** without a human typing `docker restart`. `identified_by` shows
`cert_serial`, so even the identification worked first try.

**The gateway was recoverable the entire hour.** Nothing was wrong with the credential material, the CP, the
network, or the code that recovers. The only missing thing was a second invocation of a decision the agent
already knows how to make.

## What this discharges and what it does NOT

- **DISCHARGES:** boot-path recovery by proof of possession, in place, on a real expired gateway — same node id,
  same identity, new key. That half of the epic works.
- **DOES NOT DISCHARGE:** runtime expiry. Every recovery on this walk, in §A and §B alike, is a
  stop-then-start. **§C's C-LEG-0 is the only leg that proves the runtime case**, and it does not run until the
  remedy lands.

## Post-recovery state

`agent_renew_scheduled_from_cert`: `cert_expires_in=9m0s`, `first_attempt_in=4m0s`. The renew loop is anchored to
remaining life and will keep B′ alive at the 10-minute TTL — so B′ stays a valid staging subject and will not
expire again unless deliberately stopped.

---

# §B step 1 — WF-S13-7: THE UI'S ENROL COMMAND INSTALLS A PRE-S13.1 AGENT

**Found 2026-08-01 03:06 UTC, re-enrolling aws-gw-1. Not a code defect — a RELEASE-COUPLING one.**

## What happened

The enrol command emitted by the UI pins a published digest:

```
ghcr.io/iotunnex/tunnex-node-agent@sha256:de8c9cefb614981c26b157ad1c76d2768794157df7d8f6fe93e49c1c0e22f114
```

That image **predates S13.1**. Booted against a state volume holding a certificate expired 12.5 hours earlier
(`CN=aws-gw-1`, serial `9B3DB4F7…`, `notAfter Jul 31 14:40:07`, plus a `rekey-pending-key.pem` from 14:41), it
logged `agent_reusing_stored_identity` — the `UseStored` branch — and then looped
`remote error: tls: expired certificate` indefinitely. **Zero `agent_rekey_*` lines. The join token was never
spent. No new node row was created.**

That is **WF-S11-11's original symptom, verbatim**: prefer the stored identity, ignore the token the operator just
supplied, loop forever on the one error that certificate can produce.

## Why it looked like a new defect and is not

The SAME volume, four hours earlier, took `Recover` and ran the full refusal chain. The variable was the image:

| host | image | outcome |
|---|---|---|
| aws-gw-2 | `tunnex-node-agent:9f7c56f` (locally built, `sha256:dd4443ed…`) | **recovered by proof of possession** |
| aws-gw-1 | `ghcr.io/…@sha256:de8c9ce…` (published) | `UseStored`, looped forever |

**§A's walk ran the LOCALLY BUILT image on every gateway.** The published one has never carried this epic's code.

## The finding that outlives this walk

**When S13.1 merges, the UI's emitted install command must be re-pinned to an image containing it.** Until then,
every gateway installed by the documented zero-touch procedure gets an agent that **cannot recover from
certificate expiry** — and its failure mode is silent-looking: liveness up, readiness false, one warning at boot,
then an unbounded stream of transport errors that never mentions re-key.

The digest pin is correct by design (S8.2c zero-touch reproducibility). **What is missing is the coupling between
"this epic shipped" and "the thing operators install contains it."** A merge that does not move the pin ships the
feature and not the fix.

**Registered as a MERGE PRECONDITION, not a trigger:** re-pin the UI's emitted digest, and verify by enrolling a
gateway from the UI command alone and observing recovery.

## Walk consequence

**§B step 1 is REDONE with `tunnex-node-agent:9f7c56f`** — the image §A used — preserving the same-binary
provenance §B depends on. The ghcr-based container is discarded. The state volume is untouched, so the redo
starts from exactly the state step 1 intended.
