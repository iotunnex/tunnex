# Deploy Tunnex to a public VM (single-host quickstart)

One VM runs everything: API, web, nginx, Postgres, Redis, and the `tunnex-node`
WireGuard gateway. A client anywhere on the internet signs in, gets a device
config, and connects over the WireGuard subnet.

> **Split-tunnel only (read this first).** The gateway routes traffic to the
> WireGuard subnet (the pool CIDR, default `10.99.0.0/24`) — clients reach the
> gateway and each other. It does **NOT** yet route all client internet traffic:
> `--full-tunnel` / "route everything through the VPN" needs gateway NAT +
> forwarding, which is **not built** (tracked as **S3.7**). Full-tunnel configs
> will connect but their `0.0.0.0/0` internet egress dies at the gateway.

## Prerequisites

- A public VM (2 vCPU / 2 GB is plenty) with Docker + Docker Compose.
- Its **public IP** (or a DNS name pointing at it).
- Ability to edit the cloud **security group / firewall**.

## 1. Open the ports (cloud security group AND host firewall)

Publishing a port in compose is **not** enough — the cloud provider's security
group is a separate layer.

| Port | Proto | Why |
|---|---|---|
| 80 (and 443 if TLS) | TCP | dashboard + API + CLI login |
| **51820** | **UDP** | **WireGuard data plane — the tunnel itself** |

The WireGuard port is **UDP**, not TCP — a TCP-only rule silently blocks every
tunnel while the dashboard looks fine.

## 2. Configure `.env` (endpoint BEFORE you enroll anything)

```sh
cp .env.example .env
```

Set these before first boot:

```sh
# The address CLIENT CONFIGS DIAL. Must be the VM's PUBLIC ip/host + the WG UDP
# port — NOT the compose service name. This is the #1 reason a tunnel "connects"
# in the dashboard but never hands-shakes: the .conf points at an unreachable host.
TUNNEX_NODE_ENDPOINT=YOUR_PUBLIC_IP:51820

# Public base URL for the dashboard, emailed links, and the CLI. Use https once
# TLS is on (below).
APP_BASE_URL=http://YOUR_PUBLIC_IP

# Real SMTP so verification / reset emails actually send (dev uses Mailpit).
# SMTP_HOST=... SMTP_PORT=... SMTP_FROM=... SMTP_USERNAME=... SMTP_PASSWORD=...
```

**Ordering that bites people:** `TUNNEX_NODE_ENDPOINT` is baked into every device
config at creation time. Set it **before** you enroll the gateway and create
devices. If you create a device first and fix the endpoint later, that device's
`.conf` still points at the old (wrong) address — you must revoke and recreate it
(configs are one-time and never re-served).

## 3. Boot

```sh
docker compose up -d --build --wait
```

The node-agent already has `NET_ADMIN` + `/dev/net/tun` and publishes
`51820/udp` in compose — leave that as-is. It idles until you give it a join
token (next step).

## 4. TLS + secure cookies (before real use)

For a throwaway test, `http://` works. For anything real:

- Terminate TLS at nginx (real cert; put the domain in `APP_BASE_URL=https://...`).
- Set `TUNNEX_COOKIE_SECURE=true` so the session cookie is only sent over HTTPS.
  Leaving it `false` on a public host means session cookies can traverse plain
  HTTP — do not ship that.

## 5. Create the org, enroll the gateway, connect

1. Browse to `APP_BASE_URL` → sign up → verify email → create your organization.
2. **Devices → Gateways → Enroll gateway.** Name it, generate the join token.
   The ceremony shows the complete line — if you named the gateway it includes
   `TUNNEX_NODE_NAME`:
   ```sh
   TUNNEX_JOIN_TOKEN=… TUNNEX_NODE_NAME="my-gw"
   ```
3. Give that to the node-agent and restart it (compose plumbs both vars):
   ```sh
   TUNNEX_JOIN_TOKEN=…  TUNNEX_NODE_NAME=my-gw  docker compose up -d node-agent
   ```
   The node appears in the dashboard once the agent redeems the token.
4. Create a device — via the dashboard (download the `.conf`) or the CLI:
   ```sh
   tunnex login --server https://YOUR_HOST     # browser or --device
   tunnex device create --name my-laptop        # writes ~/.config/tunnex/device.conf (0600)
   tunnex up                                     # wg-quick up
   ```
5. Verify: `wg show` has a recent handshake; you can reach the gateway
   (`ping 10.99.0.1`) and other peers on the pool CIDR. The dashboard shows the
   device **online** with a last-handshake time.

## Troubleshooting

- **Dashboard shows the device but no handshake / can't ping:** almost always
  `TUNNEX_NODE_ENDPOINT` (wrong/private address) or **UDP 51820 blocked** in the
  cloud security group.
- **`node_not_ready` when creating a device:** the agent hasn't reported its
  endpoint/key yet — confirm it enrolled (`docker compose logs node-agent`) and
  that `TUNNEX_NODE_ENDPOINT` is set.
- **Login works locally but not from the internet:** `APP_BASE_URL` still points
  at localhost, or (with TLS) `TUNNEX_COOKIE_SECURE` mismatched the scheme.
- **"Everything routes through the VPN" doesn't work:** expected — split-tunnel
  only until S3.7 (gateway NAT + forwarding).
