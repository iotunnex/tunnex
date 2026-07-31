# EPIC 13 — Gateway Recovery box-walk (RUNSHEET)

Status: **RUNSHEET** (plan). Executes in a walk session, AFTER the epic-end review pass. Walk evidence is committed
DURING the session under `walk-artifacts/S13/`; any scratch key material (device configs, join tokens, agent state
dirs) is **gitignored at creation** — device configs contain private keys.

## What this walk must prove

EPIC 13 exists because of one observed event: **an AWS gateway went offline past its 48-hour certificate lifetime and
could not come back.** Its certificate had expired, so it could not authenticate to the mTLS agent channel — and
`/agent/renew`, the only endpoint that could issue a new certificate, lives *behind* that channel. The recovery path
required the credential that had failed.

So the walk's subject is a recovery that must work **and a recovery that must not**:

1. **A gateway recovers itself** by proof of possession, keeping its node id, its site binding, its history and its
   devices. The leg the epic exists for, and the one the real box failed.
2. **A revoked gateway is REFUSED** the same recovery. D3's security property, and the **most important negative leg
   in the epic**: proof of possession must never overturn a human decision, because it cannot distinguish the
   legitimate holder from whoever took the key.
3. **The coverage limitation is honest** — a gateway whose key the control plane never recorded cannot recover this
   way, and the agent must say so *locally*, in words an operator can act on, because the control plane's refusals
   carry no reason by design.
4. **The user-facing consequences are handled** — cascade-revoked devices come back, deliberately-revoked ones stay
   dead, and a device that comes back on a different address says so instead of silently failing to connect.

**Evidence, not inference.** "The gateway came back" is inference. *"The node id is unchanged, `site_id` is unchanged,
the audit row records a succession with both key fingerprints, and the agent's log says `agent_rekeyed`"* is evidence.

## Two bars that decide pass vs finding

- **ZERO-TOUCH.** The only commands you may hand-run are **diagnostic** — reading logs, audit rows, `wg show`,
  `psql` SELECTs. Anything you must hand-run to make *recovery* work is a **FINDING**: number it and HOLD it. The
  agent is supposed to recover itself; a walk where the operator nudges it has proven nothing.
- **STAGING IS NOT A WORKAROUND, AND IT IS DECLARED.** Three legs need a state that cannot be produced on demand (an
  expired certificate, a node with no recorded key). The `UPDATE` statements that stage them are listed explicitly
  below and are **part of the setup, not part of the procedure**. A staging statement that appears anywhere other
  than a Staging block is a finding.

### STANDING RULE — carried from EPIC 11, and it applies to every negative leg here

**Could this check have failed?** A refusal that already mutated something is not a refusal, so every negative leg
ends by re-reading the row it was supposed to leave alone. And a witness must prove it was alive across the window it
certifies: before the leg, confirm it is replying *now*; after, check its timestamp bounds straddle the leg's own
start and end.

---

## Prerequisites — what Pawan needs staged

### THREE expired gateways, not two — and the reason is the uniform-refusal surface itself

Legs 1, 2 and 3a each need a gateway whose certificate has genuinely expired, and they need **three different
ones**, because the refusals are indistinguishable **by construction**:

| leg | subject | why it must be its OWN host |
|---|---|---|
| **1** PoP self-recovery | **A** | A ends the leg RECOVERED, holding a fresh 48h certificate. It is no longer an expired subject for anything after it |
| **2** keyless → token fallback | **B** | its `cert_public_key` is nulled as declared staging, so PoP is refused *for lack of verification material*. The leg then re-enrols B by token, which issues a fresh certificate — B is no longer expired either |
| **3a** revoked → refused | **C** | refused *because it was revoked* |

**Running 2 and 3a on one host proves neither.** The endpoint returns the SAME refusal for a revoked node and for
a node with no recorded key — that uniformity is D8/D9, deliberate, and the whole point of the surface. So a
single host carrying both conditions produces one refusal attributable to either cause, and the leg cannot
distinguish what it just proved. The evidence that separates them lives in the CP log and in the agent's own
local diagnosis, and each needs a subject in exactly one of the two states.

A third host is cheap to stage and **impossible to add on walk day** — it needs 48 hours it cannot get.

### Staging order — FORCED, and the stop is last

The clock and the device prerequisites are in tension: **a live agent renews every 24h and pushes `not_after`
forward**, so A cannot be live while its clock runs — but the devices Legs 4/5/6 need must be created *and
connected* on A **while A is live**. So the order below is not a preference, it is the only order that works. Do
not spend the 48-hour wait before the prerequisites exist.

| # | step | on | must be true before moving on |
|---|---|---|---|
| 1 | Control plane at this branch, **enterprise**, schema 61 | azure-cp | Leg 0's CP census recorded |
| 2 | Agent image at this branch on **A, B, C** — restart in place where possible | each host | Leg 0's per-host census recorded (sha + edition) |
| 3 | All three enrolled and healthy | azure-cp | a node row per host, `key_recorded = t` for **A** and **C** |
| 4 | **Create the devices on A** — `keeps` and `contended` (Legs 4/6) and `deliberate` (Leg 5) | UI / API | three device rows homed on A |
| 5 | **CONNECT each device and pass traffic** | the device | a handshake and non-zero transfer counters in `wg show` on A |
| 6 | **Revoke the `deliberate` device** as an admin | UI | `revoked_cause = 'deliberate'` on that row |
| 7 | Record identity for A, B, C: serial, `cert_not_after`, fingerprint | azure-cp | `walk-artifacts/S13.1/clock-record.md` filled |
| 8 | **STOP all three agents — LAST** | each host | `docker ps` empty per host; stop timestamp recorded |

Step 5 is not ceremony. Leg 6 asks whether a *user* can tell their config went stale; a device that never
connected cannot demonstrate that it stopped working, and a `needs_reexport` badge on a device nobody ever used
proves the badge renders, not that it warns anyone.

Step 8 last, and **idle is not stopped** — `renewLoop` runs regardless of whether anything is reconciling, and one
renew silently costs another 48 hours.

### The rest

| # | Requirement | Why | Clock? |
|---|---|---|---|
| 1 | **azure-cp up (enterprise edition)** | several legs read audit rows and device state | — |
| 2 | **A live REPLACEMENT gateway for Leg 4** | the operator restore re-homes onto a live node, and the server refuses a revoked or foreign target. **B-after-token-re-enrolment (Leg 2) serves** — it is a fresh live node by then — so no fourth host is needed, but Leg 2 must run BEFORE Leg 4 | none |
| 3 | The **agent logs reachable** on A, B and C | the agent's local diagnosis is the *subject* of Leg 2, not a debugging aid | — |
| 4 | A local stack (`make up-enterprise`) | Legs 0b, 3b and 7 | **CLOCK-FREE — stage any time** |
| 5 | **Dropped-response tooling for Leg 7** — a proxy that forwards `POST /agent/rekey` and kills the connection before the body returns, or a scripted `SIGKILL` of the agent between the CP's commit log line and its own save | Leg 7 has no subject without a way to lose a response | **CLOCK-FREE — stage any time** |

Shell vars: `CP=http://10.0.0.4` · `ORG=<org-uuid>` · on azure-cp, `cd ~/tunnex`.

### Which legs need the rig, and which are local

| leg | where | why |
|---|---|---|
| 0 | both | provenance is per-environment |
| **1** PoP self-recovery — **host A** | **RIG ONLY** | needs a genuinely expired certificate on a real agent (48h clock) |
| **2** keyless → token fallback — **host B** | **RIG ONLY** | same, plus the agent's own log is the evidence |
| **3** revoked → refused — **host C** (3a) | **RIG** (3a) **+ LOCAL** (3b) | 3a is the agent's own behaviour; 3b drives the endpoint directly with `curl` so the *refusal surface* is exercised without spending a 48h gateway |
| 4 operator restore | RIG | needs Leg 2's re-enrolled B as the live target, and Leg 3a's revoked C as the source |
| 5 deliberate revoke stays dead | RIG | rides Leg 4 |
| 6 re-addressed → config out of date | RIG | rides Leg 4 |
| **7** lost-response recovery (D10) | **LOCAL** | the newest code, wire-unproven; drivable against a local CP without burning a 48h gateway |

---

## Leg 0 — provenance census

**A stale image reproduces symptoms that look like defects.** And per EPIC 11's finding: **the edition is half of
provenance, and it must be re-asserted after every rebuild** — `docker compose up -d --build api` silently rebuilds
the OPEN image (`go build -tags ""`), which was missed for several legs last time.

```bash
# [azure-cp]
cd ~/tunnex && git fetch && git checkout main && git pull
SHA=$(git rev-parse --short HEAD) && echo "census sha=$SHA"
sudo make up-enterprise && sudo make migrate
curl -s localhost/api/v1/meta | grep -o '"edition":"[a-z]*"'          # -> enterprise
```

Then the surfaces this epic added, each confirmed present rather than assumed:

```bash
# [azure-cp] schema version and the two columns the recovery path depends on
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c "SELECT version, dirty FROM schema_migrations;"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c "\d nodes" | grep -E "cert_not_after|cert_public_key|cert_key_fingerprint"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c "\d devices" | grep -E "provisioned_ip|revoked_cause"

# The re-key routes exist and are UNAUTHENTICATED — a 403 refusal, never a 401 or a 404
curl -s -o /dev/null -w '%{http_code}\n' -X POST $CP/api/v1/agent/rekey/challenge \
  -H 'content-type: application/json' -d '{"cert_serial":"nobody-has-this"}'      # -> 200 (anti-enumeration: a nonce for anything)

# How many gateways can recover by PoP at all? This is the coverage limitation, measured before it matters.
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, cert_not_after < now() AS expired, cert_public_key IS NOT NULL AS key_recorded,
          left(cert_key_fingerprint,12) AS fp FROM nodes ORDER BY enrolled_at;"
```

- **PASS:** `version=61, dirty=f` · all five columns present · challenge returns **200 for a serial nobody has**
  (the anti-enumeration property, observable) · and the census table printed.
- **RECORD the census table verbatim.** It is the walk's baseline *and* the honest statement of coverage: every row
  with `key_recorded=f` is a gateway that can only recover by join token, and Leg 2 is about exactly those.
- **IF `version < 61` → STOP.** Provenance failure, not a code failure.

---

## Leg 1 — A GATEWAY RECOVERS ITSELF (the leg the epic exists for)

**Subject: gateway A** — offline ≥48h, certificate expired, `status=active`, `cert_public_key` recorded.

```bash
# [azure-cp] BEFORE — the identity that must survive, captured as evidence
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT id, name, cert_serial, site_id, status, cert_not_after, left(cert_key_fingerprint,12) AS fp
   FROM nodes WHERE name='<A>';"
```

Then **start gateway A and touch nothing else.**

```bash
# [gateway A] the agent's own account of what it did
journalctl -u tunnex-node -f | grep -E 'agent_rekeyed|agent_rekey_refused|agent_rekey_throttled|agent_enrolling'
```

- **PASS — all six, and the first is the whole epic:**
  1. `agent_rekeyed` in the agent log, with `identified_by=cert_serial ...`
  2. **the node id is UNCHANGED** (not a new row with the same name)
  3. **`site_id` is UNCHANGED** — the gateway comes back bound to its site
  4. `cert_serial` and `cert_key_fingerprint` have both MOVED (new credential, same identity)
  5. an audit row `node.rekeyed` carrying `old_cert_serial`, `new_cert_serial`, **both key fingerprints**, and
     `authorized_by` — a *succession*, so "this gateway was rebuilt on the 4th" is answerable later
  6. **zero operator commands** beyond starting the agent
- **EVIDENCE:** the before/after node rows side by side, the audit row's metadata, the agent log lines.
- **The audit fingerprint is a 12-hex PREFIX of the full identifier** (D10 redefinition) — confirm
  `old_key_fingerprint` matches the `fp` column captured before the leg. If it does not, the audit trail is naming
  keys in a vocabulary nothing else speaks.

---

## Leg 2 — THE COVERAGE LIMITATION, and the agent's local diagnosis

A gateway enrolled before migration 0057 has **no recorded public key**, so there is nothing to verify a proof
against — and "I cannot check" must never resolve to "it is fine". Those gateways recover by join token, which is why
D1(a) keeps it as the always-available manual path.

**Staging (declared):**

```bash
# [azure-cp] STAGING — simulate a pre-0057 node on HOST B ONLY. This is setup, not procedure.
# B is the keyless subject and nothing else: it must NOT also be revoked, or its refusal has two causes and
# proves neither (the endpoint answers identically for both).
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "UPDATE nodes SET cert_public_key = NULL WHERE name='<B>';"
# Confirm the generated fingerprint went NULL with it — the column is derived, not written
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, cert_public_key IS NULL AS key_gone, cert_key_fingerprint IS NULL AS fp_gone FROM nodes WHERE name='<B>';"
```

Start gateway B. It will attempt PoP, be refused, and back off.

- **PASS:**
  - `agent_rekey_refused` appears, and **its text is the subject**: it must name the local finding (*this agent's
    certificate has expired*), state that the server gave **no reason and why** (refusals are uniform by design), name
    the most likely cause, and give the remedy — *mint a join token and restart with `TUNNEX_JOIN_TOKEN` set*.
  - The backoff **doubles toward the one-hour ceiling** and the agent **does not exit** — liveness up, readiness
    false. A CrashLoopBackOff here is a finding: an enrolment refusal is a condition the control plane can resolve,
    and exiting forfeits the reconciliation that would have fixed it.
  - `fp_gone = t` — the fingerprint is **generated from the key**, so removing one removes the other. A non-NULL
    fingerprint over a NULL key would match every other keyless node.
- **Then the fallback, per the remedy the agent printed:** mint a join token in the UI, set it, restart.
  - **PASS:** the gateway enrols. **It is a NEW node** — and the agent said so before it happened
    (`agent_falling_back_to_join_token`: *"this creates a NEW node: its site binding must be re-applied and devices
    homed on the old node need re-issuing"*). Confirm the new row's id **differs** from the old, and that the warning
    was printed **before** the enrolment, not after.
- **EVIDENCE:** the full refusal log line (it is the deliverable), the backoff progression, both node ids.

> **This leg is the honest half of the epic.** Recovery does not work for every gateway, and the walk proves the
> product *says so at the only place it is observable* rather than leaving an operator watching a silent agent.

---

## Leg 3 — A REVOKED GATEWAY IS REFUSED (the most important negative leg)

### 3a — on the rig, through the agent

**Subject: gateway C** — expired, and now revoked. NOT B: B is the keyless subject, and a host carrying both
conditions produces a refusal attributable to either, which the uniform-refusal surface makes indistinguishable by
construction. C's `cert_public_key` must still be RECORDED (`key_recorded = t`), so the ONLY reason it can be
refused is the revocation — that is the whole point of the leg.

Revoke it in the UI (Gateways → Revoke), which also **cascades to its devices** — that cascade is Leg 4's premise.

> **Move A's devices to C before this leg, or give C its own.** Leg 4 restores a REVOKED gateway's devices, and C
> is the revoked one. Either home the Leg 4/5/6 devices on C at staging step 4 instead of A, or accept that Leg 4's
> source is C and stage its devices accordingly. **Pin this at staging time** — discovering on walk day that the
> cascade landed on the wrong host costs the leg.

```bash
# [azure-cp] BEFORE: the row that must be UNTOUCHED afterwards, and the cascade
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT id, cert_serial, status, revoked_at, cert_public_key IS NOT NULL AS key_recorded FROM nodes WHERE name='<C>';"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, revoked_cause, assigned_ip FROM devices WHERE node_id='<C-id>' ORDER BY name;"
```

Start gateway C's agent and let it attempt recovery.

- **PASS:**
  - `agent_rekey_refused` — **and the refusal is indistinguishable from every other refusal.** The response carries
    no reason; the agent's log says so explicitly rather than guessing.
  - **The node row is UNCHANGED afterwards** — same `cert_serial`, still revoked. *A refusal that already mutated
    something is not a refusal.*
  - The control plane's own log names the real reason where an operator can read it and an attacker cannot:
    `rekey_refused reason="node is revoked..."`.
- **THIS IS THE PROPERTY THE EPIC WOULD BE UNSAFE WITHOUT.** Expiry is an absence of action; revocation is the
  presence of a decision. A cryptographic proof may overturn the first, never the second — because proof of
  possession cannot distinguish the legitimate holder from whoever took the key.

### 3b — locally, driving the endpoint directly

The uniform-refusal discipline now spans **two identifiers** (D10), and 3a exercises one path through one of them.
Locally, drive the endpoint itself and compare responses **byte for byte**:

```bash
# [local] each must return the SAME status, the SAME error code and the SAME message
for id in '{"cert_serial":"nobody-has-this"}' \
          '{"key_fingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}' \
          '{"cert_serial":"x","key_fingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}' \
          '{"key_fingerprint":"not-hex"}' \
          '{}' ; do
  N=$(curl -s -X POST localhost:8080/api/v1/agent/rekey/challenge -H 'content-type: application/json' -d "$id")
  echo "$id -> $N"
done
```

- **PASS:** a **nonce** for the two well-formed single-identifier cases (anti-enumeration: the challenge never
  confirms existence), and the **identical 403 refusal** for both-identifiers, malformed, and neither — never a 400,
  because a schema violation answering differently from an unknown identifier tells a prober how far they got.
- Then the same comparison on `/agent/rekey` with a garbage signature: unknown serial, unknown fingerprint, live
  node and wrong key must be **the same response**.

---

## Leg 4 — THE OPERATOR RESTORE, walked as a FALSIFICATION ATTEMPT

**Source: gateway C** (revoked in Leg 3a, its devices cascade-revoked with it). **Target: B′** — gateway B after
its token re-enrolment in Leg 2, which is a fresh live node. Leg 2 and Leg 3a must both be done first.

### What this leg is now, and the trap it must not fall into

The leg was written to expose a defect: `RestoreCascadeRevokedDevices` had one caller (`Rekey`), devices are
cascade-revoked in one place (`Revoke`), and `Rekey` refuses a revoked node — so the trigger that created the work
put the node into the one state that could never reach the code that undid it. **Slice 7 was then built to fix
exactly that**, adding the operator-initiated restore.

**So a leg that now walks Slice 7's happy path is a confirmation wearing a falsification's clothes.** The code was
written against this leg; of course the endpoint returns 200. That proves the author and the walker agree, which
is worth nothing.

The claim under test therefore moves one level up, to the thing Slice 7 does **not** self-evidently establish:

> **A restored device is back IN SERVICE — not merely back to `status='active'`.**

That is falsifiable, and it is the claim Wall 6 actually made.

### THE FALSIFYING OBSERVATIONS — write these down before running anything

The leg **FAILS** on any one of these. They are listed first, deliberately, so the result cannot be read backwards
from whatever happens.

| # | observation that FALSIFIES the claim | why it is a real risk, not a formality |
|---|---|---|
| **F1** | the restored device appears in `psql` as `active` on B′, but **`wg show` on B′ does not list its peer** | the restore is a database write plus a push. If the push does not place the peer, the device is "restored" into a config the data plane never learned |
| **F2** | the device is active and peered, but **cannot pass traffic with the config it already holds** | its existing config embeds the **old gateway's endpoint and public key**. Re-homing changes `node_id` in the row; it cannot change a file on the user's laptop |
| **F3** | **F2 happens and NOTHING tells the user.** `needs_reexport` stays absent for a device that kept its address | Slice 6 derives staleness from the **address** and the baked **ranges**. It does not compare the **gateway**. A re-homed device that reclaimed its address is, on that logic, perfectly fresh — and unusable |

**My prediction, recorded before the walk so it cannot be adjusted after: F3 WILL FIRE.** I can find no code that
compares the issued config's gateway against the device's current node, and the re-home path is new. If the walk
refutes that prediction, the prediction was wrong and the leg passes — which is the point of writing it down.

**F1 and F2 are genuinely open.** I have not traced the push far enough to predict them, and a walk is what
settles that.

If all three hold clean, the leg passes and Wall 6 is closed on the wire. If F3 fires alone, the mechanism works
and the *surface* is incomplete — a finding, held, not fixed here.

### Forcing the re-address case — PINNED: deliberate pre-allocation

Half this leg is the reclaim-first behaviour, and with a roomy pool it never fires: the allocator hands the
restored device a fresh address, the `readdressed` path is never taken, and the leg silently proves half of what it
claims. **Correct code whose trigger never co-occurs — the same shape as the defect this leg exists for.**

**Mechanism (pinned; do not substitute a pool resize):** after C is revoked and before the restore, create a decoy
device on B′ and confirm it took the address the `contended` device held.

```bash
# [azure-cp] 1. the address to contend for — captured BEFORE anything is created
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, revoked_cause, assigned_ip FROM devices WHERE node_id='<C-id>' ORDER BY name;"
#    record: contended = <the assigned_ip of the device named `contended`>

# 2. create a decoy device on B′ in the UI, then CONFIRM it took that exact address
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, assigned_ip FROM devices WHERE name='decoy-1';"
```

- Cascade-revoked rows **keep** `assigned_ip` but are excluded from `ListActiveDeviceAllocations` (status filter),
  so that address reads as free and the allocator should hand it to the next device created.
- **If `decoy-1` did NOT take it**, create `decoy-2`, `decoy-3` … and check each. **Bound it at five.** If five
  decoys have not taken the address, STOP and record it: the allocator does not behave as assumed, which is itself
  worth knowing, and the pool-resize fallback goes in the record as a deviation rather than being done silently.
- The `keeps` device's address must be left **untouched**, so one device reclaims and one cannot. Both halves in
  one restore is the only way to see the discrimination work.

### Sequence

```bash
# [azure-cp] BEFORE — the cascade, with addresses PRESERVED
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, revoked_cause, assigned_ip FROM devices WHERE node_id='<C-id>' ORDER BY name;"
```

- **PASS criterion 0, independent of everything else:** cascade-revoked devices **keep `assigned_ip`**. Revocation
  preserves what it invalidates; a revoked row that lost its address made the original unreclaimable *in principle*.

Then, in the UI: **Gateways → the revoked C → "Restore devices" → choose B′.** Zero-touch bar applies — if this
needs a `curl` because the affordance is missing or broken, that is a finding.

- **PASS:**
  - `restored=2`, `readdressed=1` — `keeps` reclaims its address, `contended` gets a fresh one;
  - both rows now `active` **and `node_id = B′`**;
  - audit: one `node.devices_restored` naming **the human**, plus per-device `device.restored` /
    `device.restored_readdressed` carrying `previous_node_id`;
  - **F1 checked:** `wg show` on B′ lists both peers;
  - **F2 checked:** the `keeps` device, using **the config it already had**, connects and passes traffic through
    B′ — or does not, which is F2;
  - **F3 checked:** the Devices list shows **`config out of date`** for `contended` (address changed) — and what it
    shows for `keeps`, which is the prediction.

## Leg 5 — A DELIBERATELY-REVOKED DEVICE STAYS DEAD

Rides Leg 4. The third device (prerequisite 4) was revoked **by an admin** before the gateway was, so it carries
`revoked_cause='deliberate'`.

- **PASS:** after any restore, that device is **still revoked**, still `deliberate`, and **no** `device.restored`
  audit row names it.
- **WHY IT MATTERS MORE THAN IT LOOKS:** a gateway rebuild that quietly revives a laptop an admin revoked is a
  security regression wearing the costume of a convenience. The two-cause discrimination is the whole mechanism, and
  this leg is the only place it is observable end to end.

---

## Leg 6 — THE RE-ADDRESSED DEVICE SAYS SO

A device restored onto a **different** address holds a config that embeds the old one and **will not connect**. Before
Slice 6 the audit log recorded it and the device surface could not — it rendered exactly as clean as one that never
moved, and its owner would have discovered the problem by failing to connect.

- **PASS:**
  - the re-addressed device shows **`config out of date`** in Devices, and its tooltip names *either* cause without
    prescribing an action its provisioning mode cannot take (the census correction: *a label can lie through the
    remedy it prescribes*);
  - the device that kept its address shows **nothing**;
  - a **managed (desktop-client) device** whose address changed shows it too — that half was the gap, and a rig with
    only static exports proves the wrong half.
- **NEGATIVE HALF, and it must be checked:** a device with **no** snapshot (any row predating migration 0060) shows
  **nothing**. Unknown is not stale; a permanent false positive across a healthy fleet trains operators to ignore the
  surface, which is worse than the surface not existing.

---

## Leg 7 — LOST-RESPONSE RECOVERY (D10, this session's build, wire-unproven)

Beyond the six, and local: the newest code in the epic, proven only against a database.

**The scenario:** the control plane commits a re-key and the answer is lost. It now holds a serial the agent never
received; the agent's stored serial names a row that no longer exists. Before D10 that was permanent — one dropped
packet costing a gateway its identity.

```bash
# [local] simulate the lost answer: drop the RESPONSE, not the request.
# e.g. run the agent against a proxy that forwards POST /api/v1/agent/rekey and then kills the connection
# before the body returns, or SIGKILL the agent between the CP's commit log line and its own save.
```

- **PASS:**
  - the CP logs `node_rekeyed` (it committed) while the agent logs no `agent_rekeyed` (it never saw the answer);
  - `rekey-pending-key.pem` **exists in the agent's state dir** — written *before* the request went out, which is
    what makes the next step possible at all;
  - on retry the agent tries the **fingerprint identity first**, logs `agent_rekey_identity_refused` at most for the
    serial, and recovers **the same node id**;
  - the audit row records `identified_by=key_fingerprint`;
  - after promotion the **pending file is gone** — a superseded pending key would make the next recovery spend an
    attempt on an identifier the control plane does not hold.
- **CONVERGENCE, the property to look for:** a second lost response must not walk the identity forward. The pending
  key is **reused**, so repeated failures converge on one identity instead of leaving the agent proving possession of
  something the control plane never saw.

---

## Anti-checklist — every claim proven dead, not assumed

| claim | proven by | not by |
|---|---|---|
| recovery keeps the identity | node id + `site_id` unchanged, audit succession | "the gateway is green again" |
| a revoked gateway cannot recover | the row **unchanged** after the attempt | the attempt returning an error |
| refusals are uniform | responses compared **byte for byte** across six conditions | each one being "a 403" |
| the coverage limitation is honest | the agent's refusal log **read as text** | the code containing a log call |
| devices come back | audit rows + `active` status per device | the gateway having *some* peers |
| a deliberate revoke survives | that device still revoked, **no restore audit row naming it** | the count of restored devices |
| the address change is visible | the badge on a **managed** device | the badge existing for static exports |

## Registered residuals to carry into the walk

- **The Leg 4 premise finding** — surfaced above; a decide-item, not a walk finding.
- **The rolling-upgrade shim (0061)** — `cert_serial` is written but unread this release. The **contract migration**
  (drop it, `identifier NOT NULL`, collapse the `coalesce`) triggers on the release after this one.
- **No general rate limiting** — the re-key routes have their own throttle; login, enrolment and the wider API do
  not. Registered, still owed.
- **Body-size limits** exist only on the two re-key routes.
- **Failover hysteresis counters reset on leadership change** — beta-blocking, owned by the failover story.
