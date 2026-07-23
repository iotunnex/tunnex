# WF-A — device re-homing on hub failover (client HA) — commit-one

**Origin:** EPIC 8 smooth walk (2026-07-23, `walk-artifacts/S8.6/SMOOTH-WALK-record.md`,
Test A). A split-tunnel device peers with ONE gateway (its assigned node, a static endpoint).
When that gateway's DATA PLANE dies, the client is stuck "Connecting…" and recovers ONLY when
ITS gateway returns — CONFIRMED on the wire (the mac reconnected the instant aws-gw-1 restarted,
NOT when aws-gw-2 was promoted). **Hub HA is site-transit redundancy, not device-connectivity
redundancy.** This is a FEATURE, not a hotfix — decision-first, held for founder ruling.

**Scope (founder-set, honest): v1 = re-home a connected device when its hub is PROMOTED-PAST
(the walk's exact scenario).** Roaming / latency-based / nearest-hub selection is explicitly
NOT this story.

## D-WFA-0 (BLOCKING PREREQUISITE — decides the whole fork) — control-path independence

Does the device's CONTROL channel to the CP survive its DATA tunnel being down? The device
fetches config / reports over some path; if that path rides the WG tunnel (through the dead
gateway), the CP-driven option is impossible — a device whose tunnel is down can't be told to
re-home. **VERIFY FIRST, cite the answer.** If the control path is independent (direct HTTPS to
the CP public endpoint, NOT through the tunnel), CP-driven is viable. If it rides the tunnel,
only client-side failover works. **This item gates D-WFA-1.**

## D-WFA-1 (LEAD — needs ruling) — the re-home MECHANISM

- **(a) CP-driven re-homing** — on promotion, the device's compiled config re-points its
  endpoint to the promoted hub; rides the EXISTING promotion→compile→push path (the same
  machinery hub-set failover already uses). ZERO client election logic (honors observe-never-
  vote, refused all epic). COST: requires the control path to survive gateway death (D-WFA-0);
  a device offline at promotion re-homes on its next CP contact (bounded lag).
- **(b) client-side multi-endpoint** — the device config carries the hub-set endpoints in
  priority order; the client tries them in order on handshake-death. Works during CP
  unreachability + needs no CP round-trip. COST: puts election-ADJACENT logic (which-hub-is-up)
  in the client — the exact class observe-never-vote refused; a client picking a hub the CP
  hasn't promoted diverges the two truths.
- **(c) both-layered** — CP-driven primary + client multi-endpoint fallback for CP-unreachable.

**FOUNDER LEAN:** (a) CP-driven as PRIMARY **IF D-WFA-0 confirms the control path is
independent** (cite it); (b) client-side multi-endpoint REGISTERED as the follow-up for
CP-unreachable scenarios. Ruling held.

## D-WFA-2 (RULED direction) — re-home rides the generation/audit machinery

A device silently hopping gateways is the two-truths class. The re-home MUST carry a
generation bump + an audit event (`device.rehomed` or rides `hub_set.promotion`'s consequence),
same as every other state transition this epic. No silent endpoint swap.

## D-WFA-3 — failback symmetry

When the original hub recovers and reclaims PRIMARY (M=5 fresh), does the re-homed device
return to it, or stay on the standby-now-primary? v1 default: follow the active primary (the
device tracks the hub set's members[0]), same rule as the site-link graph — consistent, no
device-special path. State explicitly.

## Reds (to define at build, after ruling)

1. The walk's EXACT fixture: connected device on the primary → primary data-plane dies →
   standby promoted → device re-homes to the promoted hub WITHOUT a manual reconnect (the
   scenario that failed live).
2. Generation/audit: the re-home emits its event; no silent hop (D-WFA-2).
3. Failback: original hub reclaims → device follows per D-WFA-3.
4. Control-path independence proof (D-WFA-0) is a NAMED precondition red — if it fails, the
   fork flips to (b) and this red set is rewritten.

**Sequence:** this paper → D-WFA-0 verify (control-path independence — the fork-decider) →
founder ruling on D-WFA-1 → build as its OWN story (touches the compiler's peer model + the
client — the epic's most-reviewed surfaces) → targeted review → box-walk the walk's exact
fixture. NOT a hotfix — a story.
