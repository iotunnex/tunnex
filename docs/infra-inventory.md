# Running infrastructure — reference inventory

The live rig every box-walk runs against. **Recorded 2026-07-31.** Addresses are the founder's; SSH access is
theirs, not the agent's — every command in a runsheet marked `[host]` is run by a human on that host.

Keep this current. A walk that guesses which box plays which role burns a 48-hour clock finding out.

## Hosts

| host | public | private | role |
|---|---|---|---|
| **azure-cp** | `104.45.208.156` | `10.0.0.4` | **control plane** — docker-compose stack (api, web, nginx, postgres, redis, mailpit). Runs migrations. Never a gateway |
| **azure-gw** | `52.190.140.51` | `10.0.0.5` | gateway. Same VNet as the CP, so device traffic to it stays inside Azure |
| **aws-gw-1** | `15.134.60.253` | `172.31.1.217` | gateway. **The box whose real-world certificate expiry started EPIC 13** |
| **aws-gw-2** | `15.135.130.96` | `172.31.9.62` | gateway |
| **aws-behind-host** | — | `172.31.10.85` | LAN host BEHIND aws-gw. No public address. Exists to prove site transit reaches a machine that is not itself a gateway — not a gateway, never enrolled |

**Cross-cloud:** the AWS boxes and the Azure boxes are in different clouds and different regions, which is what
makes the site-to-site and cross-site DNS walks real rather than a loopback demo (~138ms between them).

## Node rows vs hosts — not one-to-one

The control plane's `nodes` table has carried **four** live rows against **three** gateway VMs, because the
in-cluster Kubernetes gateway (S10.3) is its own node row served by an agent in the k3s cluster, not by a VM's
agent. **Before any walk that stops agents, confirm which host serves which node row** — stopping a VM's agent
does not stop a pod, and vice versa.

```bash
# [azure-cp] the authoritative mapping, always run before assigning walk roles
sudo docker exec tunnex-postgres-1 psql -U tunnex tunnex -c \
  "SELECT name, status, endpoint, last_seen_at, cert_not_after,
          cert_public_key IS NOT NULL AS key_recorded
   FROM nodes WHERE revoked_at IS NULL ORDER BY enrolled_at;"
```

## How the agent runs on a gateway differs by host

Two shapes exist in this fleet and the rebuild command is not the same:

- **compose-managed** (`docker compose up -d node-agent`) — the box has a `~/tunnex` checkout;
- **standalone `docker run`** — the single-line install command the UI emits (S8.2c), with the state directory as
  a named volume.

```bash
# [any gateway] which shape is this box?
sudo docker ps --format '{{.Names}}\t{{.Image}}' | grep -i node
ls -d ~/tunnex 2>/dev/null && echo "compose-managed" || echo "standalone docker run"
```

## azure-gw CANNOT host a second agent — k3s owns `wg0` there

**Found the hard way on 2026-07-31; do not rediscover it.**

`azure-gw` runs the node-agent **inside the k3s cluster**, serving the `k8s` node row — which is why that row's
endpoint is `52.190.140.51:51820`, azure-gw's own public address. The pod uses **host networking** and owns
`wg0` on the host.

**A second host-network agent on azure-gw would contend for the same interface.** So azure-gw is NOT a spare
gateway host: the box has one agent slot and k3s is in it.

Consequences for any walk needing N expired subjects:

- the fleet has **two** usable VM agent hosts (aws-gw-1, aws-gw-2), not three;
- the `k8s` row is a usable subject **only** via `kubectl`/Helm (scale to 0 to stop it, and the image must be
  imported into k3s's containerd with `k3s ctr images import`, not `docker load`);
- where a walk needs more subjects than hosts, **sequence roles on one host** rather than doubling up agents —
  EPIC 13 ran A then C on aws-gw-1, which preserved the reason for separate subjects (B's refusal cause must not
  be conflated with C's) at no cost.

## Walk-role assignment — EPIC 13 (`docs/S13-boxwalk.md`)

The walk needs **three gateways whose certificates have genuinely expired**, and the fleet has exactly three
gateway VMs, so there is no spare. Assignments are load-bearing: each subject must carry exactly ONE reason to be
refused, or the uniform-refusal surface makes the leg prove nothing.

| walk role | host | why this one | ends the walk as |
|---|---|---|---|
| **A** — recovers by PoP (Leg 1) | **aws-gw-1** | the box whose real expiry started the epic; recovering it in place is the epic's own story closing | recovered, same node id |
| **B** — keyless → token fallback (Leg 2) | **aws-gw-2** | ends as a **NEW node** (site binding lost, devices need re-issuing), so it goes on the least entangled box | re-enrolled as B′, and is Leg 4's restore TARGET |
| **C** — revoked → refused (Leg 3a) | **azure-gw** | hosts the Legs 4/5/6 devices; nearest the CP, so device traffic is simplest to stage | **revoked and dead** — re-enrol it after the walk |
| out of the walk | the **k8s** node row | keep one live node untouched as a control | unchanged |

**C is where the walk's devices live**, not A — Leg 4 restores a *revoked* gateway's devices, and C is the one
that gets revoked.

**aws-behind-host is not used by the EPIC 13 walk.** It is a site-transit subject, and gateway recovery does not
exercise transit.

## Standing hazards

- **`git pull` reporting "Already up to date" is not proof the rig has the code.** On 2026-07-31 the branch had
  never been pushed; the rig sat 24 commits and five migrations behind, and only the schema-version check caught
  it. **Record the sha AND the schema version, every time.**
- **The edition is half of provenance.** `make up-enterprise`, never `docker compose up -d --build api` — the
  latter silently rebuilds the OPEN image, visible only as `go build -tags ""` in a build log.
- **A running agent renews every 24h and will not expire.** Any walk needing an expired certificate must stop the
  agent, and **idle is not stopped**.
