# Tunnex.io

Self-hosted, multi-tenant VPN & Zero Trust access platform — a modern, open alternative to Pritunl.

- **WireGuard** management (OpenVPN later), with a control-plane / data-plane split.
- **Auth**: local users, Google & Microsoft SSO.
- **Clients**: CLI first, then Electron for Windows & macOS.
- **One-command install**: a single `install.sh` brings up the whole stack from **prebuilt images** — no source build, no file edits — and auto-generates every secret/key on first boot.

> The full product roadmap — every epic and story — lives in [`PLAN.md`](./PLAN.md). Point any session at it and name the current story (e.g. "we're on S1.3").

## Deploy (self-host)

On a server with **Docker Engine + the Compose v2 plugin** installed:

```bash
curl -fsSL https://raw.githubusercontent.com/iotunnex/tunnex/main/deploy/install.sh | sh
```

It asks exactly two things — your **public address** (the DNS name / public IP users and gateways
reach) and **SMTP** (or skip) — generates the DB secret, writes a clean `./tunnex/.env`, pins a
released version, and starts the stack from `ghcr.io/iotunnex/tunnex-*` images. No `git clone`, no
`--build`, no editing compose. When it finishes it prints your dashboard URL and how to enroll a
gateway.

- Dashboard → `http://<your-address>/`
- Config lives in `./tunnex/.env` (edit values there; never hand-edit `tunnex.yml`).
- Upgrade: bump `TUNNEX_VERSION` to a newer tag, then `docker compose -f tunnex.yml pull && up -d`.

## Develop locally

The dev stack builds from source (Mailpit for email, no public address):

```bash
make up                   # build + start postgres, redis, api, web, nginx, node-agent, mailpit
```

Then:

- App shell → http://localhost
- API health → http://localhost/healthz
- Mailpit (dev email inbox) → http://localhost:8025

Node ≥20 is required for the web/client workspaces (pinned via `.nvmrc` + `engine-strict`).

Tear down:

```bash
make down     # stop, keep data
make reset    # stop and wipe all volumes
```

## Repository layout

```
apps/
  api/       Go control-plane API (chi, sqlc, Postgres, Redis)
  node/      tunnex-node data-plane agent (owns WireGuard via wgctrl)
  cli/       tunnex CLI client            (EPIC 5)
  client/    Electron desktop client      (EPIC 6)
  web/       React + Vite SPA dashboard   (reused by Electron renderer)
packages/
  shared/    Generated TypeScript API client (OpenAPI-first)
deploy/
  docker/    Dockerfiles
  nginx/     Reverse-proxy config
```

## Architecture (why the split)

The **API is the control plane**; it never touches WireGuard directly. A **`tunnex-node` agent** owns the **data plane** and reconciles desired state (from the API) against the actual `wgctrl` interface state. In the compose quickstart the agent runs on the same host; the same abstraction later powers site-to-site gateways and the Kubernetes operator.

## Editions

Open-core. The multi-tenant schema lives in core; the open build limits org creation. Enterprise features (SSO, Zero Trust policies, Kubernetes operator) sit behind an `internal/enterprise/**` boundary.

## Development

Requires Docker, Go 1.23+, Node 20+, pnpm 9+.

```bash
make api      # run API locally
make agent    # run node agent locally
make web      # run web dev server
make logs     # tail compose logs
```

## Licensing

Tunnex is **open-core**:

- The **Open edition** — everything except the enterprise boundary below — is
  licensed under the **[Apache License 2.0](./LICENSE)**.
- The **Enterprise edition** — `apps/api/internal/enterprise/` and any code gated
  behind the `enterprise` build tag — is **proprietary and source-available**
  under a separate license (**[`apps/api/internal/enterprise/LICENSE`](./apps/api/internal/enterprise/LICENSE)**):
  readable for reference/evaluation, but production use requires a commercial
  agreement and it may not be redistributed.

The two editions are kept from bleeding into each other by the build-tag guard
`make test-editions`, which compiles + tests BOTH the open build and the
`-tags enterprise` build so a stray cross-edition import fails CI.

See [`NOTICE`](./NOTICE) for attribution and [`CONTRIBUTING.md`](./CONTRIBUTING.md)
(external PRs are paused pending a CLA/DCO flow).

Copyright 2026 Tunnex.
