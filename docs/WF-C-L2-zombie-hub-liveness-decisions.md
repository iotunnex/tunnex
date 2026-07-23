# WF-C Layer 2 — zombie-hub liveness — commit-one (decision-first, HELD for ruling)

**Builds on** [WF-C characterization](WF-C-orphaned-wg0-characterization.md) (Layer 1 shipped:
`WGBackend.Close` = `ip link del wg0`, deferred on shutdown — `52e3f7e`). Layer 1 closed the
GRACEFUL-stop leak. Layer 2 is the HARD-crash zombie (SIGKILL / OOM / kernel panic skips the
defer): wg0 survives in the host netns and keeps forwarding **headless**. Nothing built. This
paper frames the decide-item and holds for the founder's ruling.

## Opening statement — the hazard is stale POLICY ENFORCEMENT, not just stale forwarding

The characterization called a zombie hub "degraded-not-dead — it forwards but can't reconcile."
That undersells it. A headless wg0 keeps enforcing the **last-compiled policy artifact** (its wg0
peer set + `nft` grant tables) frozen at the instant the agent died. Meanwhile the control plane
has moved on. So the real exposure is a **two-truths policy gap**:

- An admin **revokes a device** → the CP sweeps the peer + pool address + audits it, and *believes*
  the device is off. The zombie hub never got the reconcile, so its wg0 still holds that peer — the
  revoked device can **still transit** through the zombie.
- An admin **tightens a ZTNA rule** (removes a grant, narrows a subject) → the CP compiles the new
  default-deny artifact. The zombie enforces the OLD grant. Access the org intended to cut **persists**.
- The CP's health surface may still show the hub's **data-plane handshake fresh** → failover stays
  blind, and an operator reading the console sees a working hub, not a stale-enforcing one.

This is not a liveness nicety; it is an **enforcement-freshness** question. The severity is bounded
by Tunnex's OTHER enforcement points (below), but the class — "a node enforces a policy the control
plane has since changed, and neither side knows" — is exactly the two-truths class this epic has
fought everywhere else. It must be dispositioned, not documented away by reflex.

### What already bounds the severity (state honestly, so the ruling weighs the RESIDUAL, not the worst case)

- **Device revocation is multi-point.** The revoked device's OWN client tears its tunnel down on
  the RevocationMonitor's poll (S6.4) — it stops dialing the zombie regardless of the zombie's stale
  peer. So the highest-frequency case (revoke a laptop) self-heals from the client side within a poll
  cycle, zombie or not.
- **Site-to-site is both-enforce (A3b / D-A3b-2).** A de-authorized device→site path is grant-gated
  on the device node AND the hub AND the destination gateway. A zombie hub with a stale grant is one
  of three enforcement points; the far gateway (a different box, reconciling normally) still denies.
- **The zombie can't grow.** It serves only peers it already had — it cannot enroll a NEW device or
  widen to a NEW grant. The gap is strictly "fails to REMOVE," never "adds."

**The residual after those:** a policy TIGHTENING whose sole enforcement point is the zombie hub —
e.g. a ZTNA rule narrowing spoke-to-spoke access THROUGH that hub, where the client isn't revoked
(so no client-side teardown) and there's no second gateway in the path (so no both-enforce). That
residual is real but narrow. The ruling is: is that residual worth failover-controller complexity,
a deploy-architecture change, or an accept-with-named-limits.

## D-WFC2-1 (the decide-item) — the liveness model

### (a) accept-by-design + document + surface

A headless-forwarding hub is degraded-not-dead; failover intentionally does NOT fire on a working
tunnel (the whole point of the data-plane-driven model — don't churn transit off a live path). Make
the zombie **legible**: the site card surfaces the node "offline" (agent `last_seen` stale) even
while its link shows data flowing — an explicit "forwarding but not reconciling" state, never a
reassuring green (the reassuring-empty law). Document the enforcement-freshness residual + the three
mitigations above as the operator's known limit.
- **COST:** the narrow residual (sole-enforcement ZTNA tightening) stays open; mitigation is
  operator-driven (they see "offline" and restart/replace the node).
- **Honest:** this is what ships TODAY minus the surfacing. It is the floor, not a non-answer.

### (b) container-netns for wg0

Run wg0 in the container's OWN netns so it dies with the container on ANY exit (SIGKILL included) —
the zombie can't exist. **COST:** the gateway needs host-LAN forwarding, which is the entire reason
`--network host` is used; a netns move means explicit veth/forwarding plumbing + a deploy-architecture
change + a re-walk of every gateway-forwarding path (site transit, egress NAT, DNS forwarder). Large,
and it re-opens surfaces this epic just closed. It eliminates the class rather than mitigating it.

### (c) two-signal liveness (failover considers agent heartbeat)

The failover controller demotes a hub whose AGENT is stale (`last_seen` beyond a threshold) even if
its data-plane handshake is fresh — a control-plane liveness input ALONGSIDE the data-plane one. A
dead agent → demote → transit + devices re-home (WF-A) to a live hub that CAN reconcile. **COST:**
touches the failover controller (the most-reviewed surface of this epic) and RISKS a false demotion
— a transient agent restart (agent down 30s, wg0 forwarding fine) must NOT churn transit. So (c)
needs its OWN hysteresis (agent stale for N ticks, mirroring the data-plane 240s window) and its own
"agent-stale" demotion cause distinct from "data-plane-down" — real controller complexity, and it
must not let a control-plane blip demote a healthy hub (the churn the 240s window exists to avoid).

## MY LEAN (for the ruling, NOT ruled)

(a) as the floor + (c) as the REAL fix for the residual, registered with a named trigger — NOT (b).

Reasoning: the hazard is genuine (stale enforcement, two-truths), so pure "accept + document" leaves
a security-shaped residual I don't want silent. But the residual is NARROW (device-revocation and
site-to-site are already covered by client teardown + both-enforce), so it does not justify (b)'s
deploy-architecture blast radius or an urgent (c) build. (c) is the correct eventual fix BECAUSE it
closes the residual at the ONE truth (the failover controller — the same liveness authority WF-A/WF-B
already consume), rather than adding a fourth liveness opinion. Its cost (false-demotion churn) is
managed by the same hysteresis pattern already proven (N-stale-to-demote / M-fresh-to-failback). So:
ship (a)'s surfacing now if cheap; register (c) with **TRIGGER = the residual becomes load-bearing**
— i.e. a customer runs hub-sole-enforcement ZTNA tightening, OR a security review flags the freshness
gap as beta-blocking. (b) stays rejected-with-rationale in this paper (findable later).

## Reds (for the eventual build — whichever option is ruled)

- **(a):** the site card renders "offline / forwarding-not-reconciling" when node `last_seen` is
  stale AND the link handshake is fresh — an explicit third state, never green, never a bare "up".
  A red that a stale-agent + fresh-link node does NOT render as healthy.
- **(c) if ruled:** a hub whose agent `last_seen` exceeds the agent-stale window (N ticks) is demoted
  with an `agent_stale` cause EVEN with a fresh data-plane handshake; a hub whose agent restarts
  within the window is NOT demoted (no false churn); the demotion re-homes devices (WF-A) + moves
  transit; failback on agent-fresh (M ticks) mirrors the data-plane path. The demotion is ONE
  controller decision on the ONE liveness authority — no new freshness clock (the WF-B lesson:
  consume the controller's derivation, never mint a third).
- **(b) if ruled:** wg0 in the container netns dies on SIGKILL (a red that kills -9 the agent and
  asserts wg0 gone from the host netns); every gateway-forwarding path (transit, egress, DNS) re-walked.

## Sequence

This paper (HELD) → founder ruling on D-WFC2-1 → build the ruled option decision-first (reds + walk
where a wire proof applies; (a) is UI-surfacing, (c)/(b) are wire-provable) → the surfacing/controller
change re-earns a targeted review only if it touches the failover controller (option c). Until ruled:
nothing built. Layer 1 (`52e3f7e`) already closed the graceful-stop leak — the walk's original
"docker stop didn't fail over" confusion is gone; this is only the hard-crash residual.
