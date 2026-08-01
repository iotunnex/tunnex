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

## §B step 1 — COMPLETE. A1′ = `019fbb50-47c3-7581-a35a-d2825c95a605`

Run on the CORRECT image (`tunnex-node-agent:9f7c56f`) after WF-S13-7. Two results §A could not produce:

**WF-S11-8a PROVEN ON THE WIRE — first time.** The node enrolled as `aws-gw-1` while **two revoked rows already
held that name**, with no `409 node_exists`. §A's only name collision was against an ACTIVE row (correctly
refused), so the merged S11 partial unique index — `nodes_org_id_name_active_key … WHERE revoked_at IS NULL` —
had never been exercised by a walk. It works.

**Finding #5 re-proven on a second gateway, and the ordering held.** `identities_tried: 2` from attempt ONE (the
pending key was already on disk, so finding #6's per-pass rebuild had both identities available immediately) →
three refusals → `agent_rekey_exhausted` → `agent_falling_back_to_join_token` → `agent_enrolled`. **No operator
action between boot and recovery.**

The refusals were CORRECT: `019fb18b` is revoked, and D3 forbids re-keying a revoked node. Expiry authorizes;
revocation does not. The agent cannot know which locally — hence the uniform refusal and the honest
`most_likely_cause` naming both possibilities.

The fallback warning states its own cost without being asked: *"This creates a NEW node: its site binding must be
re-applied and devices homed on the old node need re-issuing."* That is the whole argument for ranking re-key
above the token, printed at the moment it matters.

| field | value |
|---|---|
| A1′ node id | `019fbb50-47c3-7581-a35a-d2825c95a605` |
| status | active, `key_recorded=t`, `cert_delivered=t` |
| site_id | NULL (required for B4 — a hub-set member would self-heal the device and fake the pass) |

## §B — B4 precondition, B1 SATISFIED, and WF-S13-6's THIRD instance (unattended)

**B4 precondition asserted and recorded:** `org_hub_set` contains A1′ (`019fbb50…`) in neither `configured` nor
`demoted` — **zero rows**. A hub-set member would re-point managed devices through the dial channel, so B4's
device would self-heal and read "no badge", the PASS string, for the wrong reason.

**B′ IS BOUND TO `azure-site`** (`019f8e4b…`), not aws-site. Deliberate to record: B1's claim is that `site_id`
SURVIVES recovery, so any non-NULL binding tests it. The site's identity is not the subject.

### WF-S13-6, THIRD WIRE INSTANCE — and the first that nobody staged

| event | time (UTC) |
|---|---|
| B′ recovered by hand (instance 2) | Jul 31 15:41:11 |
| renewed ONCE, to | Jul 31 15:56:11 |
| **expired, and stayed expired** | Jul 31 15:56:11 |
| manual `docker restart` | **Aug 1 03:17:22** |
| `agent_rekeyed` — same node, same identity | **Aug 1 03:17:24** |
| **STUCK** | **11h 21m** |
| **recovery once reachable** | **~1.7s** |

**Nobody arranged this one.** Instances 1 and 2 were found while staging; this happened overnight on an idle rig,
which is precisely how it will happen to an operator.

**It also confirms the failure needs only ONE missed renewal.** B′ renewed once at 15:45-ish and then stopped.
`renewLoop` retries on its own interval, and any retry landing after `NotAfter` hits an endpoint that requires
the certificate that just expired. One miss locks the agent out, and boot-only recovery means nothing reopens it.
The agent is running `9f7c56f`, which predates `fa35e63`'s `identityWatchLoop` — so this is the LAST fleet state
in which this can happen, and it happened.

### B1 — SATISFIED. A site-bound gateway recovers IN PLACE.

| field | before | after |
|---|---|---|
| node id | `019fb892…` | **`019fb892…` (same)** |
| `site_id` | `019f8e4b…` | **`019f8e4b…` (UNCHANGED)** |
| `cert_not_after` | Jul 31 15:56:11 (expired) | Aug 1 03:27:24 |
| `cert_delivered` | t | t |

**§A could not test this.** Its Leg 1 asserted "`site_id` unchanged across recovery" against a node whose
`site_id` was NULL — trivially true, therefore untested, and it is one of the epic's headline claims. **B1 is the
first time the claim was made against a node that had something to lose.**

## WF-S13-1 — WIRE INSTANCE, and it BLOCKED DEVICE CREATION (2026-08-01 03:2x)

**Registered on a code read in §A with the trigger *"the first report of a device homed on a dead gateway."*
This is that report, and it is worse than the trigger anticipated: the device could not be created at all.**

Creating `b3-pending` from the UI failed with:

> *the node has not reported its endpoint/key yet; ensure the agent is enrolled and TUNNEX_NODE_ENDPOINT is set*

**The message blames the operator's agent configuration. The cause is a stale row the UI still offers.**

| id | name | status | endpoint | key_recorded | last_seen |
|---|---|---|---|---|---|
| `019f8e46…` | **azure-gw** | **active** | *(empty)* | **f** | **2026-07-25** |
| `019fa205…` | k8s | active | 52.190.140.51:51820 | **f** | now |
| `019fb892…` | aws-gw-2 | active | 15.135.130.96:51820 | t | now |
| `019fbb50…` | aws-gw-1 | active | 15.134.60.253:51820 | t | now |

`azure-gw` is a leftover from a VM agent that no longer exists — the `k8s` row serves that host. It has been dead
six days, has never reported an endpoint, and **because `status='active'` it remains a selectable device target.**
That is WF-S13-1's sentence verbatim: **`active` is the wrong test.** Liveness and usability are different
questions, and device placement asks only the first.

### WALK SCAFFOLDING — a hand-written status change, recorded as such

```sql
UPDATE nodes SET status='revoked', revoked_at=now() WHERE id='019f8e46-…' AND status='active';   -- UPDATE 1
```

**This is NOT a product action and must not be read as one.** `nodes` has no `revoked_cause` column (that is on
`devices`), so this is a bare status write. `devices_on_stale_row = 0` was confirmed FIRST, so nothing cascaded
and nothing B3 later measures was disturbed.

## §C's NAMED SUBJECT CANNOT SATISFY ITS OWN ACCEPTANCE LEG — found before the clock, not during

**`k8s` has `key_recorded = f`.** It is live and reporting, but the control plane holds NO public key for it — so
it **cannot recover by proof of possession at all**. Re-key would refuse it and the agent would correctly print
*"enrolled before the control plane recorded agent public keys."*

**§C names `k8s` as its subject.** C-LEG-0 would therefore fail for a reason unrelated to `identityWatchLoop`,
after burning 48 hours. Only `aws-gw-2` and `aws-gw-1` have recorded keys, and both are §B's subjects — which is
precisely why `k8s` was chosen.

**HELD FOR RULING, two options that differ materially:**

1. **Re-enrol `k8s`** so it gains a recorded key — creates a NEW node row, orphans the `k8s-site` binding, needs
   the Helm image swap already planned. Cleanest subject, most setup.
2. **Run §C on `aws-gw-1` after §B completes** — it has a recorded key, nothing else needs it once §B is done, and
   it is the box whose real expiry opened EPIC 13. Needs its agent image rebuilt to carry `fa35e63`+.

**Recommendation: option 2** — fewer moving parts, and the subject is the original incident. It changes §C's
written staging, so it is a ruling, not a fold.


## SCAFFOLDING DEPENDENCY CHECK — no remaining leg reads the hand-revoked row

The `azure-gw` row (`019f8e46…`) was revoked by **hand-written SQL** — a state no product path produces (there is
no `revoked_cause` on `nodes`; the API's gateway revoke goes through a different path entirely). **Every
remaining leg was checked against it:**

| leg | reads `019f8e46…`? |
|---|---|
| B1 site binding survives recovery | no — reads B′'s own `site_id` |
| B2 `cert_delivered` flip | no — polls B′'s row |
| B3 recorded prior status | no — cascade/restore between B′ and A1′ |
| B4 F3 residual | no — one managed device on B′ |
| B5 Legs 7/8 | no — local, no fleet state |
| B6 refusal timing | no — B′'s serial, a wrong key, an unknown serial |
| §C / C-LEG-0 / B7 | no — `aws-gw-1` only |

**Nothing depends on a state the product cannot create.** One second-order effect noted and dismissed:
`azure-site` now has one live gateway instead of two, which changes site-link health rendering — no leg asserts
on it.


## B′'s FOUR PEERS — IDENTIFIED, benign, and they change the staging

Cross-referenced by public key. All four are §A's devices, still `active` on B′:

| peer key | device | address |
|---|---|---|
| `l+JwAP…` | `keeps` | 10.99.0.3 |
| `BywML/…` | `contended` | 10.99.0.4 |
| `su52Bj…` | `static-keeps` | 10.99.0.5 |
| `7YINAv…` | `decoy-1` | 10.99.0.2 |

**`latest-handshake 0` is explained:** B′'s agent was restarted three times overnight, so `wg0` was recreated and
no client has dialled in since. The peers are configured correctly and nobody connected. **Nothing unexplained —
no finding.**

**But they would have broken §B:** revoking B′ at step 9 would cascade SEVEN devices, not three, and with
`.2`-`.5` occupied a restore that cannot reclaim allocates fresh — **WF-S13-4's exact trigger**, which §B's
staging note explicitly set out to avoid ("stage no decoy, so every device reclaims"). Revoked via the UI (a
product action) before staging; their §A evidence is already recorded above.

## VERIFIED-CORRECT: a re-enrolled gateway keeps its WireGuard key, and nothing resolves identity from it

The revoked and active `aws-gw-2` rows share `wg_public_key LYO7iCch…`; both `aws-gw-1` rows share `zJYoUxPY…`.
That is `loadOrCreateWGKey` persisting the data-plane identity across re-enrolment.

**Checked rather than assumed, because two rows indistinguishable at the WireGuard layer WOULD be a real
collision:**

- **The only lookup BY pubkey is an EXISTS guard** (`nodes.sql:157`, `UpsertNodePeerStatus`) that asserts the key
  belongs to *some other node in the org*. It does not resolve a node, and the row is stored keyed by
  `(node_id, public_key)`.
- **Every other use maps node-id → key**, never the reverse (`service.go:1159`, `:1785`, `failover.go:218`).
- **All three consumers of `ListNodePeerStatusForOrg`** resolve by node id. **No reverse map keyed on a pubkey
  exists in the package.**

**Conclusion: verified correct.** A gateway re-enrolled on the same host keeps its data-plane key and gets a new
control-plane identity, and no code path can confuse the old row with the new one.

**One residual noted, not fixed:** that EXISTS guard has **no `status` filter**, so a REVOKED node's pubkey still
admits peer telemetry. Harmless today (a revoked gateway neither reports nor is reported), and it is the same
`active`-vs-usable shape as WF-S13-1. **Trigger: the next change to `node_peer_status` ingestion.**


# WF-S13-8 (MEDIUM) — a tunnel resolver left on the physical interface after the tunnel is gone

**Raised 2026-08-01. Cost the walk a session.** Filed, ranked, **NOT fixed during the walk.**

## What happened, on the operator's own machine

A WireGuard device was connected and disconnected with **macOS `wg-quick`** (the standalone tool, not the Tunnex
client). `wg-quick up` set the Wi-Fi service's DNS to the tunnel resolver **`10.99.0.1`**; `wg-quick down` did not
put it back. Result: **no name resolution at all, and no indication why.** The tunnel is visibly down, so the
tunnel is the last thing anyone suspects — resolver settings on the *physical* interface are not where you look
when the thing you turned off is already off. macOS `wg-quick`'s restore path is a backgrounded route monitor;
it did not fire here.

**This is the WF-S13-6 shape, in a different subsystem:** a teardown that looks correct, leaves residue, and
nothing that runs afterwards cleans it up. WF-S13-6's residue was an agent that never retried; this residue is a
resolver pointing into a tunnel that no longer exists. Both are invisible until someone needs the thing.

## The product half — CHECKED, NOT ASSUMED, and it is NARROWER than the incident

The incident is a third-party tool. **The honest question is whether Tunnex has the same shape**, and the answer
is mostly no. `apps/helper/backend_darwin.go`:

- `applyDNS` (`:652`) writes **every** service's prior setting to `/var/run/tunnex/dns.json` **before** any
  mutation, and only for a **full tunnel** (`:257`, `cfg.FullTunnel && len(cfg.DNS) > 0`). Split tunnel never
  touches the system resolver.
- `restoreDNS` (`:672`) is called from **three** places, which is the un-strand `wg-quick` lacks:
  graceful `Down` (`:450`), helper-startup `CleanStale` (`:609`), and the **dead-man release** — `CheckDeadMan`
  calls `s.be.Down()` (`state.go:204`) before its crash sweep, so a force-quit/crash restores DNS too.
- A second `applyDNS` cannot poison the backup with the tunnel's own resolver: `Supervisor.Up` refuses a second
  `Up` with `already_up` (`state.go:235`), so the re-entry that would record `10.99.0.1` as the "prior" setting is
  unreachable.

**So the incident does not reproduce through the Tunnex client.** What survives the check is smaller and real:

**`restoreDNS` cannot fail loudly, and it destroys its own retry.** Every restore is `_ = run("networksetup", …)`
(`:686`) — the error is discarded — and `os.Remove(dnsBackupPath)` (`:689`) runs **unconditionally**, outside any
success test. One `networksetup` failure on one service therefore strands that service on the tunnel resolver,
**deletes the record of what it should have been**, and makes the startup `CleanStale` retry a permanent no-op.
Silent, unrecoverable, and it produces exactly the symptom above. Neither proven nor disproven on the wire —
recorded as an unexercised branch, which is where this epic keeps finding things.

## Adjacency to S8.4b — CHECKED, and the guess does not hold

The suggestion on raising this was that it may be the trigger the S8.4b crash/owner-loss resolver sweep was
waiting for. **It is not — that sweep already landed.** `Supervisor.SetOnCrashSweep` is wired in
`apps/helper/cmd/tunnex-helper/main.go:96` and fires from the dead-man release (S8.5 Slice 1), which is the
registered ordering precondition discharged on schedule. The trigger fired at S8.5; the work shipped.

**The two are adjacent but not the same mechanism, and that is the useful part:**

| | mechanism | teardown |
|---|---|---|
| S8.4b sweep | **domain-scoped `/etc/resolver` files** (owned-marker, full-sweep) | client `set_resolvers([])` on graceful down · `CleanStaleResolvers` at startup · **crash sweep on dead-man release** |
| WF-S13-8 | **full-tunnel `networksetup` DNS on every physical service** | `restoreDNS` on Down · startup CleanStale · dead-man (via `Down`) |

Same territory (macOS name resolution), two mechanisms, two independent teardowns — and the crash path sweeps one
of them **by name** while the other rides `Down`. That asymmetry is visible at the call site and is worth stating
even though both are in fact covered: the next person adding a resolver mechanism has two teardowns to wire and
nothing tells them so. **This is a shared-territory instance (`docs/laws.md`), not a new law.**

## RANK — MEDIUM. Registered with a trigger. Not merge-blocking on EPIC 13.

**Why not higher:** the reproducing path is a third-party tool; the product's three restore paths cover the
crash, quit and reboot cases; the surviving defect is a silent-failure branch with no evidence it has ever fired.

**Why not lower:** the failure is total (no DNS), self-concealing (the tunnel is down, so nobody looks), and
**self-destroying** (the backup is deleted, so the automatic retry that exists cannot help). It already cost one
working session, which is more than most MEDIUMs can claim.

**Out of EPIC 13's surface entirely** — the epic is gateway recovery; this is client/helper territory. It does
not gate this merge.

**TRIGGER: the next change to the macOS full-tunnel DNS path, OR the next client walk carrying a full-tunnel leg
— whichever lands first.** The fix shape, when it runs, is one sentence: **restore must report its failures and
must not delete the backup it failed to apply.**

**FILED AS LAW 2026-08-01 (founder-ratified):** *A RECOVERY MECHANISM MUST NOT DESTROY ITS OWN INPUT* —
`docs/laws.md`, entered as the sibling of ABSENCE MUST BE THE CLOSED STATE. Absence-must-be-closed governs the
value an unaware writer supplies; this governs the value a recovery path destroys on the very path where recovery
is what failed. Generalised away from DNS as *cleanup that runs unconditionally after a best-effort apply*, with
the falsifying test named (fail ONE subject; assert the record survives and the next retry still repairs it).


# §B step 4 — STAGING AMENDED before it ran, and the placement question ANSWERED

## Amendment 1 — THREE devices, not four. The optional static is DROPPED.

`b3-pending` · `b3-active` · `b4-managed`. **No static.**

F3's static half is already covered — §A Leg 6 proved `static-keeps` badged with its address unchanged — and what
`b4-managed` tests is the *managed, non-hub-set* residual, a different half. A fourth device would put another
address in the pool for B4's reclaim to route around, which is **precisely the interference revoking §A's four
peers just cleared.** Dropping it costs no coverage and protects the leg.

## Amendment 2 — step 5's connect runs from a LINUX host, not the Mac

WF-S13-8 is filed against exactly the macOS `wg-quick` teardown that would be re-triggered, and re-triggering a
filed finding buys nothing. **If the Mac is unavoidable: record the current Wi-Fi DNS FIRST**
(`networksetup -getdnsservers Wi-Fi`) so it can be restored by hand — the tool's own restore is the thing under
suspicion.

## THE PLACEMENT QUESTION — scaffolding IS still needed, and the failure mode has INVERTED

**Asked before creating: does the picker now reach B′, since `azure-gw` is revoked?** Answered from the code, not
from the guess:

- **No client has a picker at all.** The API has taken `node_id` as an explicit input since S5.1
  (`CreateDeviceRequest.required: [name, node_id]`), and **both clients compute it and hide it** — web at
  `nodepick.ts:30` (`defaultDeviceNode = selectableNodes(nodes)[0]`), CLI independently at `device.go:43-52`.
- **`selectableNodes` filters on `status === "active"` only**, and `ListNodes` is `ORDER BY created_at`
  (`nodes.sql:67-70`). So the pick is *first active by creation time*.
- **ULIDs order by creation:** `019f8e46` (azure-gw, now revoked) → `019fa205` (**k8s**) → `019fb892` (B′) →
  `019fbb50` (A1′). **Revoking azure-gw moves the pick to `k8s`, not to B′.** The guess was right.

**And the failure mode inverts, which is the part worth recording.** The API's readiness gate is
`node.Endpoint == "" || node.WgPublicKey == ""` (`devices/service.go:230`) — the **WireGuard** key, NOT the agent
identity key. `k8s` has `key_recorded = f` (no *agent* public key, which is why it was disqualified as §C's
subject) but it is a live gateway with both an endpoint and a WG key. **So it passes.**

| | before the azure-gw revoke | now |
|---|---|---|
| pick | `azure-gw` — dead, no endpoint | **`k8s`** — live, ready |
| outcome | **LOUD**: `409 node_not_ready` | **SILENT SUCCESS on the wrong gateway** |

The previous state refused and blamed the operator's agent config. This state accepts and homes all three devices
on a gateway §B never intended, with **no product surface able to move them** and nothing in the UI naming which
gateway was chosen. **`active` is the wrong test bites a second time, in the opposite direction** — and the
silent one is worse, because the loud one at least stopped the walk.

This is not a new finding: it is WF-S13-1's second consequence, already ranked and already argued into a slice
(a gateway picker in BOTH clients, defaulting to the current pick, plus a selectability test that is not
`status='active'`). Recorded here because the walk observed the inversion directly.

**Consequence: step 4 stages via the API, recorded as WALK SCAFFOLDING VIA THE API** — the ruled form. It runs
the real create path (validation, address allocation, config issuance, audit), unlike the SQL used for the
azure-gw revoke, but it is **not a product action** and must not be read as one.

### The scaffolding block, as run

Bearer only — **no `X-Tunnex-CSRF` needed**: `csrfGuard` (`http/session.go:60`) fires only when the request
carries the session **cookie**, and a bearer curl carries none.

```bash
API=http://104.45.208.156                                  # azure-cp, nginx host :80 -> container :8080
TOKEN=$(jq -r .token ~/.config/tunnex/credential.json)     # or a browser session bearer
ORG=$(curl -sS "$API/api/v1/organizations" -H "Authorization: Bearer $TOKEN" | jq -r '.[0].id')
NODE=019fb892-...            # B' (aws-gw-2) — the WHOLE POINT of the scaffolding

for d in b3-pending b3-active b4-managed; do
  curl -sS -X POST "$API/api/v1/organizations/$ORG/devices" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$d\",\"node_id\":\"$NODE\",\"provisioning\":\"managed\"}" \
    | jq '{name, id, address, status, node_id}'
done
```

`provisioning` defaults to `managed`; stated explicitly so the record shows it was chosen, not inherited.
Omitting `public_key` has the server generate the keypair and return the private key **once** — that is the
one-time secret, and the returned config is walk-time scratch: **gitignored at creation, never committed.**

**Assert on the way past, before anything else runs:** every response carries `node_id` = B′. A create that
succeeded against `k8s` is the silent-wrong-gateway outcome above and invalidates B3/B4 — it does not merely
misplace a device, it stages the wrong subject for the leg.

**Addresses: RECORD ALL THREE.** B4's whole assertion is that `b4-managed`'s address is **reclaimed** on restore,
which is unprovable without the before-value. §A's four peers were revoked before staging, so `.2`–`.5` should
be free and the three should land low and contiguous.

**Approval gate:** `device_approval` = `on` for staging (step 3), `off` after B3 (step 11). `b3-pending` is
created and **left alone** — it is the only device in the walk whose value is in not being touched.

## WF-S13-1 — THREE CONSEQUENCES NOW, and the third is the worst

One finding, one sentence — ***`active` is the wrong test*** — and it has now produced three distinct failures on
this walk. Recorded together because the *progression* is the argument for the slice, and no single instance
makes it.

| # | consequence | how it failed |
|---|---|---|
| **a** | **Blocked device creation** | `409 node_not_ready` — *"the node has not reported its endpoint/key yet; ensure the agent is enrolled and TUNNEX_NODE_ENDPOINT is set."* **The error blamed the operator's agent configuration.** The cause was a stale row the UI still offered |
| **b** | **Made §B unstageable** | no shipped client sends `node_id`, so with the pick landing elsewhere there was **no product surface able to place the three devices on B′** at all |
| **c** | **SILENTLY creates on the wrong gateway** | with `azure-gw` revoked the pick moves to `k8s`, which **passes** the readiness gate (`Endpoint` + `WgPublicKey`, `devices/service.go:230`) — so the create succeeds, on a gateway §B never chose, with nothing naming which gateway was used |

**(c) is worse than (a), and the reason is the whole point.** (a) was loud: it stopped the walk, and the walk
went and found the cause. (c) is silent — it would have staged B3 and B4 against the wrong subject and the legs
would have "run", producing results about a gateway nobody meant to test. **A finding whose loud symptom is
cleared while its cause survives has not improved; it has gone quiet.**

Note the asymmetry that makes (c) possible: `key_recorded = f` disqualified `k8s` as §C's subject (no *agent*
public key → PoP impossible) and does **not** disqualify it for device placement (which tests the *WireGuard*
key). Two different keys, one row, two correct-but-opposite verdicts. Neither surface is wrong on its own.

### THE SLICE IS A ONE-TRUTH FIX, NOT A DROPDOWN

**Ruled 2026-08-01.** The temptation is to read (b) as "no picker" and ship a picker. That is the wrong shape and
would leave the defect in place.

**The API already knows which gateways can receive work.** `Create` validates `in.NodeID` through `GetOrgNode`
plus the readiness gate — org-scoped, active, endpoint reported, WG key reported. **The clients do not consult
that predicate; each reimplements a weaker one.** Web filters `status === "active"` (`nodepick.ts:30`); the CLI
iterates and breaks on the first active row (`device.go:43-52`). Two independent reimplementations of a rule the
server already owns, both wrong in the same direction, and **that is the one-truth violation** — not the missing
UI control.

**So the slice is: EXPOSE the API's predicate, and have both clients CONSUME it.** A visible gateway choice
follows from that (defaulting to the current pick, so the common case is unchanged), but it is the *consequence*
of the fix, not the fix. Shipping a dropdown over `status === "active"` would let an operator pick a gateway just
as unusable as the one picked for them, and would leave the third consequence exactly where it is.

**Refuted alternatives, kept so they are not re-proposed:** *mark-don't-narrow* (badging bad rows) helps neither
instance — badging does not let an operator choose, and choosing is what both failures needed. *Make a node hub
primary* is refuted by the finding itself — the rule is list order, not the hub set.

**One-truth counter:** this is the epic's next instance and it is filed under the existing law
(`docs/laws.md`), not as a new one.

### CONSEQUENCE (c) IS NOT A PREDICTION — IT ALREADY HAPPENED, AT 03:30

Written above as *"would silently create on the wrong gateway."* The device list shows it **already did**:

| name | address | status | node | created |
|---|---|---|---|---|
| `b3-pending` | 10.99.0.6 | pending | **`019fa205…` = k8s** | 03:30:27Z |
| `b3-active` | 10.99.0.7 | revoked | **k8s** | 03:30:53Z |
| `b4-managed` | 10.99.0.8 | revoked | **k8s** | 03:31:12Z |

Created from the UI, one at a time, each landing on `k8s` — **the silent pick, on the wire, with timestamps.**
No error, no gateway named anywhere in the flow. They were then abandoned and recreated on B′ via the API
scaffolding at 03:59. **`b3-pending` on k8s is still pending and still there** — a stray on the wrong gateway
that nothing in the product would show as misplaced, because nothing shows placement at all.

The prediction and the evidence were written 90 minutes apart, in the wrong order, from the same fleet.


# CP IDENTITY — the credential pointed at a DEAD control plane. Third instance of the provenance hazard.

**Found before step 4, not during.** The CLI credential (`~/.config/tunnex/credential.json`) named
`http://40.65.63.141`. That host **times out**. The live CP is `http://104.45.208.156` (200, nginx/1.27.5),
which is what `docs/infra-inventory.md` has recorded since 2026-07-31 — **the doc was right and the credential
was stale.** Azure public IPs are not stable across a deallocate.

**Identity CONFIRMED, not assumed:** the org (`019f8e44-8e63-7448-ac23-4059230f406e`) holds both
`019fb892…` (B′) **and** `019fbb50…` (A1′, active). A1′ was enrolled during §B step 1 last night, so it exists
only on the control plane this walk has been running against. Same CP, not a rebuild.

**Third instance of the standing provenance hazard**, each in a different disguise: `git fetch` reporting
success while seven commits behind · the rig on `5cf282f` while local was 24 ahead · **a credential authenticating
to a host that no longer exists.** In all three, the failure presents as a *content* error somewhere downstream —
here a `jq: Cannot index object with number` on what looked like a malformed list. **Check what you are talking
to before debugging what it said.**


# §B steps 3 + 4 — ALREADY COMPLETE at 03:59. Recorded, including the duplicate set that followed.

**The gate is ON** (`GET /device-approval` → `{"mode":"on"}`), and B′ carries exactly the intended three:

| device | address | status | approved | node |
|---|---|---|---|---|
| `b3-pending` | **10.99.0.2** | `pending` | `approved_by: null` — untouched, as required | B′ |
| `b3-active` | **10.99.0.3** | `active` | approved | B′ |
| `b4-managed` | **10.99.0.4** | `active` | approved | B′ |

Contiguous `.2`-`.4`, which is what revoking §A's four peers was for. **B4's before-address is `10.99.0.4`** —
the value its reclaim assertion is measured against.

## SCAFFOLDING ERROR — a duplicate set created and revoked, recorded rather than quietly removed

Not knowing steps 3-4 had already run, this session created a SECOND set at 04:59:50 — `b3-pending` `.5`,
`b3-active` `.7`, `b4-managed` `.8`, all `pending` on B′. **All three revoked at 05:0x** (`204` × 3), releasing
those addresses.

**It mattered, and not for tidiness: `.5`/`.7`/`.8` occupied is WF-S13-4's exact trigger** — the interference
that revoking §A's four peers was meant to clear. A restore that cannot reclaim allocates fresh, which is the
finding B4 exists to measure. Leaving them would have staged the interference into the leg.

**Two tool errors caused it, both the same shape and both worth naming.** The create response is
`{device:{…}, private_key, config}` and the projection read TOP-LEVEL `.name`/`.id`/`.address`; the device
address field is `assigned_ip`, not `address`. Both printed `null` for every field, which read as *"the call
failed"* when the calls had in fact succeeded. **A null-valued projection is indistinguishable from a failed
call, and the difference is three live rows.** Read the schema before the filter.

**Consequence still open:** the one-time `private_key` and `config` for the 03:59 set were returned once. Whether
they were captured at 03:59 is unknown to this session — if they were not, `b3-active` and `b4-managed` cannot
connect at step 5 and must be recreated (a one-time secret is never re-issued).
