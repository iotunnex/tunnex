package nodes

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

func idAt(n byte) uuid.UUID { return uuid.UUID{0: n} }

func gw(n byte, endpoint, wgKey string, pri *int32, seen *time.Time) sqlc.ListSiteGatewaysForOrgRow {
	r := sqlc.ListSiteGatewaysForOrgRow{ID: idAt(n), Endpoint: endpoint, WgPublicKey: wgKey, HubPriority: pri}
	if seen != nil {
		r.LastSeenAt = pgtype.Timestamptz{Time: *seen, Valid: true}
	}
	return r
}

func ids(set []sqlc.ListSiteGatewaysForOrgRow) []byte {
	out := make([]byte, len(set))
	for i := range set {
		out[i] = set[i].ID[0]
	}
	return out
}

func pri(v int32) *int32 { return &v }

// electSiteHubSet ordering reds (S8.6 D1) — PURE, no DB.
func TestElectSiteHubSetOrdering(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fresh := now.Add(-10 * time.Second) // within hubStaleWindow
	stale := now.Add(-10 * time.Minute) // well past it

	// CAPABILITY GATE: no endpoint (NAT'd) OR no wg key is EXCLUDED — the only membership criterion.
	t.Run("capability gate excludes NAT'd and keyless", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(1, "", "K1", nil, &fresh),              // NAT'd (no endpoint) → out
			gw(2, "1.2.3.4:51820", "", nil, &fresh),   // no wg key → out
			gw(3, "1.2.3.5:51820", "K3", nil, &fresh), // capable → in
		}}
		if got := ids(electSiteHubSet(topo, now)); len(got) != 1 || got[0] != 3 {
			t.Fatalf("only the capable gateway may enter, got %v", got)
		}
	})

	// TWO CAPABLE, NO PINS, EQUAL HEALTH → id order deterministic.
	t.Run("no pins equal health → id order", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(5, "h:1", "K5", nil, &fresh),
			gw(2, "h:1", "K2", nil, &fresh),
			gw(9, "h:1", "K9", nil, &fresh),
		}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{2, 5, 9}) {
			t.Fatalf("id order expected [2 5 9], got %v", got)
		}
	})

	// PIN OUTRANKS a HEALTHIER unpinned candidate (operators outrank magic).
	t.Run("pin outranks healthier unpinned", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(7, "h:1", "K7", nil, &fresh),    // unpinned, FRESH, low-ish id
			gw(3, "h:1", "K3", pri(1), &stale), // PINNED but STALE and higher id
		}}
		if got := ids(electSiteHubSet(topo, now)); got[0] != 3 {
			t.Fatalf("the PINNED (stale) gateway must be primary over a healthier unpinned one, got %v", got)
		}
	})

	// HEALTH orders the unpinned: fresh before stale.
	t.Run("health orders unpinned (fresh before stale)", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(2, "h:1", "K2", nil, &stale), // lower id but STALE
			gw(8, "h:1", "K8", nil, &fresh), // higher id but FRESH
		}}
		if got := ids(electSiteHubSet(topo, now)); got[0] != 8 {
			t.Fatalf("a FRESH gateway outranks a stale lower-id one (health > id), got %v", got)
		}
	})

	// DIFFERENT-SITE gateway enters in correct order — capability is the ONLY membership criterion (an
	// org can fail over across sites; the standby need not share the primary's site).
	t.Run("a capable gateway from a different site enters in order", func(t *testing.T) {
		siteA, siteB := pgtype.UUID{Bytes: idAt(0xA), Valid: true}, pgtype.UUID{Bytes: idAt(0xB), Valid: true}
		g1 := gw(4, "h:1", "K4", nil, &fresh)
		g1.SiteID = siteA
		g2 := gw(6, "h:1", "K6", nil, &fresh)
		g2.SiteID = siteB // DIFFERENT site
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{g1, g2}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{4, 6}) {
			t.Fatalf("the cross-site capable gateway must enter the set in id order, got %v", got)
		}
	})
}

// TestReconcileHubSetGeneration (S8.6 D5) — the persisted set + the fencing generation: bumps ONLY on a
// membership/order change (idempotent reconcile never bumps), survives a "restart" (a fresh Service over
// the same DB reads the same generation, never reset).
func TestReconcileHubSetGeneration(t *testing.T) {
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
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)", org, "hs-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	actor := uuid.New()
	if _, e := pool.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,'U')", actor, actor.String()+"@t"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	site := uuid.New()
	if _, e := pool.Exec(ctx, "INSERT INTO sites (id,org_id,name) VALUES ($1,$2,'s')", site, org); e != nil {
		t.Fatalf("seed site: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM nodes WHERE org_id=$1", org)
		_, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id=$1", org)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", actor)
	})
	// RANDOM ids (no cross-run collision); mkGw returns the id so assertions use captured values.
	mkGw := func(key string) uuid.UUID {
		id := uuid.New()
		if _, e := pool.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,site_id,wg_public_key,endpoint) VALUES ($1,$2,$3,$4,$5,$6,'h:1')",
			id, org, "gw-"+id.String()[:8], "cs-"+id.String()[:8], site, key); e != nil {
			t.Fatalf("seed gw: %v", e)
		}
		return id
	}

	svc := NewService(pool, nil, nil)

	// No gateways yet → empty set, generation starts at 1 on the first reconcile.
	hs, err := svc.ReconcileHubSet(ctx, org)
	if err != nil {
		t.Fatalf("reconcile 0: %v", err)
	}
	gen0 := hs.Generation

	// Add a gateway (MEMBERSHIP change) → the next reconcile bumps.
	gA := mkGw("K2")
	hs, _ = svc.ReconcileHubSet(ctx, org)
	if hs.Generation <= gen0 {
		t.Fatalf("a membership change must BUMP the generation: %d -> %d", gen0, hs.Generation)
	}
	if len(hs.Members) != 1 || hs.Members[0] != gA {
		t.Fatalf("members must be [gA], got %v", hs.Members)
	}
	genAfterAdd := hs.Generation

	// IDEMPOTENT: N reconciles with the SAME set → the SAME generation (no idle bump — the fence holds).
	for i := 0; i < 3; i++ {
		hs, _ = svc.ReconcileHubSet(ctx, org)
	}
	if hs.Generation != genAfterAdd {
		t.Fatalf("a stable set must NOT bump the generation across reconciles: %d -> %d", genAfterAdd, hs.Generation)
	}

	// "RESTART": a fresh Service over the same DB reads the SAME generation — CP-persisted, never reset.
	svc2 := NewService(pool, nil, nil)
	got, err := svc2.GetHubSet(ctx, org)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.Generation != genAfterAdd {
		t.Fatalf("the generation must SURVIVE a restart (D5 fencing), got %d want %d", got.Generation, genAfterAdd)
	}

	// A second membership change bumps AGAIN (monotonic). gB is a second capable gateway.
	gB := mkGw("K5")
	hs, _ = svc.ReconcileHubSet(ctx, org)
	if hs.Generation <= genAfterAdd {
		t.Fatalf("a second membership change must bump again: %d -> %d", genAfterAdd, hs.Generation)
	}
	// The primary is the lowest-id of {gA, gB} (no pins, equal health → id order).
	lowest := gA
	if gB.String() < gA.String() {
		lowest = gB
	}
	if hs.Members[0] != lowest {
		t.Fatalf("primary must be the lowest-id gateway, got %v want %v", hs.Members[0], lowest)
	}

	// The ADMIN PIN takes effect end-to-end: pin the NON-primary gateway → it becomes members[0] in the
	// PERSISTED set (operator outranks the id/health election), and the reorder bumps the generation.
	other := gA
	if lowest == gA {
		other = gB
	}
	beforePin := hs.Generation
	if err := svc.SetHubPriority(ctx, actor, org, other, pri(1)); err != nil {
		t.Fatalf("set pin: %v", err)
	}
	pinned, _ := svc.GetHubSet(ctx, org)
	if len(pinned.Members) == 0 || pinned.Members[0] != other {
		t.Fatalf("the PINNED gateway must be primary in the persisted set, got %v", pinned.Members)
	}
	if pinned.Generation <= beforePin {
		t.Fatalf("a pin that reorders the set must bump the generation: %d -> %d", beforePin, pinned.Generation)
	}
}
