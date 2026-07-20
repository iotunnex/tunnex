package nodes

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// TestFailoverConvergence (S8.6 D5 banked invariant) — active differs from configured ONLY while a member
// is demoted; all-fresh converges (fail-back IS the convergence).
func TestFailoverConvergence(t *testing.T) {
	fc := NewFailoverController()
	p, s := idAt(1), idAt(2)
	cfg := []uuid.UUID{p, s}
	fresh := map[uuid.UUID]bool{p: true, s: true}
	stale := map[uuid.UUID]bool{p: false, s: true}

	// Step now returns the DEMOTED SET (the reduce); the ACTIVE order is deriveActive(cfg, demoted).
	for i := 0; i < 10; i++ {
		if got := fc.Step(cfg, fresh); len(got) != 0 {
			t.Fatalf("all-fresh must demote NOTHING (active == configured), got demoted %v", got)
		}
	}
	var demoted []uuid.UUID
	for i := 0; i < failoverDemoteTicks; i++ {
		demoted = fc.Step(cfg, stale)
	}
	if !sameOrder(deriveActive(cfg, demoted), []uuid.UUID{s, p}) {
		t.Fatalf("N stale ticks → primary demoted (active=[standby,primary]), got demoted=%v", demoted)
	}
	for i := 0; i < failoverRestoreTicks; i++ {
		demoted = fc.Step(cfg, fresh)
	}
	if len(demoted) != 0 || !sameOrder(deriveActive(cfg, demoted), cfg) {
		t.Fatalf("M fresh ticks → restored, active CONVERGES to configured (fail-back), got demoted=%v", demoted)
	}
}

// TestFailoverFlapExactlyOne (the flap red) — an oscillating primary produces EXACTLY ONE failover, not a
// metronome: N=3 demotes once, then the M=5 restore-hold + count-reset-on-flip keep the order stable.
func TestFailoverFlapExactlyOne(t *testing.T) {
	fc := NewFailoverController()
	p, s := idAt(1), idAt(2)
	cfg := []uuid.UUID{p, s}
	fresh := map[uuid.UUID]bool{p: true, s: true}
	stale := map[uuid.UUID]bool{p: false, s: true}

	changes := 0
	var prev []uuid.UUID // the demoted set (starts empty — nothing demoted)
	tick := func(f map[uuid.UUID]bool) {
		d := fc.Step(cfg, f)
		if !sameOrder(d, prev) {
			changes++
			prev = append([]uuid.UUID(nil), d...)
		}
	}
	for i := 0; i < failoverDemoteTicks; i++ {
		tick(stale) // demote → demoted set becomes [p]: change #1
	}
	for i := 0; i < 20; i++ { // FLAP: fresh,stale,fresh,stale... — never M-consecutive-fresh, so no fail-back churn
		if i%2 == 0 {
			tick(fresh)
		} else {
			tick(stale)
		}
	}
	if changes != 1 {
		t.Fatalf("an oscillating primary must produce EXACTLY ONE failover, not a metronome, got %d", changes)
	}
}

// TestFailoverRestartConservative — a CP restart (fresh controller) restarts the counts, so a mid-window
// primary needs N ticks AGAIN: a restart delays a failover by ≤N, NEVER causes a spurious immediate one.
func TestFailoverRestartConservative(t *testing.T) {
	p, s := idAt(1), idAt(2)
	cfg := []uuid.UUID{p, s}
	stale := map[uuid.UUID]bool{p: false, s: true}

	fc := NewFailoverController()
	for i := 0; i < failoverDemoteTicks-1; i++ {
		fc.Step(cfg, stale) // 2 stale ticks — one short of demotion
	}
	fc2 := NewFailoverController() // RESTART
	if d := fc2.Step(cfg, stale); len(d) != 0 {
		t.Fatalf("a restart must NOT demote on the first post-restart tick (conservative), got demoted %v", d)
	}
}

// TestFailoverDemotedPrimaryInert (S8.6 D5 structural red) — post-promotion compile: the demoted primary's
// pubkey appears in ZERO spokes' subnet-carrying AllowedIPs (it persists as a keepalive-only peer — the warm
// fail-back line). The promoted standby carries the subnets.
func TestFailoverDemotedPrimaryInert(t *testing.T) {
	fresh := time.Now()
	awsSite, azureSite := idAt(0xA), idAt(0xB)
	awsGw := gw(1, "aws:51820", "KAWS", pri(1), &fresh)
	awsGw.SiteID = pgtype.UUID{Bytes: awsSite, Valid: true}
	awsStandby := gw(2, "aws2:51820", "KAWS2", pri(2), &fresh)
	awsStandby.SiteID = pgtype.UUID{Bytes: awsSite, Valid: true}
	azureGw := gw(3, "azure:51820", "KAZ", nil, &fresh)
	azureGw.SiteID = pgtype.UUID{Bytes: azureSite, Valid: true}
	topo := siteTopology{
		gws:     []sqlc.ListSiteGatewaysForOrgRow{awsGw, awsStandby, azureGw},
		subnets: map[uuid.UUID][]string{awsSite: {"172.31.0.0/16"}, azureSite: {"10.0.0.0/16"}},
		// ACTIVE order AFTER failover: standby promoted to members[0], the stale primary demoted to the back.
		hubMembers: []sqlc.ListSiteGatewaysForOrgRow{awsStandby, awsGw},
	}
	azurePeers, _ := siteLinkGraphFrom(topo, sqlc.Node{ID: azureGw.ID, SiteID: azureGw.SiteID})
	if p := peerByKey(azurePeers, "KAWS2"); p == nil || len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != "172.31.0.0/16" {
		t.Fatalf("the PROMOTED standby must now carry the AWS subnets, got %+v", p)
	}
	if p := peerByKey(azurePeers, "KAWS"); p == nil || len(p.AllowedIPs) != 0 {
		t.Fatalf("the DEMOTED primary must be INERT (keepalive-only, ZERO subnet-carrying AllowedIPs), got %+v", p)
	}
}

// TestOrgHubSetGenerationFence (S8.6 D5 CP-side fence, explicit) — the atomic UpsertOrgHubSet bump: a change
// to `members` bumps the generation monotonically; an unchanged set does NOT (the CASE ... IS DISTINCT FROM
// statement in nodes.sql). Concurrent writers converge to ONE monotonic result — the fence's whole job.
func TestOrgHubSetGenerationFence(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	q := sqlc.New(pool)
	org := uuid.New()
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)", org, "gf-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id=$1", org) })

	a, b := uuid.New(), uuid.New()
	up := func(configured []uuid.UUID) (int64, error) {
		hs, e := q.UpsertOrgHubSetConfigured(ctx, sqlc.UpsertOrgHubSetConfiguredParams{OrgID: org, Configured: configured})
		return hs.Generation, e
	}
	g1, _ := up([]uuid.UUID{a, b})
	g2, _ := up([]uuid.UUID{a, b}) // SAME → no bump
	if g2 != g1 {
		t.Fatalf("an unchanged set must NOT bump the generation: %d -> %d", g1, g2)
	}
	g3, _ := up([]uuid.UUID{b, a}) // REORDER (a membership change) → bump
	if g3 <= g2 {
		t.Fatalf("a reorder must bump the generation (the fence): %d -> %d", g2, g3)
	}

	// CONCURRENT writers, different member views → one monotonic result, no lost bump, no torn row.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, m := range [][]uuid.UUID{{a, b}, {b, a}} {
		wg.Add(1)
		go func(m []uuid.UUID) {
			defer wg.Done()
			<-start
			_, _ = up(m)
		}(m)
		_ = i
	}
	close(start)
	wg.Wait()
	final, _ := q.GetOrgHubSet(ctx, org)
	if final.Generation < g3 {
		t.Fatalf("concurrent writes must leave a MONOTONIC generation (never regress), got %d < %d", final.Generation, g3)
	}
	if len(final.Configured) != 2 {
		t.Fatalf("the row must be intact (no torn write), got %v", final.Configured)
	}
}

// TestFailoverPromotionAudits (S8.6 Slice 4 end-to-end + the audit red) — a pinned primary that reads STALE
// for N ticks is demoted: org_hub_set becomes [standby, primary] and a hub_set.promotion audit row lands
// naming old→new primary + the condition. Fewer than N ticks does NOT promote (the hysteresis).
func TestFailoverPromotionAudits(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	org := uuid.New()
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'O',$2,'10.99.0.0/24')", org, "fa-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM nodes WHERE org_id=$1", org)
		_, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id=$1", org)
	})
	site := uuid.New()
	if _, e := pool.Exec(ctx, "INSERT INTO sites (id,org_id,name) VALUES ($1,$2,'s')", site, org); e != nil {
		t.Fatalf("seed site: %v", e)
	}
	primary, standby := uuid.New(), uuid.New()
	mk := func(id uuid.UUID, name, key, endpoint string, prio int) {
		if _, e := pool.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,site_id,wg_public_key,endpoint,hub_priority) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
			id, org, name, "cs-"+id.String()[:8], site, key, endpoint, prio); e != nil {
			t.Fatalf("seed %s: %v", name, e)
		}
	}
	mk(primary, "primary", "KP", "p:51820", 1) // pin 1 → configured primary
	mk(standby, "standby", "KS", "s:51820", 2) // pin 2 → configured standby

	svc := NewService(pool, nil, nil)
	// Reconcile the configured set first: org_hub_set = [primary, standby].
	if _, e := svc.ReconcileHubSet(ctx, org); e != nil {
		t.Fatalf("reconcile: %v", e)
	}
	// Freshness: the STANDBY is fresh (recent handshake with KS); the PRIMARY is STALE (old handshake with KP).
	now := time.Now()
	if _, e := pool.Exec(ctx, "INSERT INTO node_peer_status (node_id,public_key,last_handshake_at) VALUES ($1,'KS',$2)", primary, now); e != nil {
		t.Fatalf("seed fresh standby: %v", e)
	}
	if _, e := pool.Exec(ctx, "INSERT INTO node_peer_status (node_id,public_key,last_handshake_at) VALUES ($1,'KP',$2)", standby, now.Add(-10*time.Minute)); e != nil {
		t.Fatalf("seed stale primary: %v", e)
	}

	// N-1 ticks: NOT yet promoted (hysteresis).
	for i := 0; i < failoverDemoteTicks-1; i++ {
		if e := svc.failoverOrg(ctx, org, time.Now()); e != nil {
			t.Fatalf("tick %d: %v", i, e)
		}
	}
	hs, _ := svc.GetHubSet(ctx, org)
	if hs.Active()[0] != primary {
		t.Fatalf("before N ticks the primary must still be members[0] (hysteresis), got %v", hs.Active())
	}
	// The Nth stale tick → DEMOTE.
	if e := svc.failoverOrg(ctx, org, time.Now()); e != nil {
		t.Fatalf("Nth tick: %v", e)
	}
	hs, _ = svc.GetHubSet(ctx, org)
	act := hs.Active()
	if len(act) != 2 || act[0] != standby || act[1] != primary {
		t.Fatalf("after N stale ticks the standby must be promoted (active=[standby,primary]), got %v", act)
	}
	// The transition is AUDITED (old→new primary + condition).
	var action, oldP, newP, cond string
	e := pool.QueryRow(ctx,
		"SELECT action, metadata->>'old_primary', metadata->>'new_primary', metadata->>'condition' FROM audit_logs WHERE org_id=$1 AND action='hub_set.promotion'",
		org).Scan(&action, &oldP, &newP, &cond)
	if e != nil {
		t.Fatalf("a promotion must land an audit row: %v", e)
	}
	if oldP != primary.String() || newP != standby.String() || cond != "primary_stale" {
		t.Fatalf("audit must name old→new + condition, got old=%s new=%s cond=%s", oldP, newP, cond)
	}
}

// TestFailoverRehydratesDemotionOnRestart — S8.6 #1: a fresh controller (a CP restart) rehydrates the
// persisted demotion set BEFORE its first Step, so a still-stale demoted primary is NOT spuriously restored
// on the first tick — no blackhole window. seedDemoted is idempotent (runs once).
func TestFailoverRehydratesDemotionOnRestart(t *testing.T) {
	p, s := idAt(1), idAt(2)
	cfg := []uuid.UUID{p, s}
	stale := map[uuid.UUID]bool{p: false, s: true} // the primary is STILL stale after the restart

	fc := NewFailoverController() // fresh = a restart (counters zeroed)
	fc.seedDemoted([]uuid.UUID{p}) // the persisted demoted=[p] is rehydrated
	demoted := fc.Step(cfg, stale)
	if !sameOrder(demoted, []uuid.UUID{p}) {
		t.Fatalf("a rehydrated demotion must PERSIST on the first post-restart tick (no spurious restore), got %v", demoted)
	}
	if !sameOrder(deriveActive(cfg, demoted), []uuid.UUID{s, p}) {
		t.Fatalf("the active order must keep the standby as primary post-restart, got %v", deriveActive(cfg, demoted))
	}
	fc.seedDemoted([]uuid.UUID{p, s}) // a later seed is a no-op (idempotent)
	if fc.demoted[s] {
		t.Fatal("seedDemoted must run ONCE — a later seed must not add members")
	}
}
