# EPIC 8 Batch Walk — Session Summary (2026-07-21)

## Train (git-verified, rebased + pushed)
| Story | Sha | State |
|-------|-----|-------|
| S8.5 routed-subnets | `fc7c4b8` | + WF-4-local fold |
| S8.6 hub-HA | `157877d` | rebased clean |
| S8.7 CIDR + live flush | `4e64b98` | rebased (fold+conntrack), branch tip |

## Live deployment (cross-cloud, all on the branch)
- CP: Azure `Tunnex-dev-vm` / 40.65.63.141 — `make up-enterprise` (source build), migrated v39, edition=enterprise
- aws-gw (primary hub): 16.176.32.176 / 172.31.28.80 — folded node-agent, agent_ready
- azure-gw (leaf): 20.245.69.218 / 10.0.0.5 — folded node-agent, agent_ready
- inst-2 (standby-to-be): 172.31.17.64 — running, behind-host target
- Mac client: Tunnex-0.1.0-universal.pkg (sha aff7dda2), split-tunnel, device 10.99.0.3

## Legs PROVEN on live iron
- **A2** routed ≠ permitted — behind-hosts default-denied under enforcing, route present
- **A1** reach LAN behind gateway — with WF-4-local fold, ZERO manual rules, ttl=63 forward
- **C1** live conntrack flush — running ping cut 16→17 on grant-revoke, `conntrack_flushed flows:1`
- **C2** scoped flush — tcp victim dies, udp neighbor survives, `flows:1` (protocol-scoped)

## Fold landed end-to-end
**WF-4-local** (merge-gating S8.5 defect, walk-found): device→LAN-behind-its-own-Docker-gateway
dropped by Docker FORWARD DROP (DOCKER-USER scoped to remote Routes only). Fix: accepts widen to
Routes ∪ LocalSubnets, MIRRORED orientation. Red TestDockerForwardLocalSubnetMirrored (both faces).
Rebased through the train. A1-REFOLD proved it live (agent auto-places the mirrored accept, no
manual rule). Fold re-verify banked.

## Findings HELD for disposition
1. WF-4-local — FOLDED (fc7c4b8)
2. group-membership-no-UI (low) — epic-close UI harvest
3. resource-no-port-field (low) — epic-close UI harvest
4. **A3 wg0→wg0 hub-transit** (deferred) — device→REMOTE-site via hub dropped by Docker FORWARD
   DROP (third WF-4 variant); S8.2/S8.6 subsystem, not S8.5-gating. Fix = wg0→wg0 transit accept,
   own paper (candidate S8.6b / S8.2c-follow-up).

## RESUME (boxes warm) — remaining
A3 (owning-story fix) · A4 DNS · A5 crash-sweep · A6 metrics · A7 blast-radius · **Deck B (HA:
standby enroll → pin → primary-kill → failover → fail-back)** · C3 CIDR-source · C4 flush-fail ·
C5 device-revoke-exempt · Deck D (UI: CW-confirm, topology, stale-button).

## Standing
Merge = Pawan's explicit word, per-story, train order, after the decks. Nothing merged this session.
GATE-REPORT-NEEDS-SHA honored throughout (phantom accepts refused; real shas built + reported).

---

## Session 2 addendum (2026-07-21 cont.) — S8.6b fold + A3b + Deck-B-fresh

**S8.6b wg0→wg0 hub-transit fold BUILT + PROVEN (scope).** Founder ruled A3=(a); phantom acceptance refused
(GATE-REPORT-NEEDS-SHA). Route DOCKER-USER rules RELAXED (D-transit-1 B: `oifname wg0 daddr`/`iifname wg0
saddr`, one rule covers eth0→wg0 + wg0→wg0) + drift-detection convergence (D-transit-2: orientation signature,
replace-on-drift one pass) + 5 reds. Deployed live: aws-gw rules relaxed + old orientation swept one pass;
aws-gw now forwards the wg0→wg0 transit (tcpdump egress). Node gates GREEN.

**A3b registered (distinct finding, NOT the fold).** device→remote-site end-to-end still fails: transit keeps
src=device-pool (un-NAT'd, masq excludes wg0→wg0) → azure-gw WG cryptokey-routing rejects (pool ∉ site-link
peer AllowedIPs) + no far device grant. Named story "device→remote-site hub transit", commit-one fork
pre-named (NAT-at-hub vs widen-AllowedIPs+compile-far-grants). Named limitation: device reaches the hub's own
site (A1); remote-site transit deferred.

**Deck B UNBLOCKED, runs FRESH.** Continuity legs = behind-host paths (A1/S8.2c geometry); device-transit
variant dropped as A3b-deferred. Deferred to a fresh session (tail-of-turn: the hub-kill timeline IS the
evidence, deserves fresh hands + fresh clock).

**TRAIN (git cat-file verified):** S8.5 `fc7c4b8` · S8.6 `4506772` · S8.7 code `2517e60` / tip `3001ad4`.

**RESUME order:** Deck B (standby enroll 3.25.125.203 → pin → warm-verify → primary-kill + clock → fail-back)
→ C3 CIDR-source → C4 flush-fail → C5 device-revoke-exempt → Deck D (UI harvest: group-membership-no-UI,
resource-no-port-field, CW-confirm, topology, stale-button). Then A3b as its own decision-first story.
Merge = Pawan's word, per-story, train order, after decks. Nothing merged.
