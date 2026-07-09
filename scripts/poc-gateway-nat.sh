#!/usr/bin/env bash
# POC-ONLY gateway NAT (S3.7-lite) — throwaway, NOT the real gateway-NAT feature.
#
# Full-tunnel routes ALL client traffic to the gateway; without NAT it black-holes.
# This installs a masquerade + forwarding in the node-agent CONTAINER so a POC
# full-tunnel can reach the internet. It is EPHEMERAL (gone on container rebuild) and
# deliberately crude (a double-NAT via the Docker bridge; the return path is flaky —
# the `rx=92` issue). The PROPER version — reconcile-managed egress, per-org policy,
# IPv6, a DNS resolver on the gateway — is the PARKED S3.7 story. Do not ship this.
#
# Prereq: docker-compose.yml sets `sysctls: net.ipv4.ip_forward=1` on node-agent.
# Usage (on the compose host): ./scripts/poc-gateway-nat.sh
set -euo pipefail
cd "$(dirname "$0")/.."

POOL="${TUNNEX_POC_POOL:-10.99.0.0/24}"

echo ">> installing POC NAT in the node-agent container (pool $POOL)"
docker compose exec -T node-agent sh -c '
  set -e
  apk add --no-cache iptables >/dev/null 2>&1 || true
  fwd=$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo 0)
  [ "$fwd" = 1 ] || { echo "!! ip_forward=$fwd — add sysctls: net.ipv4.ip_forward=1 to node-agent + recreate"; exit 1; }
  EG=$(ip route show default | awk "{print \$5; exit}")
  echo "   egress iface: $EG"
  # Idempotent (-C check before -A add).
  iptables -t nat -C POSTROUTING -s '"$POOL"' -o "$EG" -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s '"$POOL"' -o "$EG" -j MASQUERADE
  iptables -C FORWARD -i wg0 -o "$EG" -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i wg0 -o "$EG" -j ACCEPT
  iptables -C FORWARD -i "$EG" -o wg0 -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i "$EG" -o wg0 -m state --state RELATED,ESTABLISHED -j ACCEPT
  echo "   NAT + forwarding installed on $EG"
'
echo ">> done (re-run after any node-agent rebuild — the rules are ephemeral)"
