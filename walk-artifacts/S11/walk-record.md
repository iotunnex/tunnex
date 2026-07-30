# EPIC 11 box-walk — record

Rig: **azure-cp** (control plane, Docker Compose enterprise) · **azure-gw** (k3s in-cluster gateway) ·
**laptop** (macOS desktop client, WireGuard peer `10.99.0.4`).

Census sha at walk start: **`bb144ce`** (`story/S11-slice2`, slices 2–6).

---

## Leg 0 — prerequisites and provenance

**Purpose.** Prove the *running* build is the one under test before any leg draws a conclusion from it, and
establish that the new EPIC 11 surfaces exist on the wire rather than in a source tree.

| check | evidence | verdict |
|---|---|---|
| Toolchain bump is live | build log shows `golang:1.25.12-alpine` | ✅ |
| Migrations clean | `migrate_up_complete version 53 dirty:false` | ✅ |
| **Leader election running** | `{"msg":"leader_acquired","key":6076849370602602497}` | ✅ |
| **Metrics listener running** | `{"msg":"metrics_listener_start","addr":"127.0.0.1:9090","paths":"/metrics /readyz"}` | ✅ |
| `/readyz` expresses the role | `ok leader` | ✅ |
| Health-kind series complete | `grep -c '^tunnex_gateway_policy_health{'` → **13** — matches `nodes.AllKinds()` | ✅ |
| **Metrics port is loopback-only** | `curl http://10.0.0.4:9090/metrics` → `Failed to connect … 0 ms`, `%{http_code}` = **000** | ✅ |
| Tunnel live | gateway `wg show`: peer `10.99.0.4/32`, `latest handshake: 13 seconds ago` | ✅ |
| Witness flow | `ping 10.99.0.1` → 3 received, **0.0% packet loss**; continuous log from 08:51:12 | ✅ running |
| **Operator binaries in the image** | `ls -l /usr/local/bin/` → **`tunnex-api` only** | ❌ **WF-S11-1** |

The loopback result is the security-relevant one and it is a *negative* proof: D3.2 ruled the metrics port
unauthenticated-but-operator-network-only, and `000` from the host's own LAN address is what makes
"unauthenticated" acceptable rather than a hole. An endpoint that cannot be reached cannot have its
authentication be wrong.

### Baseline for Leg 3 (captured before the destructive leg)

```
   name   |           cert_serial            |          enrolled_at
----------+----------------------------------+-------------------------------
 aws-gw-1 | bb79126b0ae42e44d477931c28256b31 | 2026-07-23 09:19:27.539269+00
 azure-gw | cb882e06ed84e96fae65556d2f72b20f | 2026-07-23 09:20:06.404529+00
 aws-gw-2 | 7d84d493ca0d9151981dd0c35f446978 | 2026-07-23 09:23:16.261099+00
 k8s      | 5308c954d61ece1f4d6c67c5fb09f10f | 2026-07-27 05:21:48.285093+00
```

Trust-after-restore (Leg 3) asserts these **four serials are unchanged** after a restore and that no gateway
re-enrols. A changed serial means the agent CA did not survive, which is the failure the manifest exists to
prevent.

---

## WF-S11-1 — operator binaries were never shipped in the api image

**Severity: HIGH.** Found at Leg 0; **blocked Legs 2, 4 and 6 as documented.**

`/usr/local/bin/` in the running api container held `tunnex-api` alone. `api.Dockerfile` built `./cmd/server`
and nothing else, so `preflight` and `backupctl` — both written in this epic, both unit-tested, both named by
command in `docs/upgrade.md` and `docs/backup-restore.md` — did not exist anywhere an operator could run them.

**Why every layer said otherwise.** The commands compile. Their unit tests pass. The docs name them. Nothing in
the repo connected "this binary is referenced by a runbook" to "this binary is in an image" — the seam sits
between the code and the packaging, which is precisely the seam a unit test cannot see. This is
artifact-exists-≠-artifact-works at the **packaging tier**, and it is the fourth instance of that class this
epic.

**Two further defects surfaced while fixing it**, both worse than the missing binaries because a wrong command
fails in a way an operator will misread:

1. `docs/backup-restore.md` invoked **`tunnex-api backup-manifest`** and **`tunnex-api restore-verify`**.
   Neither subcommand has ever existed — the real tool is `backupctl manifest` / `backupctl verify`, which
   `docs/upgrade.md` had right. Two documents describing one procedure disagreed, and the one a *restoring*
   operator reads was the fabricated one.
2. Neither document said **where** to run the commands. A binary that ships in the control-plane image but is
   documented as a bare word is still unrunnable; worse, the master-key fingerprint is only meaningful when
   computed *where that key lives*, so "run it on your laptop" would produce a confidently wrong answer.

**Fix (folded, `deploy/docker/api.Dockerfile` + both docs):** build and `COPY` all three binaries; correct the
binary and subcommand names; give the concrete `docker compose exec` / `kubectl exec` form at every invocation,
with the reason the tools run inside the control plane stated once.

**Guard (`apps/api/cmd/shipcensus_test.go`):** `TestEveryOperatorToolShipsInTheImage` enumerates every
`cmd/` package and requires each to be either built-and-COPY'd in `api.Dockerfile` or listed in a
`notShipped` census with the reason it is deliberately absent (migrate has its own image; codegen, seeds and
the walk bootstrap are not operator tools). It parses the `-o /out/<bin> ./cmd/<pkg>` pairs rather than guessing
binary names, because `./cmd/server` builds `tunnex-api` and a name heuristic would have both false-passed and
false-failed.

**PROVE-A-GUARD-REJECTS, at the hardest instance.** The easy red is a package that is never built. The red run
was the *harder* one — a binary compiled into `/out` and never copied into the runtime stage, which is exactly
as absent as one never compiled while looking present in the build log:

```
shipcensus_test.go:82: cmd/ package(s) are in neither the api image nor the notShipped census:
  [preflight (built as preflight, never COPY'd into the runtime stage)]
```

Clean before, rejects the defect, clean after restoring the COPY.

**A third correction, unprompted by the walk.** `docs/backup-restore.md` claimed trust-after-restore "is
verified on real hardware in the EPIC 11 box-walk." That leg had not run — the claim was pre-dated by the
document describing it. Reworded to name it as the walk's owed proof, conditional on
`walk-artifacts/S11/` recording it.

---

## Leg 1 — the fleet baseline

Captured `2026-07-30T03:34:14Z`, before anything destructive.

```
interface: wg0   public key: KhC1ubO4+9HRyNFujU3QPxnS2Q7V0y2vAgi50DhzlW0=

peer: TOtAdJqLVL/S+9nbWsc2CB+X09vA5qzMB56avEhf4kc=   (the laptop client)
  endpoint: 103.77.0.135:15170   allowed ips: 10.99.0.4/32
  latest handshake: 7 seconds ago
  transfer: 321.42 KiB received, 171.58 KiB sent          <-- Leg 3 must EXCEED this

peer: LYO7iCchBpplzAKRSCw3cHSqxPaMyMJ2tZs5vjSCc0s=   endpoint 15.135.130.96:51820
  allowed ips: (none)                                      <-- STANDBY hub peer (S8.6 HA, correct)
  transfer: 0 B received, 6.71 MiB sent
peer: lrGiH7wTWpsOB4lWox149aI/LgGYrwYzJaaYeVAeJWM=   endpoint 15.134.60.253:51820
  allowed ips: 172.31.0.0/16
  transfer: 0 B received, 6.71 MiB sent
```

**This leg independently closed Leg 0's open item.** Both site-link peers show *sending, receiving nothing* —
the gateway is talking to two AWS hosts that have been down since Jul 23. The health surface reported
`site_link_down` = 4; the wire says `site_link_down`, reached by a different route. Two independent renderings
of the same truth agreeing is what makes the gauge trustworthy for the legs that follow, so
`healthy = 0` is an honest state and **there is no metric defect**. (My prediction going in was that all 13
series would read zero and the gauge had never run. Wrong, and wrong in the direction that matters.)

---

## Leg 2 — backup per the runbook as written — PASS

```json
{
  "version": 1,
  "taken_at": "2026-07-30T03:34:42.896143359Z",
  "master_key_fingerprint": "912f6a205877",
  "schema_version": 53,
  "note": "S11 walk"
}
```

`backupctl verify` → **exit 0**: *"ok: this control plane holds the master key this backup was sealed under
(fingerprint 912f6a205877, taken 2026-07-30 03:34:42 UTC, schema version 53)"*. Dump: 156 761 bytes.

| criterion | verdict |
|---|---|
| manifest carries a key fingerprint | ✅ |
| manifest carries **no key material** | ✅ the only hex present is the 12-char fingerprint; no base64, no 64-hex blob |
| `verify` exits 0 naming what it matched | ✅ fingerprint + timestamp + schema version |

**Registered residual (not a finding):** the fingerprint is 48 bits and the manifest is **unsigned**. That is
correct for the job it actually does — catching operator error, "did you bring the right key" — and the doc
frames it that way. It is not an authenticated artifact: anyone who can write your manifest can write anything
in it. Worth one sentence in `backup-restore.md` rather than a scope expansion.

---

## Leg 3 — TRUST AFTER RESTORE — **PASS, all four proofs. Owed debt #1 DISCHARGED.**

`pg_restore --clean --if-exists` over the live deployment, `03:35:47Z` → `03:35:57Z`, then `restart api`.

| proof | evidence | verdict |
|---|---|---|
| 1. cert serials unchanged | all four byte-identical to Leg 1 (`bb79126b…`, `cb882e06…`, `7d84d493…`, `5308c954…`) | ✅ |
| 2. no re-enrolment | agent-log grep `enroll\|certificate\|csr` over the window returned **nothing** | ✅ |
| 3. counters advanced | 321.42/171.58 KiB → **338.00/188.43 KiB**; handshake 17 s | ✅ |
| 4. no data-path interruption | see below | ✅ |
| (bonus) CP self-recovered | `/readyz` → `ok leader`, leadership re-acquired with no manual step | ✅ |

Proof 1 is the headline: **identical serials mean the agent CA was decrypted out of the restored, sealed data.**
Had the master key not matched what the dump was sealed under, the CA would have been unreadable and the fleet
would have re-issued — which is the silent orphaning the manifest exists to prevent.

**Proof 4, measured rather than eyeballed.** The restore window is `09:05:47–09:05:57` local, which is
`icmp_seq=872–882`. Every sequence number in that range is present at an unchanged ~240 ms:

```
09:05:47 icmp_seq=872 time=240.407 ms      <-- restore begins
09:05:52 icmp_seq=877 time=240.445 ms
09:05:57 icmp_seq=882 time=239.835 ms      <-- restore complete, api restarting
```

And across the entire 15-minute log (`08:51:12` → `09:06:34`, 919 packets): **zero `icmp_seq`
discontinuities, zero timeouts.** The gap detector matters — a dropped packet leaves a hole in the sequence
while the surrounding lines still look continuous, which is exactly how "it looked fine" conceals a data-path
break. A first tail of the log showed only `09:06:15` onward, *after* the window; that would have been
recovery evidence masquerading as survival evidence, so the window was pulled explicitly.

This is the claim in `self-host.md` — *"the control plane degrades; tunnels survive"* — earning its ✅ from a
wire instead of an assertion. The schema was dropped and recreated under a live tunnel and the data path never
noticed.

**Observation, tested and closed — and it exonerates this leg.** Two packets returned at exactly 2× baseline,
25 s apart, matching the peers' `persistent keepalive: every 25 seconds`. n=2 being suggestive rather than
conclusive, the whole log was swept: **47 of 919 packets (~5%) exceed 400 ms**, at sequences
`0, 20, 45, 55, 80, 105, 115, 140, 165, 175, 200, 224, 234, …` — spacing `25, 25, 10` repeating, a 60-second
cycle with three events at +0/+25/+50. That is keepalive-correlated, one extra RTT, **zero packets lost**.

The decisive detail is that it begins at **`icmp_seq=0`**, fifteen minutes before the restore. Pre-existing and
unrelated to Leg 3 — which is why it was worth measuring rather than calling "jitter," the comfortable answer
that would have left an unexplained latency pattern sitting inside the walk's headline evidence.

---

## Leg 4 — the CATASTROPHIC case proven SAFE — **PASS, all four**

```
REFUSING TO RESTORE

master key mismatch: this control plane's master key (fingerprint 2dc7a85e04a7) is NOT the key
this backup was sealed under (fingerprint 912f6a205877).

Restoring anyway would produce a control plane that starts, serves, and CANNOT READ ITS OWN
AGENT CA — every enrolled gateway would be orphaned and would have to re-enroll.
Restore the master key that belongs to this backup, then retry. The key is never contained in
the backup; it is the separate artifact you were asked to custody.
exit=2
```

| criterion | verdict |
|---|---|
| exit **2**, distinct from exit 1 (couldn't evaluate) | ✅ |
| **both** fingerprints named | ✅ `2dc7a85e04a7` vs `912f6a205877` |
| names **AGENT CA** and **orphaned** | ✅ verbatim, plus the re-enrol consequence and the custody reminder |
| fleet unmutated afterwards | ✅ four cert serials unchanged |

The refusal states the **consequence**, not merely the mismatch. That is the property that makes it effective:
an operator mid-incident who reads "starts, serves, and CANNOT READ ITS OWN AGENT CA" does not reach for a
`--force`.

**A suspicion disproved, in the good direction.** The first (invalid) attempt suggested `backupctl` might share
the server's bootstrap path, which would have meant `verify` runs migrations on the way to refusing — violating
the read-only contract that makes "run it before you touch anything" safe advice. The valid run emitted **no
`schema_migrated` line**. `verify` is genuinely read-only.

### Two walk-procedure errors in this leg, both mine

Recorded because the runsheet is an artifact under test and a procedure that needs three attempts is a
procedure with a defect.

1. **Missing `--entrypoint`.** `api.Dockerfile` sets `ENTRYPOINT ["/usr/local/bin/tunnex-api"]`, so
   `docker run … tunnex-api /usr/local/bin/backupctl verify` ran *the server* with an ignored argv. It booted,
   migrated, and died on its own wrong-key guard — an exit 1 that looks like a refusal but is a different one.
   Every other leg uses `docker compose exec`, which does not apply `ENTRYPOINT`; only this `docker run` needed
   the override.
2. **Missing `-i`.** Without it the container's stdin is closed and the redirect feeds the docker *client*:
   `read manifest: EOF`, exit 1.

Both folded into `docs/S11-boxwalk.md` with the reason inline, so the next reader does not re-derive them.

### An unplanned proof, from error 1

The invalid run produced evidence worth keeping — the **control plane refusing to start under a wrong master
key**:

```
"msg":"agent_ca_failed","error":"agent CA exists but is unusable; refusing to regenerate
 (a new CA would orphan every enrolled agent): decrypt CA key: cipher: message authentication failed"
```

`self-host.md` §2 claims "a mis-set or malformed key fails startup loudly, and Tunnex never regenerates."
Proven on the wire, by accident, at the server tier rather than the tool tier — a second independent guard on
the same catastrophe.

**Observation from it, registered:** the server ran migrations *before* validating the master key
(`schema_migrated version 53` precedes `agent_ca_failed`). Harmless as observed — the schema was already at 53
and migrations carry no key material — but the ordering means a wrong-key boot touches the schema before
discovering it cannot work. Key-first would be the better order.

---

## Leg 5 — HA UNDER A ROLL — **PASS, all four. Owed debt #2 DISCHARGED.**

Second replica `tunnex-api-2`: same network, `--volumes-from tunnex-api-1`, env inherited from the running
container. **Admission gate:** both replicas logged `master_key_fp: 85bfff0a3a64` and
`session_secret_fp: d4f81511cfe9` — identical, so this is one deployment with two replicas rather than two
deployments. api-2 also logged `agent_ca_ready ca_fp: b394684347d8`, reading the CA out of sealed data.

> Note on the gate: I predicted api-2 would log `912f6a205877`, the *manifest's* fingerprint. It does not, and
> the reason is correct design — `backup.KeyFingerprint` and `crypto.Sealer.Fingerprint` HMAC different
> domain-separation probes, so a backup identity cannot be confused with a runtime identity. The gate held
> because it compared api-1 against api-2 rather than against a remembered constant.

```
03:49:33   stop the leader (graceful; container exits at ~03:49:43)
03:49:44   api-1=DOWN   api-2=ok follower      <-- NO LEADER AT ALL. Captured.
03:49:45.481  api-2  leader_acquired            <-- ~2.5s after api-1 exited
03:49:46 … 03:50:13   api-1=DOWN   api-2=ok leader      (15 samples)
03:50:28 … 03:50:35   api-1=ok follower   api-2=ok leader
```

| criterion | verdict |
|---|---|
| 1. leadership moved | ✅ api-2 acquired ~2.5 s after the leader exited, inside the documented ~10 s |
| 2. never two leaders | ✅ 15 paired samples plus the handover instant |
| 3. returning replica is a **follower** | ✅ `ok follower` — the advisory lock is genuinely exclusive |
| 4. no data-path loss | ✅ `s11-witness2.log`: 133 packets `09:19:14`→`09:21:27`, **zero gaps, zero timeouts**, roll window `seq 19–78` present at unchanged RTT |

**The `03:49:44` sample is the most valuable observation in this leg** and it was nearly missed: api-1 down,
api-2 still `follower` — a real instant with **no leader at all**. That is the failure direction the mechanism
was chosen for. A session-scoped Postgres advisory lock fails toward *nobody leads*, never toward two, and here
it is on a wire instead of in a design note.

Takeover at 2.5 s rather than 10 s is explained rather than celebrated: api-2's retry ticker had been running
since `03:43:55`, so its next tick fell 2.5 s after the lock freed. Latency is uniform in [0, 10 s]; this
sampled the lucky end, so **~10 s remains the honest documented number.**

### The first attempt was INVALID, for three independent reasons — all mine

Recorded in full because the runsheet is an artifact under test, and because reason 1 is a procedure that
produces a **false pass**.

1. **`docker compose restart` cannot prove takeover.** api-1 released the lock at `03:45:32.160` and
   **re-acquired it at `03:45:32.588` — 428 ms later**, long before api-2's 10 s retry tick. api-2 never got a
   `leader_acquired` at all. Worse, my stated criterion ("the surviving replica now reports `ok leader`") is
   *satisfied* by the restarted leader itself, so a careless reading records a pass for a leg that proved
   nothing. Fixed in the runsheet: **stop the leader and leave it stopped**, and require the returning replica
   to come back a follower.
   *What it did prove:* `leader_released` fired, so the explicit unlock on graceful shutdown works — the lock is
   handed back deliberately, not left for Postgres to reap.
2. **The poll window missed the event by 51 seconds.** Backgrounding the restart and pasting the loop separately
   meant observation began at `03:46:23` for a transition at `03:45:32`. Fixed: the stop and the poll go in one
   block.
3. **The witness flow was dead.** `s11-witness.log` ended at `09:06:34` local — nine minutes *before* the roll.
   Its gap check returned clean, which is a clean bill of health for a log that does not cover the leg: exactly
   the shape of evidence rejected at Leg 3, and it had to be rejected here too. Fixed: restart the witness and
   verify replies *before* the leg, then grep the window explicitly rather than trusting a gap count.

### Bonus proof from cleanup: hard-kill recovery

`docker rm -f tunnex-api-2` is SIGKILL — no graceful unlock, no `leader_released` from api-2. The sole survivor
read `ok follower` immediately afterwards (a deployment with **no leader**), then acquired at `03:51:46.873`,
within one retry tick. See **WF-S11-4**: this is *faster* than the documented expectation, and the docs conflate
two failure modes with very different speeds.

---

## Leg 6 — THE UPGRADE PROCEDURE — **criteria 1–3 PASS; criterion 4 FORKED**

Provenance first: the build log shows `[build 7/7] … -o /out/preflight ./cmd/preflight && … backupctl` and
three `COPY … /usr/local/bin/` lines, so WF-S11-1's fix is in the rolled image.

| criterion | evidence | verdict |
|---|---|---|
| 1. preflight **refuses**, refuse-don't-warn observed | exit 1, naming the unconfirmed rollback plan | ✅ |
| 2. confirmed run passes | exit 0, all four checks `ok` | ✅ |
| 3. migrate clean and idempotent | `migrate_up_complete version 53 dirty:false`, no new migrations | ✅ |
| 4. CP rolls and self-recovers | `ok leader` after `up -d --build api` | ✅ |
| 5. gateway reconciles with **no action, no re-enrolment** | `k8s` serial `5308c954…` unchanged; `policy_reported_at` advanced `03:54:36 → 03:57:36` | ✅ |
| **6. an N-1 agent still healthy after the roll** | **no live N-1 agent exists** | ⛔ forked |

Criterion 5 is the substantive half: the agent reported *again, after* the roll, on the same certificate, with
nobody touching it.

**Stated limit, so the leg is not read as more than it is.** The image was already at the census sha, so this
roll carries **no version delta**. It proves the *procedure* — preflight's refuse-then-pass direction, migration
idempotence, and that gateways survive a CP roll untouched. It does **not** prove an N→N+1 upgrade.

### Criterion 6 — the fork

| node | max_ver | last reported | state |
|---|---|---|---|
| **k8s** (the only live gateway) | **7** = N | `03:57:36` | live, at N |
| aws-gw-1 | 6 = N-1 | `2026-07-25 06:13` | off, 5 days |
| azure-gw | 6 = N-1 | `2026-07-25 06:13` | off, 5 days |
| aws-gw-2 | 6 = N-1 | `2026-07-25 06:12` | off, 5 days |

Three agents sit at exactly N-1 and all three are dead; the only breathing gateway is at N. Options, surfaced
rather than chosen:

- **A — power on one AWS gateway.** One console action buys three proofs: a live N-1 agent across the roll, site
  links recovering on the wire, and a health-kind transition off `site_link_down` — which would exercise a gauge
  that has reported exactly one kind for the whole walk. Recommended if those instances are cheap to start.
- **B — named substitute.** `TestNMinusOneAgentsCanStillApply` + `TestNewContentRaisesRequiredVersion` cover the
  mechanism; wire proof deferred on a named trigger. Honest, but leaves the contract that makes rolling upgrades
  possible proven only in unit tests — the weaker side of SUBSTITUTES ≠ SATISFIES.
- **C — build an N-1 agent image** from an older commit. Heaviest, and proves the contract against a synthetic
  agent rather than a real one.

This also made **WF-S11-2 concrete rather than hypothetical**: preflight said "all 4 gateway(s) at v6 or newer"
about a fleet where three of four last spoke five days ago.

---

## WF-S11-5 — `preflight` prints its verdict above the evidence

**Severity: LOW.** Observed at Leg 6 step 1:

```
REFUSING: 1 check(s) failed. Nothing was changed.      <-- the conclusion
Tunnex upgrade preflight
  [ok  ] database reachable    connected                <-- the evidence it summarizes
  [FAIL] rollback plan         unconfirmed. ...
```

The check table is written to **stdout** and the refusal to **stderr**; unbuffered interleaving puts the
conclusion first. The confirmed run (exit 0, entirely stdout) printed in the right order, which confirms the
cause rather than leaving it a guess. An operator meets `REFUSING` with nothing above it explaining why.

Fix: flush stdout before writing the stderr summary.

---

## WF-S11-4 — the docs conflate process death with network partition

**Severity: LOW (documentation precision).** `self-host.md` §6 and `upgrade.md` both state that after a *hard*
leader failure, takeover "waits for Postgres to notice the dead session — potentially minutes." Measured above:
a SIGKILLed replica was reaped and leadership reclaimed **within one 10 s tick**.

The two cases differ materially and the wording merges them:

| failure | what Postgres sees | recovery | status |
|---|---|---|---|
| process / container death | socket **closes**, backend reaped at once | ≤10 s | ✅ proven (`03:51:46.873`) |
| true network partition | socket stays open, waits out TCP keepalive | minutes | ⚠️ not tested |

Process death is the common case and it is fast; current wording tells operators to expect minutes for it.
Recommend folding the distinction and attaching "minutes" only to the partition case — marked untested, because
it is.

---

## WF-S11-3 — `backupctl` stdin failure does not name the expected invocation

**Severity: LOW.** `backupctl verify` with no stdin prints `read manifest: EOF` and exits 1. Honest and
useless: it names the operation and the condition but not the fix. It cost a walk cycle and would cost an
operator one, in the middle of a restore, which is the worst possible moment to be guessing at argument syntax.

Suggested: `no manifest on stdin — pipe one in:  backupctl verify < backup.manifest.json`.

---

## WF-S11-2 — `preflight` reports last-known agent versions as though they were live

**Severity: LOW.** Found at Leg 0's re-verify. **Held for disposition.**

`preflight` printed *"all 4 gateway(s) at v6 or newer"* about a fleet the health surface simultaneously reported
as 4× `site_link_down`, and which Leg 1's `wg show` confirmed is two-thirds powered off. Both statements are
true; together they read as a contradiction.

`agentCompatWindow` reads persisted `nodes.capabilities->>'max_policy_version'` — last-known, not live. Three of
those gateways went down on Jul 23; their v6 is a memory. **The logic is right**: staleness is conservative for
this check's purpose, since a dead agent cannot have been silently downgraded, so last-known is a safe floor.
The *wording* is not — "all 4 gateway(s) at v6 or newer" reads as a liveness claim the check never made, and an
operator about to roll would take it as "the fleet is fine."

Two candidate shapes, and one is a design change rather than a fix:

- **(a) wording only** — "all 4 gateway(s) **last reported** v6 or newer", naming the read as last-known.
- **(b) staleness as its own verdict** — count gateways whose report is stale and return them as
  unknown-and-refuse, consistent with the check's existing unknown-≠-pass stance.

Recommendation: **(a) now, (b) registered.** (b) would make `preflight` refuse rolls on any deployment with a
legitimately dormant site, which is a policy decision about what an upgrade gate should block.

---

## Resolved at Leg 1

- ~~`tunnex_gateway_policy_health{kind="healthy"} 0` with four non-revoked gateways — broken gauge or honest
  state?~~ **Honest state.** The kinds sum to exactly 4, reconciling against the `revoked_at IS NULL` row count,
  and `site_link_down` = 4 is corroborated on the wire by both site-link peers showing sent-but-never-received.
  No defect.

---

## Dispositions (founder, in session) and the folds

| # | sev | disposition | fold |
|---|---|---|---|
| WF-S11-1 | HIGH | FOLD mid-walk | `5e0bac7` — ship all three binaries + `TestEveryOperatorToolShipsInTheImage` + both runbooks corrected + the pre-dated claim reworded |
| WF-S11-2 | LOW | **FOLD** — "show the age alongside the version" | `agentCompatWindow` reads `policy_reported_at`; `since()` renders an age or names its absence; a stale-but-in-window fleet **passes** with the distinction stated. Option (b), refuse-on-stale, remains registered and unbuilt |
| WF-S11-4 | LOW | **FOLD** — "a misstated limit is worse than an omitted one" | `self-host.md` + `upgrade.md` split into two limits: stop/crash/`kill -9`/container-removal ≤10 s **verified**; network partition minutes **marked not verified** |
| WF-S11-5 | LOW | **FOLD** (trivial) | preflight's verdict moved to **stdout** — one stream, one guaranteed order; the exit code remains the machine-readable signal |
| WF-S11-3 | LOW | **FOLD** — teaching-text convention | the stdin error now names the fix, with both the bare and the in-container invocation |
| criterion 6 | — | **RULED: Option A** | power on one AWS gateway — a live N-1 agent across a roll, site links recovering, and a health-kind transition, for one console action |

**Red for the WF-S11-2 fold, proven to reject** (`TestSinceNamesTheAgeOrItsAbsence`): with `since(nil)` returning
`"0s ago"` instead of `"never reported"` —

```
main_test.go:17: a nil report time must be named, not rendered as an age: got "0s ago"
```

Clean before, rejects, clean after. That is the finding's own shape as a red: the defect was never a wrong
number, it was a confident rendering of absent data.

### Two conventions the walk produced

- **A witness must prove it was alive across the window it certifies** — `docs/laws.md`, plus a standing rule in
  the runsheet's "two bars" section. A dead ping log returned a clean gap check for a window it never observed;
  the check could not have failed, so its pass carried no information. PROVE-A-GUARD-REJECTS applied to the
  instrument rather than the subject.
- **A procedure that can false-pass is a defect in the procedure**, not merely in its execution. Leg 5's original
  step let the leader reclaim its own lock in ~400 ms while satisfying the stated criterion.

### The finding worth remembering

WF-S11-1's embarrassing half: `backup-restore.md` claimed trust-after-restore "is verified on real hardware in
the EPIC 11 box-walk" — written in Slice 4, about a leg that had not run. It became true hours later, which is
not the same as being true when written. The ✅/🔶/⚠️ marks invented **one slice later** in `self-host.md` exist
for exactly this, and the unmarked forward-claim sat in a neighbouring file the whole time. A new honesty
convention needs a census of the surface it covers, exactly like a new guard does.

---

## WF-S11-6 — a gateway offline longer than `CertTTL` can NEVER rejoin (cert-renewal deadlock)

**Severity: HIGH. Found while staging criterion 6; it BLOCKS criterion 6.** Confirmed on the wire and in code.

### Mechanism

- `agentca.CertTTL = 48 * time.Hour` (S3.1: short lifetime bounds a compromised cert's window).
- The agent renews at **half-life** via `POST /agent/renew` — **over the mTLS channel**.
- That listener is `ClientAuth: tls.RequireAndVerifyClientCert` for **every** route, `/agent/renew` included
  (`agentchannel.go:55`, `:68`).

So the expired certificate is rejected **during the TLS handshake**, before any handler runs. The only endpoint
that can issue a new certificate requires the certificate that expired. There is no recovery path.

`control/client.go:117` states the assumption in a comment: *"Renewing at half-life keeps the agent from ever
reaching cert expiry."* That holds for a **continuously running** agent and silently fails for any other kind.

### Evidence

`aws-gw-1` (powered off 2026-07-25, restarted 2026-07-30 04:08 — cert expired 2026-07-27):

```
"msg":"reconcile_interval_failed","error":"Get \"https://104.45.208.156:8443/agent/desired-state\":
   remote error: tls: expired certificate"
"msg":"agent_report_key_failed",   "error":"Post \".../agent/report\": remote error: tls: expired certificate"
"msg":"agent_stats_read_failed",   "error":"wg show wg0 dump: ... Unable to access interface: No such device"
```

`remote error:` = the **server** sent the alert, so this is the CP rejecting the client cert, not a local clock
or trust problem. No `wg0` in `ip -brief addr show`: having never received desired-state, the agent never built
the interface. It looped for five minutes with no path out and would loop forever.

(The two interleaved `connection refused` lines are the CP's api container restarting during a rebuild —
unrelated, and they resolve back to the same TLS alert.)

### Why this is a beta blocker rather than a curiosity

EPIC 11's acceptance question is *"what breaks when a stranger runs this in production, unattended, for a
month?"* This answers it directly: **any gateway unreachable for more than 48 hours is permanently bricked
until a human re-enrolls it.** A site down over a long weekend, a VM stopped to save money, an outage, an RMA,
a scheduled maintenance window longer than two days. The recovery — re-enrolment — is documented in
`self-host.md` only as what to do for a *lost* gateway, so an operator has no reason to connect it to "my
gateway was switched off."

### The second half: the CP cannot see it

From the control plane's side this node is simply **not reporting**. The gauge reads `site_link_down`; there is
**no health kind for "this agent's certificate expired and it cannot reconnect."** An operator sees a stale
gateway, not a bricked one, and those require entirely different actions. `preflight` counts it *inside* the
version window on last-known data — correctly, per the WF-S11-2 fold, and it is exactly the case where being
"not confirmed live" understates the situation.

That is an observability gap in the epic that just built the observability floor, and it is the
who-reads-this probe inverted: not a channel field with no consumer, but a **real failure state with no
rendering at all**.

### Halted for disposition — this is a design fork, not a fold

Options, with the security argument each carries:

- **(a) Longer TTL.** Moves the cliff, does not remove it, and directly weakens the S3.1 bound. Rejected on
  sight.
- **(b) Grace on `/agent/renew` only** — accept a cert that is expired but otherwise valid (correct CA, node not
  revoked, within a bounded grace, e.g. 30 days) for that one endpoint. Small change. Partially relaxes the
  compromise window S3.1 chose 48h to bound — but only for renewal, and revocation still cuts it dead.
- **(c) A durable enrolment credential**, separate from the mTLS cert: the 48h cert keeps guarding the data
  channel while a long-lived secret survives downtime and is used only to re-obtain a cert. Revoking a node
  kills both. Cleanest security story, largest change, and how fleets normally solve this.
- **(d) Accept and surface** — document the limit honestly, add a health kind for cert-expired-cannot-reconnect,
  and make the agent's own error name the remedy instead of looping on a bare TLS alert.

(d) is not an alternative to (b)/(c); it is required regardless, because today the failure is invisible to the
operator and unactionable to the agent.

### Immediate consequence for criterion 6

The N-1 witness cannot connect, so the N/N-1 contract cannot be proven until `aws-gw-1` is re-enrolled.
Re-enrolment is a hand-run step, and it is recorded as **forced by WF-S11-6** — not as a defect in criterion 6's
own procedure.

---

## Criterion 6 — the re-enrollment, and what WF-S11-6 costs an operator

Recorded because this is the finding's operational price, and it is what a customer would face. `aws-gw-1` had
to be **re-enrolled by hand** before the N/N-1 contract could be tested at all — not because criterion 6's
procedure is defective, but because the gateway was unreachable.

```bash
# aws-gw-1 — the agent REFUSES to enroll over a stored identity (by design; that warning had been in its log
# for five days). Recovery therefore means destroying the identity, which means knowing the volume's name.
sudo docker rm -f tunnex-node
sudo docker volume ls | grep -i tunnex        # -> tunnex_node_state
sudo docker volume rm tunnex_node_state
# then: mint a join token in the UI (Sites -> the site -> enroll a gateway) and run the printed command
```

**Four hand-run steps, a UI visit, and a destroyed volume — to recover from having switched a machine off.** No
alert fired; nothing told the operator this was needed. That is the whole case for ruling (c) beta-blocking
rather than merely registered: the mechanism works, and the experience is one a customer would reasonably call
a fault.

The volume name was confirmed with `docker volume ls` before deletion rather than assumed from a naming
convention. On a live rig, deleting the wrong volume is an expensive way to learn a name.

### Ruling (i) — the backfill, and the trap it avoids

0054 made `cert_not_after` nullable, which was right (a new column must not retroactively brick every gateway).
But a **running** agent renews within 24h and stamps a real value, while an **already-dormant** agent never
renews and stays NULL forever — and dormant agents are exactly the ones that go bricked. The new kind was
therefore **unfirable for precisely the population it was built to name**, on every deployment in existence.
Dormant machinery in its purest form, and caught before shipping rather than when a customer's bricked gateway
displayed "unknown".

Migration **0055** (a new migration, not an edit to 0054 — azure-cp had already applied 54, and editing an
applied migration is how a deployment breaks) backfills `last_seen_at + 48h` where `cert_not_after IS NULL AND
last_seen_at IS NOT NULL`.

**The value is a bound, not a measurement**, and the direction is the entire argument: the certificate was
necessarily valid at the last report, so `issuance <= last_seen_at`, so
`true_expiry = issuance + 48h <= last_seen_at + 48h = the bound`. A bound already in the past therefore proves a
real expiry even further in the past — **the kind can never false-positive.** It can be up to 48h late, which is
the safe direction to be wrong in.

Three conditions, all red-enforced by `TestBackfill0055CannotFalsePositive`, which reads the migration's actual
SQL rather than a description of it:

| condition | enforcement |
|---|---|
| the value is stated as a bound, not a measurement | in the migration comment and the column `COMMENT` |
| `last_seen_at IS NULL` stays NULL | red: dropping the clause fails, naming it as the false positive the ruling avoids |
| a real stamp overwrites the bound cleanly | red on `queries/nodes.sql`: both `CreateNode` and `RenewNodeCert` must set the column unconditionally |

**PROVE-A-GUARD-REJECTS, twice, at both plausible mistakes** — and they fail in opposite directions, which is
why the red names both:

```
backfill0055_test.go:34: the backfill MUST exclude rows with last_seen_at IS NULL — ...
backfill0055_test.go:50: the bound MUST be last_seen_at + CertTTL. ...
```

- `now() + TTL` is always in the future, so the kind **never fires** — a silent false negative.
- `created_at + TTL` predates every renewal, so it can fire on a gateway whose cert is **still valid** — a false
  positive.

Only `last_seen_at` bounds it correctly. A first draft of that red's message claimed the substitution "can
false-positive" for both cases, which was true of one and wrong about the other; corrected, because a test
message is read at 3am by someone deciding whether to trust it.

---

## WF-S11-8 — a gateway can NEVER be re-enrolled under its own name

**Severity: HIGH. Found while executing WF-S11-6's documented remedy. HALTED for disposition.**

### Evidence

```
tunnex=# \d nodes
Indexes:
    "nodes_org_id_name_key" UNIQUE CONSTRAINT, btree (org_id, name)
Check constraints:
    "nodes_status_check" CHECK (status = ANY (ARRAY['active'::text, 'revoked'::text]))
```

The uniqueness on `(org_id, name)` is **unconditional** — not partial on `revoked_at IS NULL`. The only lifecycle
operation in the API surface is `POST /nodes/{id}/revoke`, which sets `status='revoked'`; there is **no delete**
(`grep DeleteNode` finds nothing in `db/queries/nodes.sql`). So a revoked row retains its name permanently and
`CreateNode` returns `409 node_exists` (`service.go:277`).

### Why this is broader than WF-S11-6

WF-S11-6's remedy is "re-enroll the gateway", and this makes that impossible under the original name. But the
blast radius is much wider, because **nothing about it is specific to certificate expiry.** `self-host.md` §7
states:

> **A lost gateway**: re-enrol it (one pasted command). It generates a fresh key; the CP re-issues its
> certificate. Nothing needs restoring.

A destroyed VM, a failed disk, a hardware RMA, a mistakenly-deleted state volume — every one of those follows
that sentence to a 409. **The documented recovery story for the most ordinary failure in the product does not
work as written**, and it has presumably never worked, because recovering a gateway under its own name has never
been exercised.

Consequences of the only available workaround (enroll under a different name):

- the site binding is orphaned — the new node is not the old node, so it must be re-bound;
- every dashboard, alert, runbook and saved query referencing the old name silently refers to a dead row;
- the name is consumed permanently, so each failure of a given gateway burns a name (`aws-gw-1`, `aws-gw-1b`,
  `aws-gw-1c`);
- the revoked row lingers with its cert serial, pool address and telemetry history, indistinguishable in a name
  search from the live gateway.

### The pattern this completes

Three of this epic's findings are now the same shape, and it is worth naming: **a mechanism that works, a
procedure around it that does not, and documentation asserting the procedure.**

| finding | mechanism | procedure | doc |
|---|---|---|---|
| WF-S11-1 | `preflight`/`backupctl` compile and pass their units | not shipped in the image | two runbooks named them |
| WF-S11-6 | short-lived certs bound compromise, renewal works | renewal requires the cert that expired | `self-host.md` implied recovery was automatic |
| WF-S11-8 | enrollment works; revocation works | re-enrolment under the same name is impossible | "re-enrol it (one pasted command)" |

None of the three is a bug in a function. All three are gaps between components that each work correctly, and
**not one was findable by a unit test** — which is the argument for walking a story that this epic has now made
three times.

### Options (surfaced, not chosen)

- **(a) Make the uniqueness partial** — `UNIQUE (org_id, name) WHERE revoked_at IS NULL`. Smallest change; a
  revoked gateway frees its name and re-enrolment works as documented. Question it raises: two rows with the same
  name in history, so audit and telemetry queries must disambiguate by id rather than name (they mostly already
  do — this needs a census, not an assumption).
- **(b) A replace-in-place enrolment** — a join token pinned to an EXISTING node re-keys that row rather than
  inserting: same id, same site binding, same history, new cert serial. Best operator experience by far, since
  the gateway keeps its identity across a rebuild. Largest change, and it needs care that "re-key" cannot be
  used as a credential-swap attack on a live node.
- **(c) A delete endpoint** — explicit, destructive, frees the name. Honest but crude: it discards the audit
  trail of a gateway that existed, which the revoked row exists to preserve.
- **(d) Document the rename** — accept the limitation and tell operators to expect a new name. Cheapest, and it
  makes the product's ordinary failure recovery permanently worse than its docs currently promise.

Recommendation: **(b) as the goal, (a) as the near-term unblock**, both under the same story as WF-S11-6's (c) —
they are one subject, "how does a gateway come back", and solving them separately would produce two half-answers.

### Consequence for criterion 6, today

The N-1 witness must be enrolled under a **new name** to proceed. Recorded as forced by WF-S11-8, not as a
choice.

---

## WF-S11-9 — gateway revoke exists in the API and never existed in the UI

**Severity: HIGH. Found while executing WF-S11-8(a)'s unblock — it was the wall standing directly in front of
it.** Folded on the founder's direct instruction.

### Evidence

`POST /api/v1/organizations/{orgId}/nodes/{nodeId}/revoke` has existed since S3 (`revokeNode` in the spec,
`RevokeNode` in `queries/nodes.sql`, called at `service.go:2068`). The **Devices** list renders a `Revoke` button
per row. The **Gateways** list, immediately above it on the same page, rendered **no action at all** — name,
version, health badge, last-seen, and nothing else.

So the documented gateway-recovery path — revoke, then re-enrol — was **unreachable from the product**. Not
merely inconvenient: revocation is the mechanism the entire security model rests on (short-lived certs plus a
refused renewal), so a revoke an operator cannot reach is worse than a missing convenience. And it is why
WF-S11-8(a) could not be exercised the moment it shipped: (a) frees a revoked gateway's name, and there was no
way to revoke a gateway.

### The fourth instance of the epic's pattern

| finding | mechanism | procedure | doc |
|---|---|---|---|
| WF-S11-1 | tools compile, units pass | not shipped in the image | two runbooks named them |
| WF-S11-6 | short certs + renewal work | renewal needs the cert that expired | docs implied auto-recovery |
| WF-S11-8 | enroll works, revoke works | re-enrol under the same name impossible | "one pasted command" |
| **WF-S11-9** | **revoke endpoint works** | **no way to call it** | recovery documented as routine |

Four for four: a mechanism that works, a procedure around it that does not, documentation asserting the
procedure. Also the **fourth producer-without-consumer of the epic** (after WF-S11-7's unrendered health kind),
which makes the standing who-reads-this probe due for a stronger form — see below.

### The fold

`apps/web/src/components/Gateways.tsx` gains a per-row revoke with a **two-step confirm**, mirroring the
`MfaSettings` disable ceremony rather than a native `window.confirm`, because the consequence needs stating in
the UI and a native dialog cannot say it:

> Revoke *name*? Devices homed here lose their tunnel. This cannot be undone.

Two-step is not ceremony for its own sake. Revoking a gateway is *wider than it looks*: it refuses that agent's
cert renewal, so every device homed there loses its tunnel and any site transit through it stops. A one-click
danger button beside a "last seen" label is one misclick from an outage.

`loadNodes` was hoisted in `pages/Devices.tsx` and passed down as `onNodesChanged`, because the parent owns the
`nodes` array — a child mutation that did not propagate would leave a revoked gateway rendering as active until
a manual reload, which is the stale-render class this project has already fixed twice.

No RBAC gate was added locally: the page has none today and the device revoke beside it relies on the
established reactive-403 cap. Adding one here alone would be an inconsistency pretending to be a hardening.

### Stated gap — this fold has NO automated test

This repository has **no component-test tier**: all nine web test files cover pure view-models (`*view.ts`), and
there is no `@testing-library` anywhere. Introducing that infrastructure inside a walk fold is out of scope, so
the honest position is that the confirm interaction is proven **only by being exercised in the next walk step**,
not by a test.

Registered as an observation rather than smoothed over: **the missing component-test tier is now a recurring
gap** — this is the second UI fold this session (after the S10.2 machine-credential panel) whose behaviour no
test can express. It belongs in the ledger.

### What "delete" would need — NOT shipped

The instruction mentioned deleting a gateway. There is **no delete endpoint** and none was added: `revoke` marks
`status='revoked'` and deliberately preserves the row's audit trail, cert serial and telemetry history. A true
delete is WF-S11-8 option (c), which was *not* ruled — and with (a) shipped, the name is already freed, which was
the only practical reason to want one. If a delete is wanted for tidiness rather than recovery, that is a
separate decision about discarding audit history.

---

## WF-S11-10 — a revoked gateway was told to re-enroll because its certificate expired

**Severity: MEDIUM. My own fold-induced defect, seen on the live dashboard one commit after shipping the kind.**

```
aws-gw-1  0.1.0  revoked  certificate expired — re-enroll this gateway
```

Two labels contradicting each other, and the instructional one is wrong: **refusing a revoked agent's renewal IS
the revocation mechanism**, so a revoked gateway's expired certificate is the system working exactly as designed.
Telling the operator to re-enroll it is a confident instruction to **undo a deliberate security action**.

**Authorship, precisely.** The latent inconsistency is pre-existing: `Gateways.tsx` has never suppressed health
badges for revoked rows the way `Devices.tsx` always has (`d.status !== "revoked" && …`). But that stayed
invisible while the badges were *vague* — a revoked gateway reading "degraded" is uninformative, not wrong. My
kind turned vague into **actionable and wrong**, which is what made a five-story-old inconsistency visible.

### Fixed at both tiers, for different reasons

- **CP-side** — `CertExpiredForNode` gates on `status == "active"`. This also restores agreement with the fleet
  metric, whose query already filters revoked rows: one truth, two renderings.
- **UI-side** — no health badge on a revoked gateway row, matching the `Devices.tsx` precedent. `revoked` **is**
  the state; a degradation badge beside it describes a gateway that is no longer meant to work.

### The red I wrote first could not fail — the tautological-guard law, caught in the act

The first attempt asserted `degradedKind(KindInput{CertExpired: false})` does not return the cert-expired kind.
That is a **tautology**. Verified by removing the production fix and re-running: **everything passed.**

```
=== with the fix REMOVED, does anything fail? ===
ok    github.com/tunnexio/tunnex/apps/api/internal/nodes    0.634s
```

The decision under test was never the projection — it was *which node rows count as expired at all*, and that
lived in an untestable inline expression at the call site. Extracted to `CertExpiredForNode(status, notAfter,
known, now)` and exercised directly, with both gates proven to reject independently:

```
REVOKED + expired: got true, want false — refusing a revoked agent's renewal IS revocation working;
  prescribing re-enrolment would undo a deliberate security action
active + expiry UNKNOWN: got true, want false — 0054 added the column nullable so it could not
  retroactively brick every enrolled gateway
```

**This is the second check this session that could not fail** — after the dead witness log whose gap detector
returned clean for a window it never observed. Both were caught by asking "could this have failed?" rather than
"did it pass". The pattern is worth stating: *a check written at the same moment as the fix tends to encode the
author's belief about the fix rather than the behaviour of the system.* Removing the fix and watching the check
is the only way to tell the difference, and it costs one minute.

Gates after the fold: web typecheck + 183 tests + build green; `test-editions` 0 FAIL / 67 packages, both editions.
