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

## D-WFA-5 (NEW — surfaced at WF-A build-start; HALT, needs ruling BEFORE code) — the device→gateway model

The ruling says "the device's config re-compiles to the promoted HUB." But the mint-path trace
shows a device is NOT necessarily on the hub: the client picks `activeNodeId` = **the FIRST active
node** (`apps/client/src/main/httpdeviceapi.ts:46-52`; web: `nodes[0].id`,
`apps/web/src/pages/Devices.tsx:80`), and the CP bakes the config endpoint = **that node's**
Endpoint (`apps/api/internal/devices/service.go:264`, `node.Endpoint`). In the walk the first
active gateway HAPPENED to be the hub (aws-gw-1), so the two coincided — but they are not the same
thing. Before building, RULE the model:

- **(A) devices attach to the ACTIVE HUB by construction.** The mint-path picks the active primary
  hub (not "first active"); the config endpoint is DERIVED from the active hub; re-home = follow the
  hub's promotion. Cleanest re-home story (one moving target — the hub), but changes the mint-path
  node selection AND makes a device's gateway a derived value, not a stored node_id. Question: what
  about an org with NO hub set (single gateway, unpinned)? → the sole/elected gateway, degenerate-fine.
- **(B) devices keep their assigned node_id; re-home fires only when THAT node is a hub-set member
  that gets demoted.** Minimal change to the mint model; but a device on a NON-hub gateway (a future
  per-site local device) never re-homes — is that in scope? For v1 (the walk = a device on the hub)
  it covers the case, but leaves a "device on a spoke gateway" gap unaddressed (register it).
- **(C) hybrid:** keep node_id, but on promotion re-point the config endpoint to the promoted hub
  when the device's node was the demoted primary. The device's "home" stays its node_id for
  identity/peer-placement, but its DIAL endpoint follows the active hub.

**RULED (founder): (C) — identity STAYS (node_id), the dial endpoint DERIVES.** node_id conflates
two facts: WHICH gateway holds the device's peer/identity (assignment — stable; revocation + audit
reference it) and WHERE the client dials (endpoint — situational; must follow the active hub). (A)
collapses them → re-mints identity every promotion, churns the home for a transport concern, makes
device history unreadable across failovers. (B) keeps them conflated + inherits the spoke gap. (C)
SEPARATES them: keep node_id as identity/assignment, derive the dial endpoint from the active hub
set at config-compile time.

**Conditions (ruled):**
- **Endpoint from the ONE-truth chain:** the device config endpoint = `deriveActive`'s HEAD (the
  active primary) — the SAME value the compiler threads to the data-plane graph + policy compiler.
  Not a second election, not a snapshot. RED: promotion → the device's next compiled config carries
  the promoted hub's endpoint, by construction of the shared value.
- **Devices-on-spokes gap STAYS REGISTERED** (not silently closed): (C) fixes a device whose dial
  target is a FAILED HUB. A device whose assigned node is a non-hub SPOKE that dies is a different
  scenario (no promotion event fires) — the named deferral stands, with (C)'s derivation noted as
  the seam a future fix extends.
- **Identity-stable RED:** a device's node_id, audit history, and revocation semantics are
  UNCHANGED by a promotion — the proof (C) didn't smuggle (A).

### D-WFA-5b (peer-hosting verify — VERIFIED, the companion rides this story)

**VERIFIED (cited): (C) is a HALF-FIX without a companion.** `ListActivePeersForNode`
(`apps/api/db/queries/devices.sql:164`, `WHERE d.node_id = $1`) scopes a device's peer to its
ASSIGNED node only. So a device with node_id = the demoted primary, dialing the PROMOTED hub's
derived endpoint, hits a hub whose peer set does NOT include it → **the handshake fails, (C) alone
is a half-fix.** COMPLETION (rides this story, same fix's other half): **a device assigned to a
hub-set member is compiled onto EVERY hub-set member's DesiredState** — warm on standbys exactly as
site-links are warm (the standby HOLDS the device peer so the dial lands on a hub that already knows
the device; the device's /32 AllowedIPs rides the active-primary recompile on promotion, mirroring
the site-link single-valued invariant). Sub-item: standby device-peer AllowedIPs — empty-warm
(pubkey only, /32 added on promotion) vs full — decide at build per the site-link precedent (lean:
empty-warm, single-valued, the promotion recompile adds the /32). RED: a device assigned to a
hub-set member appears in the promoted hub's peer set (the dial's handshake completes post-promotion).

**BUILD STATUS (CP tier DONE):** slice 1a `widenedDevicePeers` (`e18d4df`, DB-red PASS: primary /32,
standby empty-warm — the empty-warm sub-item ruled at build per the lean) + slice 1b
`activeHubDialFrom` pure primitive (`0807f3a`, red pins primary/standby→active-primary,
spoke→not-derived, promotion-follows). CP foundation is one-truth + gate-able standalone.

### D-WFA-6 (BANKED for slice 2 — the dial-endpoint CHANNEL) — RULED direction: ride routed-ranges

The dial-endpoint the client polls to re-home is the THIRD volatile-operational client channel
(revocation → routed-ranges → dial-endpoint). **RULED (founder lean, strong): FOLD it into the
EXISTING routed-ranges channel — one poll, more fields — NOT a third poller.** The endpoint is
volatile-operational EXACTLY like routed-ranges (same auth class = revocation-poll, same fail-static
semantics, same cadence); three independent pollers = three cadences, three failure modes, three
fail-static implementations — the one-truth law applied to the client's control plane. Argue for a
separate channel ONLY if the shapes genuinely differ at build; otherwise the endpoint is a field on
the routed-ranges response (`sites.ListRoutedRanges`/the client `RoutedRangesMonitor`), derived
CP-side via `activeHubDialFrom`. NOTE: routed-ranges is ORG-scoped but the dial endpoint is
DEVICE-scoped (depends on the device's node_id + hub set) — verify at build whether the channel
carries device-scoped state cleanly, or the endpoint needs the device context threaded (it may make
the channel device-scoped, which is fine — the client polls for ITS device).

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

## D-WFA-1 (RULED — CP-driven, locked) — the re-home MECHANISM

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

**RULED (founder) — CP-driven re-homing, LOCKED.** On promotion, the device's config re-compiles
to the promoted hub (endpoint + the AllowedIPs the device already holds), rides the EXISTING
promotion→compile→push path, the client applies via the ordinary reconcile. Zero election logic
in the client; observe-never-vote preserved end to end. Conditions:
- **(a) promotion-triggered, NOT health-triggered.** The client NEVER decides its gateway is dead
  — the CP's failover controller (the ONE liveness truth) decides, and the device follows the
  SAME verdict the spokes follow. No client-side liveness opinion enters.
- **(b) the re-home is AUDITED on the same generation machinery** as everything else — a device
  silently hopping gateways is the two-truths class; the audit row IS the truth.
- **(c) fail-back re-homes the device home the SAME way** — one mechanism, both directions (the
  device-tier echo of the D4 hysteresis; failback promotion re-compiles the device onto the
  restored primary).

## D-WFA-4 (RULED — IN for v1) — full-tunnel CP-endpoint carve-out

Full-tunnel's kill-switch blocks the control channel (D-WFA-0). For CP-driven re-homing to work
in full-tunnel, the kill-switch must permit egress to the CP endpoint.

**RULED (founder) — IN for v1.** Shipping split-only would give the MOST security-conscious
configuration (kill-switch on) the WORST HA — backwards; the full-tunnel outage is exactly the
one a kill-switch user bought the product for. Terms:
- ONE CP-endpoint pass rule, mirroring the existing WG-endpoint host-route pattern
  (`backend_darwin.go:126-150,301-323` — precedent cited, mechanism proven), scoped to the CP's
  IP/port EXACTLY.
- **Threat argument (one line, founder-framed):** the CP is already the TLS trust root the device
  authenticates to — a pass rule to it widens NOTHING; the client still authenticates everything
  it receives. Not a broad exception; a single named carve-out.

## D-WFA-2 (RULED direction) — re-home rides the generation/audit machinery

A device silently hopping gateways is the two-truths class. The re-home MUST carry a
generation bump + an audit event (`device.rehomed` or rides `hub_set.promotion`'s consequence),
same as every other state transition this epic. No silent endpoint swap.

## D-WFA-3 — failback symmetry

When the original hub recovers and reclaims PRIMARY (M=5 fresh), does the re-homed device
return to it, or stay on the standby-now-primary? v1 default: follow the active primary (the
device tracks the hub set's members[0]), same rule as the site-link graph — consistent, no
device-special path. State explicitly.

## Reds (RULED — the acceptance set)

1. **ACCEPTANCE (the walk's EXACT fixture, CLOCKED):** connected device on the primary → gateway
   killed → promotion → the device's config re-homes within ONE push cycle → tunnel re-establishes
   to the promoted hub → traffic resumes — WITHOUT a manual reconnect. Stopwatch the re-home; this
   timeline joins the 4m48s failover as the demo's SECOND number.
2. **Run RED 1 in BOTH modes** — split AND full re-home IDENTICALLY (the D-WFA-4 carve-out makes
   full-tunnel reach the CP; the re-home path is otherwise one mechanism).
3. **Promotion-triggered, not health-triggered (D-WFA-1a):** the client emits NO liveness verdict;
   the re-home fires only from the CP controller's promotion. A test that flaps a gateway's
   data-plane WITHOUT a promotion must NOT re-home the device.
4. **Generation/audit (D-WFA-1b/D-WFA-2):** the re-home emits its audit event on the same
   generation machinery; no silent hop.
5. **Fail-back (D-WFA-1c/D-WFA-3):** original hub reclaims (M=5 fresh) → device re-homes back the
   same way; one mechanism both directions.
6. **Kill-switch invariant (D-WFA-4):** full-tunnel + kill-switch armed → CP reachable (carve-out
   live) AND everything else still dropped — the block-all invariant re-verified MINUS exactly one
   new named exception (the kill-switch's own reds re-run with the carve-out present).

**Sequence:** this paper (RULED) → build as its OWN story (compiler device-config path + client
reconcile + helper CP-endpoint carve-out) → targeted review on the peer-model + kill-switch
surfaces (both most-reviewed classes, UNDISCOUNTED) → box-walk: the walk's exact fixture, BOTH
tunnel modes, stopwatch on the re-home. NOT a hotfix — a story.
