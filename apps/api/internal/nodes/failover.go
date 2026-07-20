package nodes

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// S8.6 Slice 4 — the failover trigger. ONE CP-side ticker → ONE method → read freshness → update counts →
// derive the ACTIVE order → if it differs from the persisted set, that is a PROMOTION EVENT (persist, bump
// the generation, audit) which the ORDINARY compile+push carries. No failover-special messages, no new
// push path, no measuring (the tick READS node_peer_status freshness, the substrate stores). Fail-back is
// the SAME event in the other direction (a membership-order change back toward the configured intent).

const (
	// FailoverTickInterval is the CP tick cadence (rides the accesslog-sweep goroutine pattern).
	FailoverTickInterval = 30 * time.Second
	// failoverDemoteTicks (N) — consecutive stale ticks before a member is PROMOTED-PAST (fail-over FAST).
	failoverDemoteTicks = 3
	// failoverRestoreTicks (M > N) — consecutive fresh ticks (the hold window) before a demoted member is
	// RESTORED (fail-back SLOW). The asymmetry lives entirely in N vs M.
	failoverRestoreTicks = 5
	// failoverStaleWindow — a member with no handshake within this window is STALE for THIS tick. Reuses the
	// hub freshness idea; N ticks of it before demotion.
	failoverStaleWindow = 90 * time.Second
)

// The hub-set audit actions (S8.6 REDUCE #2, landing a — NAMED constants via the standard audit() path, the
// closest to "typed" available since the product has no audit-action registry; a real typed registry + lint
// is deferred to its own story). The promotion/failback kinds are the record of ACTIVE-ORDER transitions
// specifically (condition 1b); the membership kind is a CONFIGURED change (bind/unbind/pin), DISTINCT from a
// failover so an operator reading the log never confuses "I rebound a gateway" with "my primary went stale".
const (
	auditHubPromotion  = "hub_set.promotion"  // the controller DEMOTED a member (active-order transition, a loss)
	auditHubFailback   = "hub_set.failback"   // the controller RESTORED a member (active-order transition, recovery)
	auditHubMembership = "hub_set.membership" // ReconcileHubSet changed the CONFIGURED membership (not a failover)
)

// FailoverController holds ONE org's in-memory hysteresis state: per-member consecutive stale/fresh tick
// counts + the demoted set. NOT persisted — rebuilt from stored freshness on a CP restart, so a mid-window
// restart restarts the counts (delays a failover by ≤N ticks, NEVER causes a spurious one — the
// conservative stance; dormant-machinery's cousin: no persistence for state the substrate re-derives).
type FailoverController struct {
	n, m    int
	stale   map[uuid.UUID]int
	fresh   map[uuid.UUID]int
	demoted map[uuid.UUID]bool
}

// NewFailoverController builds a controller with the ruled thresholds (N=3 demote, M=5 restore).
func NewFailoverController() *FailoverController {
	return &FailoverController{
		n: failoverDemoteTicks, m: failoverRestoreTicks,
		stale: map[uuid.UUID]int{}, fresh: map[uuid.UUID]int{}, demoted: map[uuid.UUID]bool{},
	}
}

// Step advances ONE tick and returns the DEMOTED set (in configured order) — the members currently
// promoted-past for staleness. It updates the hysteresis counts from `freshness` (N consecutive stale →
// demote; M consecutive fresh → restore), then collects the demoted members. The ACTIVE order is NOT
// computed here: deriveActive(configured, demoted) is the ONE shared derivation every consumer applies (the
// REDUCE). Returning the demoted SET — not the active order — is what makes the controller a single-field
// writer: it persists ONLY `demoted`. Convergence: when NOTHING is demoted, deriveActive returns the
// configured order unchanged — fail-back IS that convergence.
func (fc *FailoverController) Step(configured []uuid.UUID, freshness map[uuid.UUID]bool) []uuid.UUID {
	for _, id := range configured {
		if freshness[id] {
			fc.fresh[id]++
			fc.stale[id] = 0
			if fc.demoted[id] && fc.fresh[id] >= fc.m {
				fc.demoted[id] = false // fail-back: M consecutive fresh (the hold window) restores it
			}
		} else {
			fc.stale[id]++
			fc.fresh[id] = 0
			if fc.stale[id] >= fc.n {
				fc.demoted[id] = true // promote-past: N consecutive stale
			}
		}
	}
	demoted := make([]uuid.UUID, 0)
	for _, id := range configured {
		if fc.demoted[id] {
			demoted = append(demoted, id)
		}
	}
	return demoted
}

func (s *Service) failoverFor(orgID uuid.UUID) *FailoverController {
	s.failoverMu.Lock()
	defer s.failoverMu.Unlock()
	fc := s.failovers[orgID]
	if fc == nil {
		fc = NewFailoverController()
		s.failovers[orgID] = fc
	}
	return fc
}

// sameOrder reports whether two member lists are element-wise identical (order matters — a reorder IS a
// promotion event).
func sameOrder(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RunFailoverTick is the ticker's method: for every org with a multi-member hub set, advance one failover
// tick. One org's error is logged and skipped — a single org must not stall the fleet.
func (s *Service) RunFailoverTick(ctx context.Context) error {
	orgs, err := s.q.ListFailoverOrgs(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, orgID := range orgs {
		if err := s.failoverOrg(ctx, orgID, now); err != nil {
			slog.WarnContext(ctx, "failover_tick_failed", "org_id", orgID.String(), "error", err.Error())
		}
	}
	return nil
}

// failoverOrg advances one org's tick: read the PERSISTED configured order (ReconcileHubSet's output — the
// intent) + each member's freshness (node_peer_status, Slice 1) → Step → the DEMOTED set. If it differs from
// the persisted demoted field, PERSIST it via the demoted-field writer (atomic generation bump) and AUDIT
// the active-order transition IN THE SAME TX (#5 — an audit failure rolls back the demotion; the next tick
// retries). The ordinary compile+push carries it (no failover-special path). Configured < 2 → nothing to
// fail over. The controller writes ONLY `demoted` (the writer partition — ReconcileHubSet owns `configured`).
func (s *Service) failoverOrg(ctx context.Context, orgID uuid.UUID, now time.Time) error {
	current, err := s.GetHubSet(ctx, orgID)
	if err != nil {
		return err
	}
	configured := current.Configured
	if len(configured) < 2 {
		return nil // single hub (or none) → no failover
	}
	// pubkeys for the configured members (from the live gateways). A configured member no longer a gateway
	// has no pubkey → no fresh handshake → it demotes; the next ReconcileHubSet drops it from configured.
	topo, err := s.siteTopoLoad(ctx, orgID)
	if err != nil {
		return err
	}
	pubkey := make(map[uuid.UUID]string, len(topo.gws))
	for i := range topo.gws {
		pubkey[topo.gws[i].ID] = topo.gws[i].WgPublicKey
	}
	// FRESHNESS — reads, never measures: a member is FRESH if a recent handshake exists with its pubkey
	// (someone reached it) in node_peer_status. Same latest-per-key fold the view uses (#8 shared helper).
	rows, err := s.q.ListNodePeerStatusForOrg(ctx, orgID)
	if err != nil {
		return err
	}
	latest := latestByPubKey(rows)
	freshness := make(map[uuid.UUID]bool, len(configured))
	for _, id := range configured {
		t := latest[pubkey[id]].LastHandshakeAt
		freshness[id] = !t.IsZero() && now.Sub(t) < failoverStaleWindow
	}

	demoted := s.failoverFor(orgID).Step(configured, freshness)
	if sameOrder(demoted, current.Demoted) {
		return nil // the demotion state is unchanged → no active-order transition
	}
	// The transition KIND (condition 1b): the demoted set SHRANK → a failback (recovery); else a promotion (a
	// member lost). old/new primary are DERIVED from the shared derivation over the old vs new demoted set.
	oldActive := deriveActive(configured, current.Demoted)
	newActive := deriveActive(configured, demoted)
	action, condition := auditHubPromotion, "primary_stale"
	if len(demoted) < len(current.Demoted) {
		action, condition = auditHubFailback, "recovered"
	}
	// System actor (no user) — a CP-driven transition (actor_system convention). Persist + audit in ONE tx.
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		row, err := q.UpsertOrgHubSetDemoted(ctx, sqlc.UpsertOrgHubSetDemotedParams{OrgID: orgID, Demoted: demoted})
		if err != nil {
			return err
		}
		return audit(ctx, q, orgID, nil, action, "org", orgID.String(), map[string]any{
			"old_primary": primaryOf(oldActive), "new_primary": primaryOf(newActive),
			"generation": row.Generation, "condition": condition,
		})
	})
}

// primaryOf is the head node-id of an ordered hub set as a string ("" when empty) — the audit's old/new
// primary field.
func primaryOf(order []uuid.UUID) string {
	if len(order) == 0 {
		return ""
	}
	return order[0].String()
}
