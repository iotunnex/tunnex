# WF-B — site-link health: name the peer + subordinate the demoted-dead link — commit-one

**Origin:** EPIC 8 smooth walk (2026-07-23, Test A) + the Deck-B rematch finding-2 + the Deck-D
harvest item (badge-names-which-peer). During a live failover, azure-gw showed a bare
**"site link down"** while transit was healthy via the promoted hub — the badge flagged the DEAD
DEMOTED aws-gw-1 keepalive peer as if the SITE were down, and never named which peer. Two truths
were collapsed into one misleading headline. **ONE paper** (founder-ruled): the naming half and
the subordination half share the SAME CP-side per-peer computation — splitting them ships the
data-model change twice.

## Current state (cited)

- `siteLinkDown := caps.SiteLinkStale` (`apps/api/internal/nodes/service.go:1364`) — an
  AGENT-REPORTED BOOL. Carries NO peer identity, NO demoted-flag. It is a THIRD freshness signal,
  independent of the failover controller's.
- The failover controller ALREADY derives per-member liveness: `latestByPubKey(ListNodePeer
  StatusForOrg)` (spoke-observed handshake freshness) ⋂ `deriveActive(configured, demoted)`
  (hub-set membership / who's demoted) — `apps/api/internal/nodes/failover.go` (failoverOrg +
  Step). This is the ONE liveness truth.
- Health kind: `KindSiteLinkDown` ranked as a site HEADLINE, above the policy apply/desync kinds
  (`apps/api/internal/nodes/policyhealth.go:148-150`, transitionTable :119).
- Web badge: bare "site link down" / danger tone (`apps/web/src/lib/healthview.ts:33-34`),
  rendered on the site card (`apps/web/src/lib/sitesview.ts`).

## D-WFB-1 (LEAD — founder-RULED) — the per-peer liveness SOURCE

**RULED (founder, strong prior): WF-B's per-peer health MUST consume the failover controller's
EXISTING liveness derivation (spoke-observed freshness ⋂ hub-set membership — the one-truth
census the controller already reads). It must NEVER compute a third freshness.** A health badge
disagreeing with the controller about who is stale is the two-truths class at the worst seam
(the failover surface). The agent-reported `caps.SiteLinkStale` bool is retired as the site-link
health source, OR demoted to a corroborating-only signal — the AUTHORITATIVE per-peer verdict is
the controller's.

Sub-item (the HOW, needs ruling): share by **(a) a shared PURE function** both the controller and
the health computation call (one derivation, two callers — literally one truth), OR **(b) the
controller PERSISTS its per-member verdict** and the health path reads it (one writer, the health
path never re-derives). **Lean: (a) shared pure function** — the derivation is already pure
(`deriveActive` + a freshness fold); extracting the per-member "is this member's link fresh, and
is it demoted" as one function keeps it clockless + unit-pinnable, and there is no persistence
race. (b) risks a staleness lag between the controller's tick and the health read.

## D-WFB-2 (needs ruling) — the data-model change

`SiteLinkDown bool` → must carry the DOWN-PEER IDENTITY + a DEMOTED flag, threaded
`KindInput` → the API health payload (OpenAPI codegen) → the web badge. Decide the wire shape:
- **(a)** a structured `site_link_status` sub-object: `{ down_peer_name, down_peer_demoted,
  transit_healthy }` — explicit, extensible.
- **(b)** flat fields on the existing health payload: `site_link_down_peer` (string, "" = none) +
  `site_link_down_peer_demoted` (bool).
**Lean: (b)** — minimal codegen surface, matches the existing flat health-field convention
(policy_degraded_kind et al.); the badge needs only name + demoted + the ranking already knows
transit health from the kind precedence (D-WFB-3).

## D-WFB-3 (needs ruling) — subordination precedence

When the down peer is a DEMOTED member AND transit flows via the active primary (its link fresh),
the demoted-dead link is a LINE ITEM, never the site's headline — the site's transit IS healthy,
and the rendering must not contradict the wire. Ruling: `degradedKind` (policyhealth.go) drops a
DEMOTED-member-only link-down BELOW `KindHealthy` for the site headline, surfacing it as a
distinct subordinate line. A NON-demoted (active-primary) link-down STAYS the headline (a real
failure must never be subordinated). This is the honest-by-design disposition from Deck-B
finding-2, now with the legibility fix it was owed.

## D-WFB-4 (RULED direction) — naming

The badge names the specific peer: **"site link down: aws-gw-1 (demoted)"** not a bare
site-level alarm. The Deck-D harvest item (badge-names-which-peer) folds in here — SAME render,
one fix. The `(demoted)` qualifier is the disambiguator that tells the operator "expected, this
is the failed-over-past member" vs "a live peer's link is actually down."

## Reds

1. **ACCEPTANCE (the walk's exact state):** promotion-in-effect + demoted-dead peer → the site
   shows a **transit-healthy headline** AND a **named-peer-down line item** ("aws-gw-1 (demoted)"),
   both truths DISTINCT and both TRUE.
2. **One-truth (the two-truths red):** the badge's "who is stale" == the failover controller's
   verdict for the same org/tick — they can NEVER disagree (D-WFB-1). A test drives a state where
   the retired agent-bool would have said otherwise and asserts the badge follows the controller.
3. **A real failure is NOT subordinated:** the ACTIVE primary's link dead (not a demoted member) →
   the site headline still reads site-link-down (D-WFB-3's guard).
4. **Naming:** the badge text carries the specific peer name + the demoted qualifier (D-WFB-4).
5. **Fixture fidelity:** the health test-double must not out-capability the substrate (the S8.2
   law) — it carries the same per-peer freshness + membership shape the controller reads.

**Sequence:** this paper → founder rulings on D-WFB-1(how)/D-WFB-2/D-WFB-3 → build (shared
liveness derivation + KindInput/API/codegen + web badge) → targeted review on the health surface
(the S7.4b/S8.2 most-reviewed class, UNDISCOUNTED) → box-walk the walk's exact failover state.
Own story, not a hotfix (data-model + reviewed-surface change).
