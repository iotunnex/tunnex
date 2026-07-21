# Deck B rematch — failover PROVEN on the wire (S8.6 merge-gating fix)

Date: 2026-07-21. CP at `782d036` (S8.7 tip; the failover fold `ac230ac` rides in it +
migration 0039 the dev-VM DB required). Cross-cloud: aws-gw (`019f786e`, AWS Sydney),
aws-gw-2 (`019f8353`, AWS, standby), azure-gw (Azure). Org `apple`.
Weapon: `docker stop tunnex-node && ip link del wg0` on aws-gw.

## Pre-flight gates (both passed before the kill)

- (a) **Ticker runs the fold** — `failover_tick` Info lines flowing, per-member
  observed-age / verdict / counters / decision. Exogenous suspect (a) killed.
- (b) **240s window's first dividend** — the observability CAUGHT contaminated state:
  the old 90s CP had spuriously demoted BOTH members (`demoted=true`), rehydrated by
  seedDemoted. The fold's fail-back self-healed it: `08:20:13 restore=[both]` →
  `demoted=[]`. Kill held until both `demoted=false stale_n=0` (clean).

## The kill → promotion clock (all UTC)

```
08:22:25  KILL aws-gw (stopwatch start)
~08:22:09 last handshake azure observed with aws-gw (the freeze point)
08:22:43+ aws-gw age climbs MONOTONIC (34s→1m4s→...), no rehandshake reset — corpse
          certified dead from the LIVING side (azure). suspect (c) killed.
08:26:13  age=4m4s → verdict flips fresh→STALE, stale_n=1  (crossed 240s)
08:26:43  stale_n=2
08:27:13  age=5m4s stale_n=3 → decision=demote=[019f786e]   ← PROMOTION
```
**Kill → promotion = 4m48s** (240s window from the freeze + 3×30s hysteresis, to the
second). aws-gw-2 (`019f8353`) stayed `fresh` throughout (alive, observed) — promotion
went to a proven-live standby. ZERO flicker — the window fix held.

Audit: `08:27:13 hub_set.promotion old=aws-gw(019f786e) → new=aws-gw-2(019f8353)
primary_stale gen=6`. Banner: "Failover in effect — the configured primary is
unreachable; a standby is carrying transit." View: aws-gw-2 PRIMARY warm, hub set v6.

Transit follows the hub: `azure → 172.31.17.64 (aws-gw-2) @ 138ms` (cross-cloud, via
the promoted hub; dead-primary path had no route). Grant rule present on aws-gw-2
(`saddr 172.31 daddr 10.0 accept` + default-drop) — enforcing boundary followed the
promotion.

## Fail-back (exactly one restore)

```
08:22:25→08:36:13  demoted=true HELD the whole outage; stale_n climbed to 21 — NO
                    spurious restore while dead.
08:36:43  aws-gw revived (docker start) → age=52s fresh fresh_n=1
08:37→38  fresh_n 2→3→4 (hysteresis hold)
08:38:43  fresh_n=5 → decision=restore=[019f786e]   ← ONE restore at exactly M=5
```
No flap, no metronome. Audit: `08:38:43 hub_set.failback old=aws-gw-2(019f8353) →
new=aws-gw(019f786e) recovered gen=7`. View: hub set v7, aws-gw PRIMARY warm /
aws-gw-2 STANDBY warm, azure-gw online (the transient site-link-down cleared).

## Verdict

The merge-gating "failover does not fire" bug is DEAD — proven on the wire both
directions, clean N=3/M=5 hysteresis, zero flicker, full observability. The root
(90s window < WG rekey cadence) is fixed at 240s; the fold ALSO self-healed the
old CP's contaminated demotion state live. This SATISFIES the S8.6 proof (the unit
reds were the SUBSTITUTE).

## Findings held (neither a mechanism failure)

1. **Enforcing-face counter-increment** — unprovable in this topology: aws-site has
   only the two gateways, no behind-host to forward THROUGH the hub to. Rule-present
   + 138ms reach is the substitute; increment needs a third aws-side host. (Same
   "why a behind-host may not reach yet" class the UI already flags.)
2. **Transient "azure-gw site link down" during failover** — the demoted-but-still-
   peered dead primary (kept as a keepalive-only peer for warm fail-back) trips
   azure's agent-computed `site_link_stale` while transit actually flows via the new
   hub. Cleared on fail-back. Misleading-health-during-failover candidate; disposition
   post-deck.
