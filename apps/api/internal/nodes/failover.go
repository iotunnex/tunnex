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

// Step advances ONE tick and returns the ACTIVE order. It updates the counts from `freshness`, then derives
// active = the CONFIGURED order with demoted members moved to the BACK (skipped for primary, kept as warm
// standbys). Convergence (banked invariant): active differs from configured ONLY while some member is
// demoted; when NOTHING is demoted (all restored) the orders CONVERGE — fail-back IS that convergence. Two
// boring passes: count, then split.
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
	live := make([]uuid.UUID, 0, len(configured))
	dead := make([]uuid.UUID, 0)
	for _, id := range configured {
		if fc.demoted[id] {
			dead = append(dead, id)
		} else {
			live = append(live, id)
		}
	}
	return append(live, dead...)
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

// failoverOrg advances one org's tick: read the CONFIGURED order (electSiteHubSet — the intent) + each
// member's freshness (node_peer_status, Slice 1) → Step → the ACTIVE order. If it differs from the persisted
// set, PERSIST it (atomic generation bump), AUDIT the transition (old→new primary + condition), and let the
// ordinary compile+push carry it. No standby (configured < 2) → nothing to fail over.
func (s *Service) failoverOrg(ctx context.Context, orgID uuid.UUID, now time.Time) error {
	topo, err := s.siteTopoLoad(ctx, orgID)
	if err != nil {
		return err
	}
	configuredRows := electSiteHubSet(topo, now)
	if len(configuredRows) < 2 {
		return nil // single hub (or none) → no failover
	}
	configured := make([]uuid.UUID, len(configuredRows))
	pubkey := make(map[uuid.UUID]string, len(configuredRows))
	for i := range configuredRows {
		configured[i] = configuredRows[i].ID
		pubkey[configuredRows[i].ID] = configuredRows[i].WgPublicKey
	}
	// FRESHNESS — reads, never measures: a member is FRESH if a recent handshake exists with its pubkey
	// (someone reached it) in node_peer_status.
	rows, err := s.q.ListNodePeerStatusForOrg(ctx, orgID)
	if err != nil {
		return err
	}
	freshestByKey := map[string]time.Time{}
	for _, r := range rows {
		if r.LastHandshakeAt.Valid && r.LastHandshakeAt.Time.After(freshestByKey[r.PublicKey]) {
			freshestByKey[r.PublicKey] = r.LastHandshakeAt.Time
		}
	}
	freshness := make(map[uuid.UUID]bool, len(configured))
	for id, key := range pubkey {
		t := freshestByKey[key]
		freshness[id] = !t.IsZero() && now.Sub(t) < failoverStaleWindow
	}

	active := s.failoverFor(orgID).Step(configured, freshness)

	current, err := s.GetHubSet(ctx, orgID)
	if err != nil {
		return err
	}
	if sameOrder(active, current.Members) {
		return nil // no change → no promotion event
	}
	// PROMOTION EVENT — persist the active order (atomic generation bump), then audit the transition.
	newHS, err := s.q.UpsertOrgHubSet(ctx, sqlc.UpsertOrgHubSetParams{OrgID: orgID, Members: active})
	if err != nil {
		return err
	}
	oldPrimary, newPrimary := "", ""
	if len(current.Members) > 0 {
		oldPrimary = current.Members[0].String()
	}
	if len(active) > 0 {
		newPrimary = active[0].String()
	}
	condition := "primary_stale"
	if sameOrder(active, configured) {
		condition = "recovered" // active converged to the configured intent → fail-back / restoration
	}
	// System actor (no user) — a CP-driven transition (actor_system convention). Audited so an operator can
	// answer "why did my hub change at 3am".
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		return audit(ctx, q, orgID, nil, "hub_set.promotion", "org", orgID.String(), map[string]any{
			"old_primary": oldPrimary, "new_primary": newPrimary,
			"generation": newHS.Generation, "condition": condition,
		})
	})
}
