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

**Observation, under test, not a finding yet:** two packets returned in exactly 2× baseline (`seq=892` at
`09:06:07`, `seq=917` at `09:06:32`) — 25 s apart, matching the peers' `persistent keepalive: every 25 seconds`.
No loss either time. n=2 is suggestive, not conclusive; periodicity being checked across the whole log before
it is called jitter or a pattern.

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
