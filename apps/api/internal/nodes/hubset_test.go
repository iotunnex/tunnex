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

// electSiteHubSet two-tier membership reds (S8.6 (3)) — PURE, no DB.
func TestElectSiteHubSetOrdering(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fresh := now.Add(-10 * time.Second) // within hubStaleWindow
	stale := now.Add(-10 * time.Minute) // well past it

	// CAPABILITY GATE: no endpoint (NAT'd) OR no wg key → not a candidate.
	t.Run("capability gate excludes NAT'd and keyless", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(1, "", "K1", nil, &fresh),              // NAT'd → out
			gw(2, "1.2.3.4:51820", "", nil, &fresh),   // no key → out
			gw(3, "1.2.3.5:51820", "K3", nil, &fresh), // capable → the single hub
		}}
		if got := ids(electSiteHubSet(topo, now)); len(got) != 1 || got[0] != 3 {
			t.Fatalf("only the capable gateway is the hub, got %v", got)
		}
	})

	// NO PINS → a SINGLE auto-elected hub (set of one) — today's zero-config behavior, no standbys.
	t.Run("no pins → single-hub set of one (lowest id)", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(5, "h:1", "K5", nil, &fresh), gw(2, "h:1", "K2", nil, &fresh), gw(9, "h:1", "K9", nil, &fresh),
		}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{2}) {
			t.Fatalf("no pins → set of ONE (lowest-id hub), got %v", got)
		}
	})

	// PINS present → the set is the PINNED gateways ONLY (HA opt-in); unpinned leaves EXCLUDED, ordered.
	t.Run("pins → pinned set only, unpinned leaf excluded", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(7, "h:1", "K7", nil, &fresh),    // unpinned leaf (fresh) — EXCLUDED (the walk's azure-gw)
			gw(3, "h:1", "K3", pri(2), &stale), // pinned #2
			gw(5, "h:1", "K5", pri(1), &fresh), // pinned #1 → primary
		}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{5, 3}) {
			t.Fatalf("pinned set ordered by priority (5=#1, 3=#2), unpinned 7 excluded, got %v", got)
		}
	})

	// A PIN priority outranks health among the pinned (operator outranks magic).
	t.Run("pin priority outranks health", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(3, "h:1", "K3", pri(1), &stale), // pinned #1 but STALE
			gw(7, "h:1", "K7", pri(2), &fresh), // pinned #2 but FRESH
		}}
		if got := ids(electSiteHubSet(topo, now)); got[0] != 3 {
			t.Fatalf("the lower-priority pin is primary regardless of health, got %v", got)
		}
	})

	// PINNED-BUT-INCAPABLE → excluded; the set falls back to the capable pin (capability still gates).
	t.Run("pinned but incapable is excluded", func(t *testing.T) {
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
			gw(2, "", "K2", pri(1), &fresh),    // pinned #1 but NAT'd → INELIGIBLE
			gw(4, "h:1", "K4", pri(2), &fresh), // pinned #2, capable → the actual primary
		}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{4}) {
			t.Fatalf("a pinned-but-NAT'd gateway is excluded; set falls back to the capable pin, got %v", got)
		}
	})

	// A PINNED cross-site gateway enters — membership = intent + capability, NOT geography (Slice 2 red
	// reinterpreted).
	t.Run("a pinned cross-site gateway enters", func(t *testing.T) {
		siteA, siteB := pgtype.UUID{Bytes: idAt(0xA), Valid: true}, pgtype.UUID{Bytes: idAt(0xB), Valid: true}
		g1 := gw(4, "h:1", "K4", pri(1), &fresh)
		g1.SiteID = siteA
		g2 := gw(6, "h:1", "K6", pri(2), &fresh)
		g2.SiteID = siteB
		topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{g1, g2}}
		if got := ids(electSiteHubSet(topo, now)); string(got) != string([]byte{4, 6}) {
			t.Fatalf("both pinned gateways (any site) enter in priority order, got %v", got)
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

	// Two capable gateways, gA < gB by id (no pins yet → single-hub set = [gA]).
	gA, gB := mkGw("K2"), mkGw("K5")
	if gB.String() < gA.String() {
		gA, gB = gB, gA
	}

	// The single hub (gA) → members [gA], bump from empty.
	hs, _ = svc.ReconcileHubSet(ctx, org)
	if hs.Generation <= gen0 {
		t.Fatalf("electing the single hub must BUMP: %d -> %d", gen0, hs.Generation)
	}
	if len(hs.Configured) != 1 || hs.Configured[0] != gA {
		t.Fatalf("no pins → members = [lowest-id hub gA], got %v", hs.Configured)
	}
	genAfterAdd := hs.Generation

	// IDEMPOTENT: N reconciles with the SAME set → the SAME generation (no idle bump — the fence holds).
	for i := 0; i < 3; i++ {
		hs, _ = svc.ReconcileHubSet(ctx, org)
	}
	if hs.Generation != genAfterAdd {
		t.Fatalf("a stable set must NOT bump across reconciles: %d -> %d", genAfterAdd, hs.Generation)
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

	// gB is already present (higher id, UNPINNED) — the single-hub set is still [gA], so a reconcile does NOT
	// bump: an endpoint-bearing LEAF joining does not change the hub set (two-tier: no intent, no membership).
	hs, _ = svc.ReconcileHubSet(ctx, org)
	if hs.Generation != genAfterAdd || len(hs.Configured) != 1 || hs.Configured[0] != gA {
		t.Fatalf("an unpinned leaf must NOT change the single-hub set (no bump), got members=%v gen %d->%d", hs.Configured, genAfterAdd, hs.Generation)
	}

	// PIN gB → the set becomes the PINNED set [gB] (opt-in HA) → membership changes [gA]->[gB] → BUMP, and
	// the pin takes effect end-to-end (members[0] = gB).
	beforePin := hs.Generation
	if err := svc.SetHubPriority(ctx, actor, org, gB, pri(1)); err != nil {
		t.Fatalf("set pin: %v", err)
	}
	pinned, _ := svc.GetHubSet(ctx, org)
	if len(pinned.Configured) != 1 || pinned.Configured[0] != gB {
		t.Fatalf("pinning gB → the set is the pinned [gB], got %v", pinned.Configured)
	}
	if pinned.Generation <= beforePin {
		t.Fatalf("a pin that changes membership must bump: %d -> %d", beforePin, pinned.Generation)
	}
}

// peerByKey finds a compiled peer by its wg pubkey (nil if absent).
func peerByKey(peers []Peer, key string) *Peer {
	for i := range peers {
		if peers[i].PublicKey == key {
			return &peers[i]
		}
	}
	return nil
}

// TestSiteLinkGraphHA (S8.6 Slice 3) — the corrected (3) topology on the three-gateway WALK shape: two
// PINNED AWS hubs (primary + standby, same site) + an UNPINNED endpoint-bearing azure LEAF. Immortalizes
// the duplicate-subnet trace that caught the membership bug, + the single-AllowedIPs invariant, + hub
// symmetry, + same-site exclusion.
func TestSiteLinkGraphHA(t *testing.T) {
	fresh := time.Now()
	awsSite := idAt(0xA)
	azureSite := idAt(0xB)
	awsGw := gw(1, "aws:51820", "KAWS", pri(1), &fresh) // primary (pin 1)
	awsGw.SiteID = pgtype.UUID{Bytes: awsSite, Valid: true}
	awsStandby := gw(2, "aws2:51820", "KAWS2", pri(2), &fresh) // standby (pin 2, SAME AWS site)
	awsStandby.SiteID = pgtype.UUID{Bytes: awsSite, Valid: true}
	azureGw := gw(3, "azure:51820", "KAZ", nil, &fresh) // UNPINNED leaf (endpoint-bearing — the trap)
	azureGw.SiteID = pgtype.UUID{Bytes: azureSite, Valid: true}
	topo := siteTopology{
		gws:     []sqlc.ListSiteGatewaysForOrgRow{awsGw, awsStandby, azureGw},
		subnets: map[uuid.UUID][]string{awsSite: {"172.31.0.0/16"}, azureSite: {"10.0.0.0/16"}},
	}
	nodeOf := func(g sqlc.ListSiteGatewaysForOrgRow) sqlc.Node { return sqlc.Node{ID: g.ID, SiteID: g.SiteID} }
	countWith := func(peers []Peer, cidr string) int {
		n := 0
		for i := range peers {
			for _, a := range peers[i].AllowedIPs {
				if a == cidr {
					n++
				}
			}
		}
		return n
	}

	// (1) The LEAF (azure, unpinned) compiles as a SPOKE — the primary carries the AWS subnets, the standby
	// is keepalive-only (empty), and 172.31.0.0/16 appears in EXACTLY ONE peer's AllowedIPs (the bug's death).
	azurePeers, _ := siteLinkGraphFrom(topo, nodeOf(azureGw))
	if p := peerByKey(azurePeers, "KAWS"); p == nil || len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != "172.31.0.0/16" {
		t.Fatalf("azure's PRIMARY peer must carry the AWS subnets, got %+v", p)
	}
	if p := peerByKey(azurePeers, "KAWS2"); p == nil || len(p.AllowedIPs) != 0 {
		t.Fatalf("azure's STANDBY peer must be keepalive-only (empty AllowedIPs), got %+v", p)
	}
	if n := countWith(azurePeers, "172.31.0.0/16"); n != 1 {
		t.Fatalf("the single-AllowedIPs invariant: 172.31.0.0/16 must be in EXACTLY ONE peer, got %d (the duplicate bug)", n)
	}

	// (2) The PRIMARY hub (aws-gw) peers with the azure leaf (azure subnets); NOT with the standby (same
	// AWS site — same-site exclusion kills the spurious same-L2 link).
	primaryPeers, primaryRoutes := siteLinkGraphFrom(topo, nodeOf(awsGw))
	if p := peerByKey(primaryPeers, "KAZ"); p == nil || len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != "10.0.0.0/16" {
		t.Fatalf("primary must peer with azure carrying azure subnets, got %+v", p)
	}
	if peerByKey(primaryPeers, "KAWS2") != nil {
		t.Fatal("primary must NOT peer with its same-site standby (same-site exclusion)")
	}

	// (3) The STANDBY hub (aws-instance-2) carries the SAME transit posture — peers with azure (azure
	// subnets, ready to forward), NOT with the same-site primary. Hub-symmetry: identical routes to the
	// primary → promotion changes nothing hub-side.
	standbyPeers, standbyRoutes := siteLinkGraphFrom(topo, nodeOf(awsStandby))
	if p := peerByKey(standbyPeers, "KAZ"); p == nil || len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != "10.0.0.0/16" {
		t.Fatalf("standby must carry the full transit posture (peer azure w/ subnets), got %+v", p)
	}
	if peerByKey(standbyPeers, "KAWS") != nil {
		t.Fatal("standby must NOT peer with its same-site primary (same-site exclusion)")
	}
	if len(primaryRoutes) != len(standbyRoutes) || (len(primaryRoutes) > 0 && primaryRoutes[0].DstCIDR != standbyRoutes[0].DstCIDR) {
		t.Fatalf("hub-symmetry: primary + standby must carry identical routes, got %v vs %v", primaryRoutes, standbyRoutes)
	}

	// (4) ZERO-CONFIG GOLDEN: with NO pins, the same topology compiles single-hub (byte-identical to pre-HA)
	// — the leaf peers with ONLY the single elected hub (lowest id = aws-gw), NO standby peer at all.
	noPins := topo
	noPins.gws = []sqlc.ListSiteGatewaysForOrgRow{
		{ID: awsGw.ID, SiteID: awsGw.SiteID, WgPublicKey: "KAWS", Endpoint: "aws:51820"},
		{ID: awsStandby.ID, SiteID: awsStandby.SiteID, WgPublicKey: "KAWS2", Endpoint: "aws2:51820"},
		{ID: azureGw.ID, SiteID: azureGw.SiteID, WgPublicKey: "KAZ", Endpoint: "azure:51820"},
	}
	azureNoPin, azureNoPinRoutes := siteLinkGraphFrom(noPins, nodeOf(azureGw))
	if len(azureNoPin) != 1 || azureNoPin[0].PublicKey != "KAWS" {
		t.Fatalf("no-pins zero-config: the leaf peers with ONLY the single hub (aws-gw), got %d peers %+v", len(azureNoPin), azureNoPin)
	}

	// (5) VERSION/HASH — no bump: the standby peers add NO routes (the OS routes are the remote subnets,
	// peer-count-independent), so the leaf's ROUTES are IDENTICAL with pins (warm standby) and without. The
	// CanonicalHash is over routes+policy, so a standby peer never changes it → no RequiredVersion bump.
	_, azurePinnedRoutes := siteLinkGraphFrom(topo, nodeOf(azureGw))
	if len(azurePinnedRoutes) != len(azureNoPinRoutes) ||
		(len(azurePinnedRoutes) > 0 && azurePinnedRoutes[0].DstCIDR != azureNoPinRoutes[0].DstCIDR) {
		t.Fatalf("standby peers must add NO routes (hash-invariant): pinned %v vs no-pins %v", azurePinnedRoutes, azureNoPinRoutes)
	}
}

// TestGetHubSetView (S8.6 Slice 6) — the served hub set: ordered members with role (primary=members[0]),
// generation, and per-member L1 metrics from node_peer_status. The render-floor distinction: a member
// with a node_peer_status row has METRICS (even idle rx/tx=0); a NOT-reporting member has NIL metrics
// (absent ≠ zeroes-as-data).
func TestGetHubSetView(t *testing.T) {
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
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'O',$2,'10.99.0.0/24')", org, "hv-"+org.String()[:8]); e != nil {
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
	pr, sb := uuid.New(), uuid.New()
	mk := func(id uuid.UUID, name, key string, prio int) {
		if _, e := pool.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,site_id,wg_public_key,endpoint,hub_priority) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
			id, org, name, "cs-"+id.String()[:8], site, key, "e:51820", prio); e != nil {
			t.Fatalf("seed %s: %v", name, e)
		}
	}
	mk(pr, "primary", "KPR", 1)
	mk(sb, "standby", "KSB", 2)

	svc := NewService(pool, nil, nil)
	if _, e := svc.ReconcileHubSet(ctx, org); e != nil {
		t.Fatalf("reconcile: %v", e)
	}
	// The PRIMARY is IDLE-but-reporting: a node_peer_status row with rx/tx = 0 (a real link, no traffic yet).
	// The STANDBY is NOT reporting: NO row.
	now := time.Now()
	if _, e := pool.Exec(ctx, "INSERT INTO node_peer_status (node_id,public_key,last_handshake_at,rx_bytes,tx_bytes) VALUES ($1,'KPR',$2,0,0)", sb, now); e != nil {
		t.Fatalf("seed primary metrics: %v", e)
	}

	view, err := svc.GetHubSetView(ctx, org)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Members) != 2 || view.Members[0].NodeID != pr || view.Members[0].Role != "primary" || view.Members[1].Role != "standby" {
		t.Fatalf("ordered members with role (primary=members[0]), got %+v", view.Members)
	}
	// IDLE-but-reporting primary → metrics PRESENT with zeroes (an honest idle link).
	if view.Members[0].Metrics == nil || view.Members[0].Metrics.RxBytes != 0 {
		t.Fatalf("an idle-but-reporting member must have metrics (rx/tx=0 is honest), got %+v", view.Members[0].Metrics)
	}
	// NOT-reporting standby → metrics NIL (absent ≠ zeroes — a not-reporting link is a different truth).
	if view.Members[1].Metrics != nil {
		t.Fatalf("a NOT-reporting member must have ABSENT metrics (nil), never zeroes-as-data, got %+v", view.Members[1].Metrics)
	}
	if view.Generation <= 0 {
		t.Fatalf("the generation (version tag) must be served, got %d", view.Generation)
	}
}

// TestReconcileHubSetMembershipAudit — S8.6 REDUCE #4 (membership as its own event, condition 1b): a
// CONFIGURED change (a gateway leaving the pinned set — the unbind/delete membership event) bumps the
// generation AND audits hub_set.membership, DISTINCT from a promotion/failback. An unchanged reconcile
// neither bumps nor re-audits (no idle tick eroding the fence).
func TestReconcileHubSetMembershipAudit(t *testing.T) {
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
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)", org, "ma-"+org.String()[:8]); e != nil {
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
	g1, g2 := uuid.New(), uuid.New()
	mk := func(id uuid.UUID, name, key string, prio int) {
		if _, e := pool.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,site_id,wg_public_key,endpoint,hub_priority) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
			id, org, name, "cs-"+id.String()[:8], site, key, "e:51820", prio); e != nil {
			t.Fatalf("seed %s: %v", name, e)
		}
	}
	mk(g1, "g1", "KG1", 1)
	mk(g2, "g2", "KG2", 2)

	svc := NewService(pool, nil, nil)
	hs1, e := svc.ReconcileHubSet(ctx, org) // configured=[g1,g2] — the first membership event
	if e != nil {
		t.Fatalf("reconcile 1: %v", e)
	}
	if len(hs1.Configured) != 2 || hs1.Configured[0] != g1 {
		t.Fatalf("configured must be [g1,g2], got %v", hs1.Configured)
	}
	membershipAudits := func() int {
		var n int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE org_id=$1 AND action='hub_set.membership'", org).Scan(&n)
		return n
	}
	if membershipAudits() != 1 {
		t.Fatalf("the first configured write must audit hub_set.membership once, got %d", membershipAudits())
	}

	// UNCHANGED reconcile → NO bump, NO new audit (the fence + audit both quiet).
	hs1b, _ := svc.ReconcileHubSet(ctx, org)
	if hs1b.Generation != hs1.Generation || membershipAudits() != 1 {
		t.Fatalf("an unchanged reconcile must not bump/re-audit, gen %d->%d audits=%d", hs1.Generation, hs1b.Generation, membershipAudits())
	}

	// MEMBERSHIP EVENT: g1 leaves the site (the unbind/delete effect on configured). Reconcile drops it.
	if _, e := pool.Exec(ctx, "UPDATE nodes SET site_id=NULL WHERE id=$1", g1); e != nil {
		t.Fatalf("unbind g1: %v", e)
	}
	hs2, e := svc.ReconcileHubSet(ctx, org)
	if e != nil {
		t.Fatalf("reconcile 2: %v", e)
	}
	if len(hs2.Configured) != 1 || hs2.Configured[0] != g2 {
		t.Fatalf("after g1 leaves, configured must be [g2], got %v", hs2.Configured)
	}
	if hs2.Generation <= hs1.Generation {
		t.Fatalf("a membership change must bump the generation: %d -> %d", hs1.Generation, hs2.Generation)
	}
	if membershipAudits() != 2 {
		t.Fatalf("the membership change must audit hub_set.membership again (2 total), got %d", membershipAudits())
	}
	// The compiler + view AGREE with the new configured set — the derived active order is [g2].
	view, _ := svc.GetHubSetView(ctx, org)
	if len(view.Members) != 1 || view.Members[0].NodeID != g2 || view.Members[0].Role != "primary" {
		t.Fatalf("view must agree with the reconciled set: [g2 primary], got %+v", view.Members)
	}
}

// TestRevokedGatewayLeavesHubSet — S8.6 #4 (revoke-path): a REVOKED gateway drops from the hub-set candidate
// pool (the status='active' filter on ListSiteGatewaysForOrg) and RevokeNode's ReconcileHubSet trigger makes
// the drop durable. Revoking the PRIMARY (the loudest case — the org's active transit hub itself) removes it
// from configured, promotes the standby to the active head via the ORDINARY derivation (no blackhole), bumps
// the generation, and audits hub_set.membership. "Revoked but still electable as the org's transit hub" —
// the promise-contradiction at the topology tier — is closed.
func TestRevokedGatewayLeavesHubSet(t *testing.T) {
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
	if _, e := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)", org, "rv-"+org.String()[:8]); e != nil {
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
	mk := func(id uuid.UUID, name, key string, prio int) {
		if _, e := pool.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,site_id,wg_public_key,endpoint,hub_priority) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
			id, org, name, "cs-"+id.String()[:8], site, key, "e:51820", prio); e != nil {
			t.Fatalf("seed %s: %v", name, e)
		}
	}
	mk(primary, "primary", "KPRI", 1)
	mk(standby, "standby", "KSTB", 2)

	svc := NewService(pool, nil, nil)
	hs1, e := svc.ReconcileHubSet(ctx, org) // configured=[primary, standby]
	if e != nil {
		t.Fatalf("reconcile 1: %v", e)
	}
	if len(hs1.Configured) != 2 || hs1.Active()[0] != primary {
		t.Fatalf("configured must be [primary, standby], got %v", hs1.Configured)
	}
	membershipAudits := func() int {
		var n int
		_ = pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs WHERE org_id=$1 AND action='hub_set.membership'", org).Scan(&n)
		return n
	}

	// REVOKE THE PRIMARY (the active transit hub). It drops from ListSiteGatewaysForOrg (status='active') —
	// this is what RevokeNode's trigger reconciles against.
	if _, e := pool.Exec(ctx, "UPDATE nodes SET status='revoked' WHERE id=$1", primary); e != nil {
		t.Fatalf("revoke primary: %v", e)
	}
	hs2, e := svc.ReconcileHubSet(ctx, org) // the RevokeNode trigger's effect
	if e != nil {
		t.Fatalf("reconcile 2: %v", e)
	}
	if len(hs2.Configured) != 1 || hs2.Configured[0] != standby {
		t.Fatalf("a revoked primary must leave configured; the standby becomes the set, got %v", hs2.Configured)
	}
	if hs2.Active()[0] != standby {
		t.Fatalf("the standby must be the NEW active primary (promotion-shaped, no blackhole), got %v", hs2.Active())
	}
	if hs2.Generation <= hs1.Generation {
		t.Fatalf("a revoked-gateway membership change must bump the generation: %d -> %d", hs1.Generation, hs2.Generation)
	}
	if membershipAudits() != 2 {
		t.Fatalf("the revoke membership change must audit hub_set.membership (2 total), got %d", membershipAudits())
	}
	// The view agrees — the revoked primary is GONE, the standby is primary (no ghost hub candidate).
	view, _ := svc.GetHubSetView(ctx, org)
	if len(view.Members) != 1 || view.Members[0].NodeID != standby {
		t.Fatalf("the view must show only the live standby as primary, got %+v", view.Members)
	}
}
