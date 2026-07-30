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

| # | Requirement | Why | Lead time |
|---|---|---|---|
| 1 | **azure-cp up (enterprise edition)** | the rig; several legs read audit rows and device state | — |
| 2 | **TWO gateways offline for ≥ 48 hours** — call them **A** and **B** | `agentca.CertTTL` is a **constant 48h and not configurable**, so an expired agent certificate cannot be manufactured: the clock is the only way. A is the PoP-recovery subject, B is the refusal subject, and B is consumed by its leg (it ends revoked) | **START 48h+ BEFORE THE WALK** |
| 3 | At least **one device homed on gateway A**, ideally two — one whose address will be free at restore, one whose address will have been taken | Legs 4 and 6 have no subject otherwise | — |
| 4 | A **third device, deliberately revoked** by an admin before A is revoked | Leg 5's subject: it must stay dead through a restore that revives its neighbours | — |
| 5 | The **agent logs reachable** on both A and B (`journalctl -u tunnex-node` or `kubectl logs`) | the agent's local diagnosis is the *subject* of Leg 2, not a debugging aid | — |
| 6 | A local stack (`make up-enterprise`) | Legs 0b, 3b and 7 run here — see the split below | — |

Shell vars: `CP=http://10.0.0.4` · `ORG=<org-uuid>` · on azure-cp, `cd ~/tunnex`.

### Which legs need the rig, and which are local

| leg | where | why |
|---|---|---|
| 0 | both | provenance is per-environment |
| **1** PoP self-recovery | **RIG ONLY** | needs a genuinely expired certificate on a real agent (48h clock) |
| **2** token-only fallback | **RIG ONLY** | same, plus the agent's own log is the evidence |
| **3** revoked → refused | **RIG** (3a) **+ LOCAL** (3b) | 3a is the agent's own behaviour; 3b drives the endpoint directly with `curl` so the *refusal surface* is exercised without spending a 48h gateway |
| 4 cascade restore | RIG | rides Leg 1's recovery |
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
# [azure-cp] STAGING — simulate a pre-0057 node. This is setup, not procedure.
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

**Subject: gateway B**, now expired *and* revoked. Revoke it in the UI (Gateways → Revoke), which also **cascades to
its devices** — that cascade is Leg 4's premise, so capture the device rows now.

```bash
# [azure-cp] BEFORE: the row that must be UNTOUCHED afterwards, and the cascade
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT id, cert_serial, status, revoked_at FROM nodes WHERE name='<B>';"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, revoked_cause, assigned_ip FROM devices WHERE node_id='<B-id>' ORDER BY name;"
```

Start gateway B's agent and let it attempt recovery.

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

## Leg 4 — CASCADE RESTORE (premise flagged — read this first)

**Wall 6:** revoking a gateway cascades to every device homed on it, so recovery *without* restoring them hands back
a working gateway with **zero users**, each needing a re-issued one-time config — one rebuild becoming a fleet-wide
user event, invisible until people call.

> ### ⚠ DESK-CHECK FINDING, RAISED WHILE DRAFTING THIS LEG — DO NOT WALK AROUND IT
>
> `RestoreCascadeRevokedDevices` has exactly **one** caller: `nodes.Rekey`, after commit. Devices are cascade-revoked
> in exactly **one** place: `nodes.Revoke`. And **`Rekey` refuses a revoked node** (D3). So the trigger that produces
> cascade-revoked devices puts the node into the one state that can never reach the code which restores them.
>
> On this reading the restore path is **unreachable in production** — correct code wired to a trigger it cannot fire
> from, which is precisely what the DORMANT-MACHINERY law names. It is a **decide-item, not a walk finding**, and it
> is surfaced rather than resolved: either a reachable trigger is named (an operator "restore devices" action on a
> recovered gateway, or an un-revoke that D3 has good reason to refuse), or the mechanism is removed and Wall 6 is
> re-opened honestly.
>
> **Walk it anyway, as a falsification attempt.** The desk analysis may be wrong — that is what a wire proof is for.
> If the sequence below produces `devices_restored_after_rekey`, the analysis was wrong and the leg passes. If it
> produces nothing, the walk has confirmed the finding on the wire, which is worth more than the leg would have been.

**Sequence (rig):** on gateway A — recovered in Leg 1 — revoke it, confirm the cascade, then attempt recovery.

```bash
# [azure-cp] after revoking A: every device homed on it must be revoked with cause='cascade', KEEPING assigned_ip
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, revoked_cause, assigned_ip FROM devices WHERE node_id='<A-id>' ORDER BY name;"
```

- **First PASS criterion, independent of the finding above:** cascade-revoked devices **keep `assigned_ip`**.
  Revocation preserves what it invalidates — a revoked row that lost its address made the original unreclaimable *in
  principle*, and `revoked_cause` is what makes a cascade distinguishable from a deliberate revoke at all.
- **Then**, if a reachable recovery exists for A: `devices_restored_after_rekey restored=N readdressed=M` in the CP
  log, audit rows `device.restored` / `device.restored_readdressed`, and each restored device **active again**.
- **Reclaim-first:** a device whose original address is still free comes back **on the same address**; one whose
  address was taken by a live device comes back on a **fresh** one and is audited distinctly.

---

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
