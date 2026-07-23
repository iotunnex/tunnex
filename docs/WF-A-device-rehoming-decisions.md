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

Does the device's CONTROL channel to the CP survive its DATA tunnel being down? If it rides the
WG tunnel (through the dead gateway), CP-driven re-homing is impossible — a device whose tunnel
is down can't be told to re-home.

**VERIFIED (2026-07-23, cited both ends) — the answer is CONDITIONAL on tunnel mode:**

**SPLIT-tunnel (the desktop DEFAULT — `index.ts:23` `DEFAULT_FULL_TUNNEL=false`; the walk's EXACT
scenario) → control path INDEPENDENT → CP-driven VIABLE.**
- Client transport: `HttpDeviceApi` uses the global (undici) `fetch`, NO wg0/utun binding, NO
  custom dispatcher/proxy — follows the OS routing table. All calls hit `${origin}/api/v1/...`
  where origin = the CP base URL (`cred.server`): `httpdeviceapi.ts:26-30,47,58,100,109,118,129,143`,
  bound at `ipc.ts:61,206,307,331`. Every monitor (Revocation/Approval/RoutedRanges/Health) rides
  this same CP origin, never a gateway.
- Helper routing: a split tunnel arms **NO kill-switch**; only the peer AllowedIPs (pool + org LAN
  ranges) route to utun; the cleartext physical default is left intact BY DESIGN
  (`backend_darwin.go:79-84`). The CP's PUBLIC IP is in neither the pool nor the org LAN ranges,
  so CP traffic egresses the physical interface and survives the gateway dying.
- **Empirically confirmed on the walk:** the app kept rendering live CP data (gateway states, hub
  set, sites) the whole time the tunnel read "Connecting…".

**FULL-tunnel → control path RIDES the tunnel → CP-driven FAILS (as-is).**
- The full-tunnel kill-switch does `block drop out all` with carve-outs ONLY for {loopback, tunnel
  iface, the WG ENDPOINT UDP, DHCP, NDP} — **NO exception for the CP** (`backend_darwin.go:301-323`,
  armed full-only `:85-89`). The CP's public IP is captured by the `0.0.0.0/1`+`128.0.0.0/1` tunnel
  half-routes (`wgcommon.go:123-139`) and/or dropped. Tunnel down → CP unreachable.
- The ONLY physical-gateway carve-out today is the WG endpoint host-route (`backend_darwin.go:126-150`)
  — **the CP could join it the same way** (see D-WFA-1's resolution).

**GATE RESULT:** D-WFA-1 is UNBLOCKED for split-tunnel (CP-driven confirmed). Full-tunnel gets a
clean resolution path (a CP-endpoint kill-switch carve-out), not a fork-flip.

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

**D-WFA-0 RESULT FOLDS IN (2026-07-23):** control path is independent for SPLIT-tunnel
(confirmed, cited) → **(a) CP-driven is viable for v1** (the walk's split-tunnel scenario).
Full-tunnel's control path rides the tunnel — but the fix is NOT a fork-flip to (b): it is a
**CP-endpoint kill-switch carve-out** — add the CP's IP to the full-tunnel pf pass rules exactly
as the WG endpoint host-route already is (`backend_darwin.go:126-150,301-323`), so a full-tunnel
device with a dead gateway can still reach the CP to be re-homed. This keeps CP-driven UNIFORM
across both modes and is a small, bounded helper change (a new decide-item **D-WFA-4** below).
(b) client-side multi-endpoint stays REGISTERED for genuinely-CP-unreachable cases (CP itself
down / network-partitioned), out of v1 scope.

## D-WFA-4 (NEW, surfaced by the D-WFA-0 verify) — full-tunnel CP-endpoint carve-out

Full-tunnel's kill-switch blocks the control channel (D-WFA-0). For CP-driven re-homing to work
in full-tunnel, the kill-switch must permit egress to the CP endpoint — mirroring the WG-endpoint
carve-out that already exists (`backend_darwin.go:301-323`). Decide: is this IN v1 (CP-driven
must work in both modes → yes, add it) or is v1 SPLIT-ONLY (the walk's scenario) with full-tunnel
re-homing deferred? **Lean: include it — it is small, mirrors an existing pattern, and a
full-tunnel device losing its gateway is the harder outage this feature exists for.** Security
note: the carve-out is a single CP-IP pass rule (the CP is already the trust root the device
authenticates to over TLS); it does NOT widen the kill-switch's threat surface the way a broad
exception would. Held for ruling with D-WFA-1.

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
