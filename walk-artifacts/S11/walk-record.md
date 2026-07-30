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

## Open at Leg 0

- **`tunnex_gateway_policy_health{kind="healthy"} 0`** with four non-revoked gateways. Not yet diagnosed: the
  fleet may legitimately be in `desync_unknown` (three of the four are dormant cross-cloud walk hosts), or the
  gauge's org walk may be under-counting. Resolved before Leg 1 — a health floor that reads zero on a working
  fleet is either an honest state or a broken metric, and which one it is matters more than either leg after it.
