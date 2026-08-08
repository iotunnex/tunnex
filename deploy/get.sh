#!/bin/sh
# Tunnex one-command installer — served at https://get.tunnex.io
#
#   curl -fsSL https://get.tunnex.io | sh
#
# Verify first if you would rather (and you should):
#
#   curl -fsSL https://get.tunnex.io -o get.sh
#   curl -fsSL https://get.tunnex.io/SHA256SUMS -o SHA256SUMS
#   sha256sum -c SHA256SUMS --ignore-missing
#   less get.sh && sh get.sh
#
# Non-interactive (every default taken, nothing asked):
#
#   curl -fsSL https://get.tunnex.io | sh -s -- --yes
#   curl -fsSL https://get.tunnex.io | TUNNEX_PUBLIC_ADDR=vpn.acme.com sh -s -- --yes
#
# ⛔ THIS REPLACES dl.tunnex.io ENTIRELY. That host is NXDOMAIN — measured — while the download page
# advertised four commands against it, every one of which failed at DNS on line one. A customer's first
# contact with the product was a resolver error.
#
# Idempotent: re-running against an existing ./tunnex REUSES the generated database password. A fresh one
# would not match the volume, and the stack would come up refusing its own credentials.
set -eu

# ── plumbing ────────────────────────────────────────────────────────────────────────────────────
say() { printf '%s\n' "$*"; }
die() { printf '\n!! %s\n' "$*" >&2; exit 1; }

# have_tty: can we read from the controlling terminal? True under `curl | sh` on a real terminal, false in
# CI — which is why --yes exists rather than the script hanging on a prompt nobody can answer.
have_tty() { [ -r /dev/tty ] && [ -t 1 ]; }

ASSUME_YES=0
for arg in "$@"; do
	case "$arg" in
	--yes | -y) ASSUME_YES=1 ;;
	--help | -h)
		say "usage: sh get.sh [--yes]"
		say "  --yes   take every default, ask nothing (for automation)"
		exit 0
		;;
	*) die "unknown argument: $arg" ;;
	esac
done

# ask PROMPT DEFAULT — reads from the TERMINAL even under `curl | sh`, shows the default in brackets, and
# Enter accepts it. Under --yes (or with no terminal) it returns the default without asking.
ask() {
	_prompt="$1"
	_default="${2:-}"
	if [ "$ASSUME_YES" = "1" ] || ! have_tty; then
		printf '%s' "$_default"
		return
	fi
	if [ -n "$_default" ]; then
		printf '%s [%s]: ' "$_prompt" "$_default" >/dev/tty
	else
		printf '%s: ' "$_prompt" >/dev/tty
	fi
	IFS= read -r _reply </dev/tty || _reply=""
	[ -n "$_reply" ] || _reply="$_default"
	printf '%s' "$_reply"
}

# ⛔ LOOPBACK IS REFUSED AT THE SOURCE, both paths. Email links and the WireGuard endpoint both derive from
# this value, so `localhost` produces a deployment that looks healthy on every screen and is unreachable
# from every device. A scheme is refused too — this is a bare host, not a URL.
addr_ok() {
	case "$1" in
	"" | localhost | 127.* | 0.0.0.0 | ::1 | *://*) return 1 ;;
	esac
	return 0
}

# ── 0. preflight ────────────────────────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || die "Docker is required. Install Docker Engine, then re-run."
docker compose version >/dev/null 2>&1 ||
	die "The Docker Compose v2 plugin is required (\`docker compose version\` must work)."
command -v curl >/dev/null 2>&1 || die "curl is required."

say ''
say '=========================================================================='
say '  TUNNEX INSTALLER'
say '=========================================================================='
say ''

# ── 1. pin a real RELEASE — never :latest for a real deploy ─────────────────────────────────────
API="https://api.github.com/repos/iotunnex/tunnex"
RAW="https://raw.githubusercontent.com/iotunnex/tunnex"
VERSION="${TUNNEX_VERSION:-}"
if [ -z "$VERSION" ]; then
	VERSION="$(curl -fsSL "${API}/releases/latest" 2>/dev/null |
		grep -m1 '"tag_name"' | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/')"
fi
[ -n "$VERSION" ] || die "could not resolve a released Tunnex version. Set TUNNEX_VERSION to pin one."
say ">> Installing Tunnex ${VERSION}"
say ''

# ── 2. the questions ────────────────────────────────────────────────────────────────────────────
say '-- Deployment --------------------------------------------------------------'

# ⚠ NO DEFAULT FOR THE ADDRESS, ON PURPOSE. Every other prompt can be guessed; this one cannot, and a wrong
# guess is invisible until a device fails to connect.
ADDR="${TUNNEX_PUBLIC_ADDR:-}"
if [ -n "$ADDR" ]; then
	addr_ok "$ADDR" || die "TUNNEX_PUBLIC_ADDR='${ADDR}' is not usable (loopback, empty, or includes a scheme)."
elif have_tty && [ "$ASSUME_YES" = "0" ]; then
	while :; do
		ADDR="$(ask '   Public hostname or IP your users and gateways reach (e.g. vpn.acme.com)' '')"
		addr_ok "$ADDR" && break
		say "   !! Not usable. Enter the bare DNS name or public IP — no http://, not localhost."
	done
else
	die "no public address. Re-run with it set:
    curl -fsSL https://get.tunnex.io | TUNNEX_PUBLIC_ADDR=vpn.acme.com sh -s -- --yes"
fi

ADMIN_EMAIL="$(ask '   Administrator email' "${TUNNEX_ADMIN_EMAIL:-admin@${ADDR}}")"

# ⛔ THE POOL CIDR IS ASKED, NOT ASSUMED. It is the address space every device gets a /32 from, and it must
# not collide with any LAN you intend to route — a collision is discovered later, as traffic that silently
# goes to the wrong place.
POOL_CIDR="$(ask '   WireGuard address pool (must not overlap any LAN you will route)' "${TUNNEX_POOL_CIDR:-10.99.0.0/16}")"

say ''
say '-- Email -------------------------------------------------------------------'
say '   Invitations, password resets and email verification all need SMTP.'
say ''

SMTP_HOST="${SMTP_HOST:-}"
SMTP_PORT="${SMTP_PORT:-587}"
SMTP_FROM="${SMTP_FROM:-}"
SMTP_USERNAME="${SMTP_USERNAME:-}"
SMTP_PASSWORD="${SMTP_PASSWORD:-}"

if [ -z "$SMTP_HOST" ] && have_tty && [ "$ASSUME_YES" = "0" ]; then
	WANT="$(ask '   Configure SMTP now? (yes/skip)' 'skip')"
	case "$WANT" in
	y | Y | yes | YES)
		SMTP_HOST="$(ask '   SMTP host' '')"
		# ⚠ 587, NOT 465. Go'"'"'s net/smtp dials plaintext and upgrades via STARTTLS; it has NO implicit-TLS
		# path, so an SMTPS port hangs or errors. This is a standard-library property, not a setting.
		SMTP_PORT="$(ask '   SMTP port (587 = STARTTLS; 465 is NOT supported)' '587')"
		SMTP_FROM="$(ask '   Send mail as' "no-reply@${ADDR}")"
		SMTP_USERNAME="$(ask '   SMTP username (blank if none)' '')"
		[ -z "$SMTP_USERNAME" ] || SMTP_PASSWORD="$(ask '   SMTP password' '')"
		;;
	esac
fi

if [ -z "$SMTP_HOST" ]; then
	say ''
	say '   !! SMTP SKIPPED. Invitations will NOT be delivered.'
	say '      The invitation is still created and the dashboard shows a copyable link'
	say '      you can send yourself — nothing silently succeeds. Password resets and'
	say '      email verification will not work at all until SMTP is set.'
	say ''
	say '      To fix later: set SMTP_HOST/SMTP_PORT/SMTP_FROM (and SMTP_USERNAME +'
	say '      SMTP_PASSWORD if your provider needs auth) in .env, then:'
	say '        docker compose -f tunnex.yml up -d api'
fi

# ── 3. workspace ────────────────────────────────────────────────────────────────────────────────
mkdir -p tunnex
cd tunnex

curl -fsSL "${RAW}/${VERSION}/deploy/tunnex.yml" -o tunnex.yml ||
	die "could not download deploy/tunnex.yml for ${VERSION}"

# ⛔ CONFIRM WHAT WE FETCHED IS A PRODUCTION COMPOSE FILE, rather than trusting the tag. A dev compose file
# reaching a customer is a Mailpit that swallows every invitation and a non-production environment flag —
# both of which look fine and behave wrongly. Cheap to check, catastrophic to miss.
if grep -qi 'mailpit' tunnex.yml; then
	die "the fetched tunnex.yml references Mailpit — that is a development compose file. Refusing to install."
fi
grep -q 'TUNNEX_ENV: production' tunnex.yml ||
	die "the fetched tunnex.yml does not set TUNNEX_ENV: production. Refusing to install."
say ''
say ">> Verified: production compose, no Mailpit."

# ── 4. secrets — REUSE the DB password on a re-run (a new one will not match the volume) ─────────
PG_PASS=""
if [ -f .env ]; then
	PG_PASS="$(grep -E '^POSTGRES_PASSWORD=' .env | head -1 | cut -d= -f2- || true)"
	[ -n "$PG_PASS" ] && say ">> Reusing the existing database password (idempotent re-run)."
fi
[ -n "$PG_PASS" ] || PG_PASS="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"

umask 077
cat >.env <<EOF
# Written by the Tunnex installer. Edit values here; never hand-edit tunnex.yml.
TUNNEX_VERSION=${VERSION}
APP_BASE_URL=http://${ADDR}
TUNNEX_PUBLIC_ADDR=${ADDR}
TUNNEX_ADMIN_EMAIL=${ADMIN_EMAIL}
TUNNEX_POOL_CIDR=${POOL_CIDR}
POSTGRES_PASSWORD=${PG_PASS}
SMTP_HOST=${SMTP_HOST}
SMTP_PORT=${SMTP_PORT}
SMTP_FROM=${SMTP_FROM}
SMTP_USERNAME=${SMTP_USERNAME}
SMTP_PASSWORD=${SMTP_PASSWORD}
# ⛔ NEVER SET THIS ON A DEPLOYMENT — it tees message bodies to the log, and those bodies are working links.
MAIL_DEV_LOG=false
EOF

# ── 5. start ────────────────────────────────────────────────────────────────────────────────────
say ''
say '>> Pulling images and starting the stack...'
docker compose -f tunnex.yml pull
docker compose -f tunnex.yml up -d --wait

# ── 6. THE FIRST-RUN CREDENTIAL — READ BACK AND PRINTED WHERE THE OPERATOR IS LOOKING ───────────
#
# ⛔ THIS IS THE STEP THAT WAS MISSING AND IT COST THE WHOLE INSTALL. bootstrap.EnsureAdmin prints the
# administrator credential ONCE, to the API container's stdout — and `up -d` is detached, so it scrolled
# into a log the operator was never told to read. The code comment on that banner says it exists precisely
# because a JSON log line was "correct, greppable, invisible"; detaching put it straight back out of sight.
#
# ⚠ THE CREDENTIAL IS NOT REPRINTABLE. It exists as an argon2id hash and nowhere else, so this is the only
# moment it can be surfaced. If this read fails, the operator must be told the exact command — not left to
# guess — because the alternative is `docker compose down -v` and starting over.
say ''
CREDS="$(docker compose -f tunnex.yml logs api 2>/dev/null |
	sed -n '/TUNNEX - FIRST RUN/,/^.*=\{20,\}$/p' | tail -n +2 || true)"

if printf '%s' "$CREDS" | grep -q 'password'; then
	say '=========================================================================='
	say '  YOUR ADMINISTRATOR CREDENTIAL — SHOWN ONCE, COPY IT NOW'
	say '=========================================================================='
	printf '%s\n' "$CREDS"
	say '=========================================================================='
else
	say '=========================================================================='
	say '  !! COULD NOT READ THE FIRST-RUN CREDENTIAL FROM THE API LOG'
	say '=========================================================================='
	say ''
	say '  It was printed once at first boot. Retrieve it with:'
	say ''
	say '    docker compose -f tunnex.yml logs api | grep -A8 "FIRST RUN"'
	say ''
	say '  If it is genuinely gone there is no recovery and no second admin —'
	say '  reset the deployment with:  docker compose -f tunnex.yml down -v'
	say '=========================================================================='
fi

# ── 7. hand-off ─────────────────────────────────────────────────────────────────────────────────
say ''
say '=========================================================================='
say " Tunnex ${VERSION} is running."
say ''
say "   1. Open:      http://${ADDR}/"
say '   2. Sign in with the administrator credential above.'
say '      You will be required to set your own password immediately.'
say '   3. Create your first organization.'
say '   4. Enroll a gateway:  Dashboard -> Gateways -> Generate join token,'
say '      then run the command it shows on the machine that will be the gateway.'
say ''
# ⭐ SIGNUP IS ALREADY SHUT, and saying so is not trivia: it is the difference between an operator who
# thinks they must hurry and one who knows the deployment is already theirs.
say '   Public signup is CLOSED on this deployment — the administrator account'
say '   above is the only way in, and everyone else arrives by invitation or SSO.'
say ''
say "   Config:   $(pwd)/.env"
say '   Upgrade:  set TUNNEX_VERSION to a newer tag in .env, then:'
say '             docker compose -f tunnex.yml pull && docker compose -f tunnex.yml up -d'
say '=========================================================================='
say ''
