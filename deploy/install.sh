#!/bin/sh
# Tunnex.io — zero-build installer (S6.6). ONE script, safe to pipe blind into a root shell.
#
#   Convenience (one-liner):
#     curl -fsSL https://raw.githubusercontent.com/iotunnex/tunnex/main/deploy/install.sh | sh
#
#   Security-conscious (download, verify, inspect, then run — the recommended default):
#     curl -fsSL <url>/install.sh -o install.sh
#     curl -fsSL <url>/install.sh.sha256 -o install.sh.sha256 && sha256sum -c install.sh.sha256
#     less install.sh
#     sudo sh install.sh
#
# Brings up a working Tunnex deployment from PREBUILT images — no source build, no file edits.
# Prerequisite: any host with Docker Engine + the Compose v2 plugin AND a public address (a DNS name
# or public IP users + gateways can reach). It installs the SOFTWARE; it does not conjure the server.
#
# Non-interactive / piped-with-no-terminal: set the two inputs as env vars so the pipe still works:
#     curl -fsSL <url> | TUNNEX_PUBLIC_ADDR=vpn.acme.com TUNNEX_SMTP=skip sh
# For SMTP=configure non-interactively, also export SMTP_HOST/SMTP_PORT/SMTP_USERNAME/SMTP_PASSWORD/SMTP_FROM.
#
# Idempotent: re-running against an existing ./tunnex REUSES the generated DB password (a fresh one
# would not match the existing postgres volume) and never leaves a half-written .env (write-then-move).
set -eu

REPO="iotunnex/tunnex"
RAW="https://raw.githubusercontent.com/${REPO}"
API="https://api.github.com/repos/${REPO}"
DIR="${TUNNEX_DIR:-tunnex}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# BEGIN INSTALL VERSION RESOLVER — kept byte-identical in install.sh and get.sh; the regression test
# extracts this block from both files and refuses drift.
resolve_install_version() {
	VERSION="${TUNNEX_VERSION:-}"
	SOURCE_REF="${TUNNEX_SOURCE_REF:-}"
	SOURCE_COMMIT=""
	VERSION_PROVENANCE=""

	if [ -z "$VERSION" ]; then
		# ⛔ A RELEASE IS NOT THE LATEST GREEN BUILD. Main CI publishes :latest and :sha-<commit> after
		# its gates, while GitHub's latest-release pointer advances only when somebody cuts a release. The old resolver
		# therefore served July's v0.3.0-rc5 during an August install even though newer green images existed.
		#
		# The successful CI WORKFLOW is the atomic publication pointer: it cannot be success until every
		# image in the publish matrix has finished. Bind the compose manifest and all image tags to that
		# same commit, so an install cannot mix a new manifest with old or partially-published images.
		_runs="$(curl -fsSL "${API}/actions/workflows/ci.yml/runs?branch=main&event=push&status=success&per_page=1" 2>/dev/null || true)"
		SOURCE_COMMIT="$(printf '%s' "$_runs" |
			sed -n 's/.*"head_sha"[[:space:]]*:[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' |
			head -1)"
		[ -n "$SOURCE_COMMIT" ] || die "could not resolve the newest fully-published green main build. Refusing to fall back to an older release."
		VERSION="sha-$(printf '%.7s' "$SOURCE_COMMIT")"
		SOURCE_REF="$SOURCE_COMMIT"
		VERSION_PROVENANCE="successful main CI commit ${SOURCE_COMMIT}"
	else
		case "$VERSION" in
		latest)
			SOURCE_REF="${SOURCE_REF:-main}"
			;;
		sha-*)
			# Docker's sha-* image tag is not a Git ref; raw.githubusercontent.com accepts the abbreviated
			# commit after the prefix is removed. TUNNEX_SOURCE_REF remains available for an explicit full SHA.
			SOURCE_REF="${SOURCE_REF:-${VERSION#sha-}}"
			;;
		*)
			SOURCE_REF="${SOURCE_REF:-$VERSION}"
			;;
		esac
		VERSION_PROVENANCE="operator override ${VERSION} (manifest ref ${SOURCE_REF})"
	fi

	case "$VERSION" in '' | *[!A-Za-z0-9._-]*) die "resolved image tag '${VERSION}' contains unsupported characters" ;; esac
	case "$SOURCE_REF" in '' | *[!A-Za-z0-9._/-]*) die "resolved manifest ref '${SOURCE_REF}' contains unsupported characters" ;; esac
}
# END INSTALL VERSION RESOLVER

# have_tty: can we read from the controlling terminal? True under `curl | sh` on a real terminal
# (stdin is the pipe, but /dev/tty is the keyboard); false in CI / fully-detached pipes.
have_tty() { [ -e /dev/tty ] && { true </dev/tty; } 2>/dev/null; }
# ask reads from the TERMINAL even under `curl | sh`.
ask() {
	printf '%s' "$1" >/dev/tty
	IFS= read -r reply </dev/tty || die "no input on the terminal"
	printf '%s' "$reply"
}
# A public address must NOT be loopback/empty or carry a scheme (POC item 3: verify/reset/invite
# links + the WG endpoint are emitted from it — localhost is unreachable off-box).
addr_ok() {
	case "$1" in
	'' | localhost | localhost:* | 127.* | ::1 | 0.0.0.0 | *://*) return 1 ;;
	*) return 0 ;;
	esac
}

# ── 0. prerequisites — fail LOUD + actionable ────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || die "Docker is required. Install Docker Engine + the Compose plugin (https://docs.docker.com/engine/install/), then re-run."
docker compose version >/dev/null 2>&1 || die "The Docker Compose v2 plugin is required (\`docker compose version\` must work)."
command -v curl >/dev/null 2>&1 || die "curl is required."
command -v openssl >/dev/null 2>&1 || die "openssl is required (secret generation)."

# ── 1. pin the newest fully-published green main build ──────────────────────────────────────────
resolve_install_version
say ">> Installing Tunnex ${VERSION}"
say ">> Provenance: ${VERSION_PROVENANCE}"

# ── 2. public address — env override OR prompt; loopback refused at the SOURCE (both paths) ───────
ADDR="${TUNNEX_PUBLIC_ADDR:-}"
if [ -n "$ADDR" ]; then
	addr_ok "$ADDR" || die "TUNNEX_PUBLIC_ADDR='${ADDR}' is not a usable public address (loopback/empty, or includes a scheme). Set the bare DNS name or public IP users + gateways reach."
elif have_tty; then
	while :; do
		ADDR="$(ask 'Public address your users + gateways reach (DNS name or public IP, e.g. vpn.acme.com): ')"
		if addr_ok "$ADDR"; then break; fi
		say "!! '${ADDR}' is not a usable public address (loopback/empty, or includes a scheme). Enter the bare"
		say "   DNS name or public IP the machine is reachable at — email links + the WireGuard endpoint depend"
		say "   on it. (Pure-local dev uses the repo's docker-compose.yml, not this installer.)"
	done
else
	die "no terminal to prompt on. Re-run non-interactively with the address set, e.g.:
    curl -fsSL ${RAW}/main/deploy/install.sh | TUNNEX_PUBLIC_ADDR=vpn.acme.com TUNNEX_SMTP=skip sh"
fi

# ── 3. SMTP — env override (skip|configure) OR prompt; default when non-interactive = skip ───────
SMTP_HOST="${SMTP_HOST:-}"
SMTP_PORT="${SMTP_PORT:-}"
SMTP_FROM="${SMTP_FROM:-}"
SMTP_USERNAME="${SMTP_USERNAME:-}"
SMTP_PASSWORD="${SMTP_PASSWORD:-}"
SMTP_MODE="${TUNNEX_SMTP:-}"
if [ -z "$SMTP_MODE" ]; then
	if have_tty; then
		case "$(ask 'Configure SMTP now for email (verify / reset / invite)? [y/N]: ')" in
		y | Y | yes | YES) SMTP_MODE=configure ;;
		*) SMTP_MODE=skip ;;
		esac
	else
		SMTP_MODE=skip # non-interactive default: email disabled (local sign-in still works)
	fi
fi
case "$SMTP_MODE" in
configure)
	if have_tty; then
		[ -n "$SMTP_HOST" ] || SMTP_HOST="$(ask '  SMTP host: ')"
		[ -n "$SMTP_PORT" ] || SMTP_PORT="$(ask '  SMTP port [587]: ')"
		[ -n "$SMTP_USERNAME" ] || SMTP_USERNAME="$(ask '  SMTP username: ')"
		[ -n "$SMTP_PASSWORD" ] || SMTP_PASSWORD="$(ask '  SMTP password: ')"
		[ -n "$SMTP_FROM" ] || SMTP_FROM="$(ask "  From address [no-reply@${ADDR}]: ")"
	fi
	SMTP_PORT="${SMTP_PORT:-587}"
	SMTP_FROM="${SMTP_FROM:-no-reply@${ADDR}}"
	[ -n "$SMTP_HOST" ] || die "TUNNEX_SMTP=configure but SMTP_HOST is not set (export SMTP_HOST/SMTP_USERNAME/SMTP_PASSWORD for a non-interactive run)."
	;;
skip)
	say ">> ⛔ SMTP SKIPPED — NOBODY CAN BE INVITED TO THIS DEPLOYMENT."
	say "   Invitations are the only way people join, and they are delivered by email. Password resets"
	say "   and address verification need it too. You can still sign in as the administrator, and the"
	say "   dashboard shows a copyable invitation link you can send by hand."
	say "   Enable it later: set SMTP_HOST/SMTP_PORT/SMTP_FROM (and SMTP_USERNAME/SMTP_PASSWORD if your"
	say "   provider needs auth) in .env, then \`docker compose -f tunnex.yml up -d api\`."
	;;
*)
	die "TUNNEX_SMTP must be 'skip' or 'configure' (got '${SMTP_MODE}')."
	;;
esac

# ── 4. workspace + the VERSIONED compose (matches the pinned images) ─────────────────────────────
mkdir -p "$DIR"
cd "$DIR"
trap 'rm -f .env.new 2>/dev/null' EXIT # never leave a half-written .env behind on failure
curl -fsSL "${RAW}/${SOURCE_REF}/deploy/tunnex.yml" -o tunnex.yml || die "could not download deploy/tunnex.yml at ${SOURCE_REF}"

# ── 5. secrets — REUSE the existing DB password on a re-run (a new one won't match the volume) ────
PG_PASS=""
if [ -f .env ]; then
	PG_PASS="$(sed -n 's/^POSTGRES_PASSWORD=//p' .env | head -1)"
	[ -n "$PG_PASS" ] && say ">> Reusing the existing database password (idempotent re-run)."
fi
[ -n "$PG_PASS" ] || PG_PASS="$(openssl rand -hex 24)"

# ── 6. write a CLEAN .env (write the WHOLE file — NEVER append; duplicate keys make compose ──────
#      silently use the FIRST value — the trap that bit the POC). Back up any existing one. ────────
if [ -f .env ]; then
	cp .env ".env.bak.$(date +%Y%m%d%H%M%S)"
	say ">> Backed up your existing .env"
fi
umask 077
cat >.env.new <<EOF
# Tunnex deployment config — generated by install.sh. Safe to edit these values; do NOT hand-edit
# tunnex.yml. Upgrade: bump TUNNEX_VERSION to a newer release tag, then \`docker compose -f tunnex.yml pull && up -d\`.
TUNNEX_VERSION=${VERSION}
# Exact Git ref the installer used for tunnex.yml; image tag + manifest provenance stay inspectable.
TUNNEX_SOURCE_REF=${SOURCE_REF}
TUNNEX_LOG_LEVEL=info
APP_BASE_URL=http://${ADDR}
TUNNEX_NODE_ENDPOINT=${ADDR}:51820
POSTGRES_USER=tunnex
POSTGRES_PASSWORD=${PG_PASS}
POSTGRES_DB=tunnex
DATABASE_URL=postgres://tunnex:${PG_PASS}@postgres:5432/tunnex?sslmode=disable
REDIS_URL=redis://redis:6379/0
SMTP_HOST=${SMTP_HOST}
SMTP_PORT=${SMTP_PORT}
SMTP_FROM=${SMTP_FROM}
SMTP_USERNAME=${SMTP_USERNAME}
SMTP_PASSWORD=${SMTP_PASSWORD}
EOF
mv .env.new .env # atomic swap — the .env is never observed half-written

# ── 7. pull + start ─────────────────────────────────────────────────────────────────────────────
say ">> Pulling images and starting the stack…"
docker compose -f tunnex.yml pull
docker compose -f tunnex.yml up -d --wait

# ── 8. NEXT STEPS (the customer's first experience — a real hand-off, not an echo) ───────────────
say ''
say '════════════════════════════════════════════════════════════════════════════'
say " Tunnex ${VERSION} is running."
say ''
say "   1. Open the dashboard:   http://${ADDR}/"
say '   2. Sign up — you will be guided to create your first organization.'
say '   3. Enroll a gateway:     Dashboard → Gateways → “Generate join token”.'
say '      Copy the ONE command it shows and run it in this folder to bring the'
say '      gateway online (it re-creates the node-agent with your join token).'
say ''
say "   Config:   $(pwd)/.env       (edit values here; never hand-edit tunnex.yml)"
say '   Upgrade:  set TUNNEX_VERSION to a newer tag in .env, then:'
say '             docker compose -f tunnex.yml pull && docker compose -f tunnex.yml up -d'
say '════════════════════════════════════════════════════════════════════════════'
