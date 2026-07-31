# EPIC 13 — RUN PLAN: three distinct runs, three different purposes

**Do not run `docs/S13-boxwalk.md` end to end at a 48-hour TTL.** The legs are one sheet; the *runs* are three,
and they buy different things. Running the full sheet at 48h would spend two days re-proving what §A already
proved at ten minutes.

| run | TTL | purpose | state |
|---|---|---|---|
| **§A** rehearsal #1 | 10m | the epic's mechanics, end to end | **COMPLETE** 2026-07-31 |
| **§B** rehearsal #2 | 10m | the five items §A could not cover, + Legs 7/8, + refusal TIMING | pending |
| **§C** the 48h run | **48h (default)** | **that the shortening knob did not change behaviour** — nothing else | pending |

---

# §A — REHEARSAL #1 (COMPLETE)

Ran 2026-07-31 at `TUNNEX_AGENT_CERT_TTL=10m`. CP `c417c85`, enterprise, schema 64. Full record:
`walk-artifacts/S13.1/walk-record.md`. **Legs 0, 1, 2a, 2b, 3a, 3b, 4, 5, 6 PASS.**

## Two amendments this run forced, which §B and §C inherit

**1. Two hosts, sequenced — not three hosts.** The sheet asks for three expired subjects. The fleet has **two
usable VM agent hosts**, so **A and C are the same box in different states, run in order**: aws-gw-1 recovers by
PoP (Leg 1), is then revoked, expires again, and becomes Leg 3a's subject. This preserves the sheet's actual
requirement — B's refusal (keyless) must never be conflated with C's (revoked) — because B's key stays unrecorded
while C's is recorded throughout.

**2. `azure-gw` cannot host a second agent.** It runs the node-agent **inside k3s**, serving the `k8s` row with
host networking, owning `wg0`. A second host-network agent contends for the same interface. azure-gw is not a
spare gateway host. (`docs/infra-inventory.md`.)

## What §A proved that the others need not repeat

- Uniform refusal **measured**: `403 / 178 bytes` for eight distinct wrong inputs and for two different internal
  causes, against `200 / 57 bytes` for both well-formed identifiers.
- Finding **#6** on the wire: `identities_tried` 1 → 2 across attempts.
- Finding **#5**: three refusals → `exhausted` → fallback, no operator action.
- **F3 in isolation**: `static-keeps` badged with its address unchanged.
- **Specificity**: `decoy-1` silent.
- Findings raised: **WF-S13-1** (registered), **WF-S13-3** (HIGH, fixed), **WF-S13-4**, **WF-S13-5**;
  **WF-S13-2 withdrawn**.

---

# §B — REHEARSAL #2 (10-minute TTL)

**Purpose: the five items §A could not cover, plus the two local legs, plus refusal timing.** ~30 minutes of wall
clock. Same TTL knob, same rig, one staging cycle.

```bash
# [azure-cp] the knob stays at 10m for this run — confirm it is ACTIVE
grep TUNNEX_AGENT_CERT_TTL .env                    # exactly ONE line, =10m
sudo docker compose logs api 2>&1 | grep agent_cert_ttl_shortened | tail -1   # MUST be present
```

## B0a — PROVENANCE: §B runs the SAME BINARY as §A. Do not rebuild.

Everything committed between §A's run and §B is **documentation plus two dev scripts** (`scripts/mutate.sh`,
`scripts/prove-fix.sh`) — no `apps/` code and **no migrations**. Verify rather than trust:

```bash
# [azure-cp] pull the docs; the image is already correct
cd ~/tunnex && git merge --ff-only origin/story/S13.1-gateway-recovery
git log --oneline -1                                     # 779f1a0 or later
git diff --name-only c417c85..HEAD | grep -vE '^(docs/|walk-artifacts/|scripts/)' || echo "DOCS ONLY — no rebuild"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -tAc "SELECT max(version) FROM schema_migrations;"   # 64
```

**`make up-enterprise` must NOT run for §B.** Rebuilding would swap the artifact under the walk for no gain, and
it would cost the thing that makes §B worth running: §A and §B execute the *same* binary, so a §B result that
differs from §A is a difference in the **staging**, not in the build. That is what makes the two runs comparable
rather than merely consecutive.

`git fetch` is not `git pull`. On 2026-07-31 the rig ran `fetch`, reported success, and stayed seven commits
behind — the second instance of the standing hazard, in a different disguise from the first. **Read the sha
back after pulling; do not infer it from the command's exit status.**

## B0 — STAGING ORDER (ruled 2026-07-31). Read before touching anything.

**The roles INVERT from §A.** After §A, `aws-gw-1` is revoked — D3 forbids its recovery — so the only live,
key-recorded gateway is **B′** (`aws-gw-2`, node `019fb892…`). B′ is therefore rehearsal #2's **subject**, and a
re-enrolled `aws-gw-1` is the **restore target**.

Two rulings fixed the order:

- **Approval gate: ON for staging, OFF after B3.** A `pending` device cannot connect (no peer), so B4's managed
  device must be **approved** before it is asked to demonstrate anything. Turning the gate off afterwards leaves
  the org as we found it; existing `pending` rows stay pending, which is what B3 needs.
- **B6's timing runs in the window between expiry and recovery**, against B′ itself. That is the only moment the
  wrong-key population exists — expired, active, key-recorded — without a fourth agent host the fleet does not
  have.

| # | step | why here |
|---|---|---|
| 1 | re-enrol **aws-gw-1** with a fresh token → new row **A1′** | the restore target for B3/B4; aws-gw-1 is revoked and cannot come back any other way |
| 2 | **bind B′ to a site** (Sites → Bind gateway) | B1's precondition — the claim is that `site_id` SURVIVES recovery, so it must exist first |
| 3 | approval gate **ON** | B3 needs a device that is `pending` and stays that way |
| 4 | create on B′: `b3-pending` (leave unapproved) · `b3-active` (approve) · `b4-managed` (approve, managed) · optionally a static | B3 needs both prior statuses; B4 needs a managed device whose address will be RECLAIMED |
| 5 | **connect** `b3-active` and `b4-managed` | a device that never worked cannot show it stopped working |
| 6 | **stop B′'s agent**, record `cert_not_after` at the stop, wait ~10 min | the clock |
| 7 | **B6 — timing**, N BOUNDED | the only window where B′ is expired + active + key-recorded |
| 7a | **drain the throttle**, confirm | B6 spends the SAME bucket Leg 1 needs — see B6's bound |
| 7b | **B7 — saturate deliberately**, then start the agent | measures finding #4 and the agent's 429 handling in one motion |
| 8 | start **B2's poller**, then start B′'s agent | B1 + B2 + Leg 1 in one motion |
| 9 | **revoke B′** → cascade. Assert `revoked_prev_status` is RECORDED | the column §A found empty (WF-S13-3) |
| 10 | **restore onto A1′** | B3 (pending returns pending) + B4 (managed, address reclaimed, gateway moved → NO badge) |
| 11 | approval gate **OFF** | leave the org as found |
| 12 | **B5 — Legs 7/8** locally | independent of the rig; any time |

**WF-S13-4 will not interfere**: it fires only when a device cannot reclaim and allocates fresh. Stage no decoy,
so every device reclaims and B4's "only the gateway moved" holds.

## B1 — Leg 1 with a SITE-BOUND gateway

§A's Leg 1 asserted *"`site_id` unchanged across recovery"* against a node that **had no site binding** — trivially
true, therefore untested. It is one of the epic's headline claims.

**Before staging:** Sites → `aws-site` → **Bind gateway** → `aws-gw-1`. Confirm `site_id IS NOT NULL`, then run
Leg 1 as written and assert `site_id` is **identical** before and after.

## B2 — catch `cert_delivered` false → true

The window is seconds: re-key clears the marker and the agent authenticates immediately after promotion. §A's
sample landed after the flip. **Start the poller BEFORE starting the agent.**

```bash
# [azure-cp] start this FIRST, then start the agent on aws-gw-1
for i in $(seq 1 600); do
  sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -tAc \
    "SELECT now()||' '||cert_serial||' delivered='||cert_delivered
     FROM nodes WHERE id='<A-node-id>';"
  sleep 0.2
done | uniq | tee /tmp/delivered-flip.log
```

**PASS:** the log contains a line with the **new** serial and `delivered=f`, followed by one with `delivered=t`.
That transition is the D3 gate's entire input — re-key clears it in the same CAS, `AuthenticateCert` sets it on
first use, and a marker that never clears makes lost-response recovery impossible.

## B3 — #8's recorded-prior-status path (unblocked now WF-S13-3 is fixed)

§A's cascade predates the fix, so those rows carry NULL and the restore took the unknown-prior branch.

1. Settings → turn the org's **device approval gate ON**.
2. Create a device that stays **`pending`** (never approved) on the source gateway, plus one **`active`**.
3. Revoke the gateway → both cascade. **Assert `revoked_prev_status` is now RECORDED** (`pending` / `active`) —
   the column §A found empty.
4. Restore onto the target.

**PASS:** the pending device comes back **`pending`**, the active one **`active`**. A gateway rebuild must not
grant an approval no human granted.

## B4 — F3's residual, observed rather than assumed

No §A device isolated it: `contended` was managed but had *both* address and gateway changed, so it fired on the
address cause.

Stage **one managed device whose address is reclaimed** — nothing may take it — so **only its gateway moves**.

**PASS:** it shows **no badge**. That is the documented residual (a managed device re-homed onto a non-hub-set
gateway relies on the dial channel, which only re-points hub-set members). **RECORD it as observed**; a rig whose
target happens to be a hub-set member would show self-healing and be misread as "no residual".

## B5 — Legs 7 and 8, local

Both against the local stack, inside the same 10-minute window.

**Leg 7 — lost response.** Point the agent's `TUNNEX_API_URL` at a proxy that forwards `POST /api/v1/agent/rekey`
and kills the connection **after the CP commits, before the body returns**.

**PASS:** CP logs `node_rekeyed` while the agent logs no `agent_rekeyed`; `rekey-pending-key.pem` exists; **the
same process recovers on its next pass** via `identified_by=key_fingerprint`; node id unchanged; pending file
gone after promotion.

**Leg 8 — save failure after commit.** Make the state dir unwritable *to the agent* (a read-only bind mount of a
file over `key.pem`, or a size-0 tmpfs) with a **valid token present**.

**PASS:** `agent_save_creds_failed`, the agent **does not enrol**, and a later pass recovers the **same node id**
once the write can succeed. **FAILING OBSERVATION:** a new node appears in Gateways — that is the identity being
destroyed by a disk condition.

## B6 — TIMING: the third dimension of uniform refusal

> ### BOUND N, AND DRAIN BEFORE LEG 1
>
> Each sample is **challenge + submit = 2 requests**, and the re-key bucket is **600/min**. Three populations at
> 100 samples each is 600 requests — the bucket **exactly** — and Leg 1's recovery would then be throttled and
> read as a failure that is not one.
>
> **It is one bucket, not several.** The throttle keys on the RAW peer address and is registered above
> `middleware.RealIP`; the CP sits behind nginx, so every request arriving through the proxy — the timing curls
> from azure-cp AND the agent's re-key from aws-gw-2 — is attributed to nginx's address. That is finding #4, and
> it is why this section can starve the next one.
>
> **N = 40 per population** (3 × 40 × 2 = **240** requests, 40% of the budget), paced with a short sleep.
>
> **Then confirm the drain before starting the agent:**
>
> ```bash
> sleep 70    # the window is a fixed minute; let it roll over
> curl -s -o /dev/null -w 'drain check = %{http_code}\n' -X POST localhost/api/v1/agent/rekey/challenge \
>   -H 'content-type: application/json' -d '{"cert_serial":"drain-probe"}'      # MUST be 200, not 429
> ```
>
> **Window order, fixed:** stop → expiry → **B6 timing** → **drain confirmed** → B7 → B2 poller started →
> agent started (Leg 1).

### The measurement

§A proved status code and body length are identical. **Timing was never measured**, and finding #16 conceded the
one asymmetry the ordering cannot remove: **wrong-key is the only refusal that pays for a full RSA
verification**, because it passes the gate. Unknown-identifier and revoked-node refusals do not.

**Measure it rather than asserting the residual is small.**

Three populations, N ≥ 50 each, against the **same** control plane in one sitting:

| population | how to produce it | work done before refusal |
|---|---|---|
| **unknown serial** | a serial no row holds | one indexed lookup |
| **revoked node** | the revoked gateway's real serial | lookup + gate |
| **wrong key** | a **live, expired, key-recorded** node's real serial, real nonce, **wrong signature** | lookup + gate + **RSA verify** |

The third requires a node that *passes* the gate, so **stage it before that node is recovered**.

```bash
# [azure-cp] N timed submits per population; each needs its own fresh nonce
timed() {  # $1=label $2=identifier-json
  for i in $(seq 1 50); do
    N=$(curl -s -X POST localhost/api/v1/agent/rekey/challenge -H 'content-type: application/json' \
        -d "$2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["nonce"])')
    curl -s -o /dev/null -w '%{time_total}\n' -X POST localhost/api/v1/agent/rekey \
      -H 'content-type: application/json' \
      -d "{$3,\"nonce\":\"$N\",\"csr\":\"$CSR\",\"signature\":\"$SIG\",\"agent_version\":\"0\"}"
  done | sort -n | awk -v l="$1" '{a[NR]=$1} END{printf "%-16s n=%d  p50=%.4f  p95=%.4f  max=%.4f\n", l, NR, a[int(NR*0.5)], a[int(NR*0.95)], a[NR]}'
}
```

**Report the three distributions.** Then state the residual **with a number**:

- if wrong-key's p50 sits inside the spread of the other two, the residual is **bounded below measurement noise**
  — say so with the figures;
- if it is separable, say **by how much**, and what an attacker learns: only *"this serial belongs to a real,
  expired, key-recorded node"* — which reaching that path already required.

Either way it is **measured, not asserted** — the same standard §A applied to status and body length.

## B7 — THE THROTTLE, MEASURED (its only wire exercise)

Finding **#4** is registered as a bounded limitation **on a code read alone**: *"in every shipped topology the
peer is the edge proxy, so the throttle is one global bucket and any unauthenticated caller can starve fleet-wide
recovery."* Nothing has ever observed it. This costs one minute and settles it.

**Its own line in the record — not folded into B6.**

```bash
# [azure-cp] deliberately exhaust the shared bucket — 700 challenges, past the 600/min ceiling
for i in $(seq 1 700); do
  curl -s -o /dev/null -w '%{http_code} ' -X POST localhost/api/v1/agent/rekey/challenge \
    -H 'content-type: application/json' -d '{"cert_serial":"saturate"}'
done | tr ' ' '\n' | sort | uniq -c
#   EXPECT: ~600 x 200, then 429s. RECORD the counts.

curl -s -D- -o /dev/null -X POST localhost/api/v1/agent/rekey/challenge \
  -H 'content-type: application/json' -d '{"cert_serial":"saturate"}' | grep -iE '^HTTP|retry-after'
#   RECORD the 429 and its Retry-After.

sudo docker compose logs api --since 2m 2>&1 | grep rekey_throttled | tail -3
#   #13's fix: the 429 must leave a SERVER-SIDE line naming the peer. Before Batch D there was none.
```

**Then, with the bucket still exhausted, start B′'s agent.** It arrives through the same proxy, so it is throttled
too — which is the point.

**PASS — three things, and the third is the decisive one:**

1. **`agent_rekey_throttled`, NOT `agent_rekey_refused`.** A refusal means *this will never work*; a 429 means
   *not right now*. Conflating them is what made the agent print the destructive "mint a join token" remedy for a
   rate limit.
2. **The server's `Retry-After` is HONOURED**, not merely printed — `retry_in` in the log matches the header
   (Batch B, claims 9/10/14: the value used to be parsed into an error string and discarded while the agent
   retried on its own floor, so the log and the code stated different intervals).
3. **The agent RECOVERS once the window rolls over, with the SAME node id.** Throttles must not count toward the
   three-refusal exhaustion. B′ has a token in its environment, so if they did count, it would fall back and
   enrol as a **NEW node** — decisive in one field, exactly like the precedence check.

**What this measures for #4:** how many requests one unauthenticated caller needs to deny gateway recovery to
every gateway behind that proxy, and how long the denial lasts. Record both numbers. The limitation stays
registered either way — but registered **with measurements** rather than with an inference.

---

# §C — THE 48-HOUR RUN. NARROW. ONE SUBJECT.

## §C.0 — DELETE the TTL line. Do not append an override.

```bash
# [azure-cp]
sed -i '/TUNNEX_AGENT_CERT_TTL/d' .env
grep -c TUNNEX_AGENT_CERT_TTL .env                 # MUST print 0
sudo make up-enterprise
sudo docker compose logs api 2>&1 | grep -c agent_cert_ttl_shortened   # MUST print 0
```

**Why deletion and not an override.** Compose takes the **first** value for a repeated key. A line reading
`TUNNEX_AGENT_CERT_TTL=48h` appended *below* an existing `=10m` leaves **10m winning** — and you discover that ten
minutes into a run you believe is forty-eight hours, having already staged and stopped everything.

**The absence of `agent_cert_ttl_shortened` in the API log is the gate.** Its presence means the run is void
before it starts.

## What this run is actually for

**It proves the shortening knob did not change behaviour.**

`TUNNEX_AGENT_CERT_TTL` is **new code, written during staging, with no review pass and no box-walk of its own**.
Everything §A and §B prove is proven *through* it. If the knob altered anything — issuance, the renewal anchor,
the gate's arithmetic — every earlier result inherits the flaw.

So §C is a **differential**, not a second proof of the epic. One subject, natural expiry, one recovery, compared
against §A's result at 10m.

**It is NOT:** a re-run of the leg sheet · a second F3 proof · a second uniform-refusal matrix. Those were proven
at 10m and the knob is the only thing that could invalidate them — which is exactly what this measures.

## §C.1 — the run

1. **One gateway**, site-bound (carry B1's binding), enrolled on the branch build with the knob **absent**.
2. Record: `cert_not_after` **must be ~48h out**, not ten minutes. That single value is the knob's own proof.
3. Devices staged and **connected** on it, as in §A.
4. **Stop it. Wait out the natural expiry.**
5. Start it → **Leg 1 only**: PoP self-recovery.

**PASS — the differential, item by item:**

| | §A (10m) | §C (48h) — must match |
|---|---|---|
| `agent_rekeyed`, `identified_by=cert_serial` | ✅ | must match |
| node id unchanged | ✅ | must match |
| **`site_id` unchanged** | untested in §A | **B1 covers it; §C confirms at the real TTL** |
| serial + fingerprint moved | ✅ | must match |
| audited succession, both fingerprints | ✅ | must match |
| `cert_delivered` false → true | B2 | must match |
| new `cert_not_after` | +10m | **+48h** |
| zero operator commands | ✅ | must match |

**Anything that differs between §A and §C is a defect in the knob**, and that is the finding this run exists to
produce.

## §C.2 — the ceiling, once, cheaply

```bash
# [azure-cp] the knob must refuse to LENGTHEN — unit-proven, confirmed once on a real deployment
echo 'TUNNEX_AGENT_CERT_TTL=720h' >> .env && sudo make up-enterprise
sudo docker compose logs api 2>&1 | grep agent_cert_ttl_clamped | tail -1   # MUST be present
sed -i '/TUNNEX_AGENT_CERT_TTL/d' .env && sudo make up-enterprise
sudo docker compose logs api 2>&1 | grep -c agent_cert_ttl_shortened        # MUST print 0 again
```

A month must clamp to 48h. Revocation here is refusal-to-renew, so the certificate lifetime **is** the window a
revoked agent keeps working — and no environment may extend it.
