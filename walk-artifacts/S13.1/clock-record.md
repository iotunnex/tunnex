# EPIC 13 walk — CLOCK RECORD

**STATUS: NOT STARTED. This file contains the procedure and EMPTY evidence slots.**
Nothing below the evidence tables has been observed. Do not read a blank row as a pass — the clock has not
started, and until the tables are filled `docs/S13-boxwalk.md` cannot legally run at all.

Branch: `story/S13.1-gateway-recovery` — tip `7c1a127` (the prompt named `120ff0c`; the branch has since advanced
by two DOCS-ONLY commits, `3b40e94` PLAN/memory and `7c1a127` the reviewer brief. No product code differs.)

## Why a clock exists at all

`agentca.CertTTL = 48 * time.Hour` is a **constant** — verified, no environment override, no per-org setting. An
expired agent certificate therefore cannot be manufactured; it can only be waited for. Two facts govern the
staging:

1. **A RUNNING agent never expires.** `renewLoop` renews every 24h (`TUNNEX_AGENT_RENEW_INTERVAL`, default 24h),
   so a live gateway refreshes its certificate at half its lifetime, forever. The agents must be **stopped**, not
   merely idle.
2. **A RESTART DOES NOT RESET `not_after`.** The renew ticker has no immediate first tick, and `identity.Decide`
   takes `UseStored` when the stored certificate is valid — so rebuilding the agent image at this branch and
   restarting **keeps the existing identity and the existing expiry**. Rebuild does not cost a fresh 48 hours;
   only a *re-enrolment* does.

That second point is the difference between waiting ~24h and waiting a full 48h. Prefer restart-in-place unless a
host's identity is unusable.

## The gate

**Earliest legal walk time = the LATER of the two hosts' `cert_not_after`, plus a margin.**

Not "stop time + 48h" — that is the wrong quantity and is always too late. The certificate expires at its own
`not_after`, which was fixed when it was last issued or renewed, possibly long before the stop. A host renewed 20
hours ago expires in 28, not 48.

Both hosts must be past their own `not_after` before Legs 1–3 mean anything: Leg 1's subject must be genuinely
unable to authenticate, and so must Leg 2's and Leg 3a's.

---

## Step 1 — PROVENANCE CENSUS, before anything else

**Both halves, per host. The commit alone is half the story.** The S11 walk had four rebuilds silently swap the
OPEN build for the enterprise one; the census verified the sha and not the edition, and the mismatch was visible
only as `go build -tags ""` in a build log nobody was reading. It cost several legs. `docs/laws.md` records this
as *could this check have failed?* — a census that cannot detect the substitution it exists to prevent.

```bash
# [azure-cp] the control plane — must be THIS branch and MUST be enterprise
cd ~/tunnex && git fetch && git checkout story/S13.1-gateway-recovery && git pull
git rev-parse --short HEAD                          # record: CP sha
sudo make up-enterprise && sudo make migrate        # up-enterprise, NOT `compose up --build api`
curl -s localhost/api/v1/meta | grep -o '"edition":"[a-z]*"'   # record: MUST be "enterprise"
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -tAc "SELECT version, dirty FROM schema_migrations;"
                                                    # record: MUST be 61 / f
```

```bash
# [each gateway host] the agent image — sha AND edition, per host
sudo docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' <agent-image>
sudo docker inspect --format '{{index .Config.Labels "org.opencontainers.image.title"}}'    <agent-image>
# If the image carries no revision label, the sha is UNKNOWN — rebuild from a known tree rather than assuming.
# The agent has no edition tags (open-core gating is control-plane side); record "n/a (agent)" and assert the
# CP's edition instead. Recording "n/a" is the honest entry; leaving the column blank is not.
```

### Evidence — provenance

| host | role | image sha | image edition | CP sha | CP edition | schema version |
|---|---|---|---|---|---|---|
| _(azure-cp)_ | control plane | n/a | n/a | **PENDING** | **PENDING** | **PENDING** |
| _(host A)_ | PoP-recovery subject | **PENDING** | n/a (agent) | — | — | — |
| _(host B)_ | refusal / fallback subject | **PENDING** | n/a (agent) | — | — | — |

---

## Step 2 — bring both agents up and prove ENROLMENT SUCCEEDED

A running process is not an enrolled agent. The proof is a node row with a certificate, not a container that
started.

```bash
# [each gateway host]
sudo docker logs <agent-container> 2>&1 | grep -E 'agent_enrolled|agent_rekeyed|node_ready|agent_no_usable_identity|agent_unrecoverable'
```

```bash
# [azure-cp] the authoritative check — the CP's own record of each node
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, cert_serial, cert_not_after, last_seen_at,
          cert_public_key IS NOT NULL AS key_recorded,
          left(cert_key_fingerprint,12) AS fp
   FROM nodes WHERE revoked_at IS NULL ORDER BY enrolled_at;"
```

**`key_recorded` must be TRUE for host A.** Proof-of-possession recovery is impossible without it, and Leg 1 is
the leg the epic exists for. (Host B's is deliberately nulled *during* the walk, as declared staging in Leg 2 —
not now.)

### Evidence — identity at stop time

| host | node id | cert serial | `cert_not_after` (UTC) | `key_recorded` | fingerprint (12) |
|---|---|---|---|---|---|
| _(host A)_ | **PENDING** | **PENDING** | **PENDING** | **PENDING** | **PENDING** |
| _(host B)_ | **PENDING** | **PENDING** | **PENDING** | **PENDING** | **PENDING** |

---

## Step 3 — STOP both agents, and prove stopped

```bash
# [each gateway host]
sudo docker stop <agent-container>          # or: sudo systemctl stop tunnex-node
sudo docker ps --filter name=<agent-container>      # must list NOTHING
sudo docker ps -a --filter name=<agent-container> --format '{{.Status}}'   # must read "Exited (…)"
date -u +%FT%TZ                                     # record: stop timestamp
```

Idle is not stopped. An agent that is up but not reconciling still runs `renewLoop`, and a single renew resets
`not_after` and silently costs another 48 hours — discovered, if at all, on walk day.

### Evidence — stop

| host | stopped at (UTC) | `docker ps` empty | exit status |
|---|---|---|---|
| _(host A)_ | **PENDING** | **PENDING** | **PENDING** |
| _(host B)_ | **PENDING** | **PENDING** | **PENDING** |

---

## Step 4 — the gate

```
EARLIEST LEGAL WALK TIME  =  max(host A cert_not_after, host B cert_not_after) + 15 min margin
```

**PENDING** — cannot be computed until the `cert_not_after` values above are recorded.

Verification on walk day, before Leg 1 (cheap, and the whole walk rests on it):

```bash
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, cert_not_after, cert_not_after < now() AS expired FROM nodes WHERE revoked_at IS NULL;"
```

Both subjects must read `expired = t`. If either reads `f`, the walk **does not start** — Legs 1, 2 and 3a would
all be exercising a live gateway and would prove nothing about recovery.
