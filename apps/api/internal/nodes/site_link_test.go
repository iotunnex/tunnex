package nodes

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// TestPushedHashMatchesServedForRoutedGateway — S8.2 review #1 fix (was a MERGE-BLOCKER): the pushed-hash
// baseline and the served artifact are finalized the SAME way (single source), so a route-carrying
// ENFORCING site gateway compares CLEAN — no permanent false silent_desync. Before the fix the served
// artifact bumped to v5 (routes present) while the pushed hash stayed v4 (route-less); Version is
// in-hash → pushed != applied forever. This is the routed-but-dropped scenario (routes, no grant).
func TestPushedHashMatchesServedForRoutedGateway(t *testing.T) {
	svc := &Service{policy: fakeProvider{}} // enterprise path (policy != nil)
	siteA, siteB := uuid.New(), uuid.New()
	nodeA := uuid.New()
	topo := siteTopology{
		gws: []sqlc.ListSiteGatewaysForOrgRow{
			{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KA", Endpoint: "a:51820"},
			{ID: uuid.New(), SiteID: pgtype.UUID{Bytes: siteB, Valid: true}, WgPublicKey: "KB"},
		},
		subnets: map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}, siteB: {"10.2.0.0/24"}},
	}
	node := sqlc.Node{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}}
	mkRouteless := func() *policyspec.Compiled { // route-less enforcing artifact (routed-but-dropped: no grant)
		return &policyspec.Compiled{Version: policyspec.RequiredVersion(policyspec.Compiled{Mode: "enforcing"}), NodeID: nodeA.String(), Mode: "enforcing"}
	}
	if mkRouteless().Version != 4 {
		t.Fatalf("precondition: a route-less enforcing artifact is v4, got %d", mkRouteless().Version)
	}
	served := svc.finalizeArtifact(topo, node, mkRouteless()) // what the agent applies
	if served.Version != 5 || len(served.Routes) == 0 {
		t.Fatalf("finalize must attach routes + bump to v5, got v%d routes=%d", served.Version, len(served.Routes))
	}
	applied := policyspec.CanonicalHash(*served)
	pushed := svc.pushedHash(topo, node, mkRouteless()) // finalized the SAME way
	if pushed != applied {
		t.Fatalf("#1: pushed(%s) must equal applied(%s) for a routed enforcing gateway — false desync", pushed, applied)
	}
}

// TestFinalizeAttachesDNSForwardsOutOfHash — S8.4 D1/D5: finalizeArtifact attaches the org DNS forwarding
// table onto a routed gateway, and it is OUT-of-hash — the pushed/applied hash is byte-identical with or
// without DNS (a DNS-only change never false-alarms silent_desync; every gateway carries the whole table).
func TestFinalizeAttachesDNSForwardsOutOfHash(t *testing.T) {
	svc := &Service{policy: fakeProvider{}}
	siteA, siteB, nodeA := uuid.New(), uuid.New(), uuid.New()
	base := siteTopology{
		gws: []sqlc.ListSiteGatewaysForOrgRow{
			{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KA", Endpoint: "a:51820"},
			{ID: uuid.New(), SiteID: pgtype.UUID{Bytes: siteB, Valid: true}, WgPublicKey: "KB"},
		},
		subnets: map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}, siteB: {"10.2.0.0/24"}},
	}
	node := sqlc.Node{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}}
	mkRouteless := func() *policyspec.Compiled {
		return &policyspec.Compiled{Version: policyspec.RequiredVersion(policyspec.Compiled{Mode: "enforcing"}), NodeID: nodeA.String(), Mode: "enforcing"}
	}
	withDNS := base
	withDNS.dnsForwards = []policyspec.DNSForward{{Domain: "corp.local", ResolverIP: "10.2.0.53"}}

	got := svc.finalizeArtifact(withDNS, node, mkRouteless())
	if len(got.DNSForwards) != 1 || got.DNSForwards[0].Domain != "corp.local" {
		t.Fatalf("finalize must attach the org DNS table onto the gateway, got %+v", got.DNSForwards)
	}
	// Out-of-hash: the finalized hash is identical with vs without DNS.
	noDNS := svc.finalizeArtifact(base, node, mkRouteless())
	if policyspec.CanonicalHash(*got) != policyspec.CanonicalHash(*noDNS) {
		t.Fatal("DNSForwards must be out-of-hash — attaching DNS changed the artifact hash (false-desync risk)")
	}
	if got.Version != noDNS.Version {
		t.Fatalf("DNSForwards must not bump the version; got v%d vs v%d", got.Version, noDNS.Version)
	}
}

// TestElectSiteHubIsTheOneElection — S8.3 D2: the hub designation the Node API projects (is_site_hub) reads
// electSiteHub, the SAME picker the site-link graph + health use — endpoint-bearing, lowest id, ties by id,
// nil when all NAT'd. siteTopoHasHub is exactly (electSiteHub != nil), so existence and designation never
// disagree (no second election in the UI, the overrule's point).
func TestElectSiteHubIsTheOneElection(t *testing.T) {
	lo, hi := uuid.New(), uuid.New()
	if lo.String() > hi.String() {
		lo, hi = hi, lo // ensure lo has the lower id
	}
	now := time.Now()
	// Two endpoint-bearing gateways (with keys) → the lower id wins (no pins, equal health → id tie-break).
	topo := siteTopology{gws: []sqlc.ListSiteGatewaysForOrgRow{
		{ID: hi, Endpoint: "b:51820", WgPublicKey: "Khi"}, {ID: lo, Endpoint: "a:51820", WgPublicKey: "Klo"},
	}}
	hub := electSiteHub(topo, now)
	if hub == nil || hub.ID != lo {
		t.Fatalf("the endpoint-bearing lowest-id gateway must be the hub, got %+v", hub)
	}
	if !siteTopoHasHub(topo) {
		t.Fatal("siteTopoHasHub must agree with electSiteHub (one election)")
	}
	// A NAT'd gateway (no endpoint) is never the hub even with a lower id.
	topo.gws = []sqlc.ListSiteGatewaysForOrgRow{{ID: lo, WgPublicKey: "Klo"}, {ID: hi, Endpoint: "b:51820", WgPublicKey: "Khi"}}
	if h := electSiteHub(topo, now); h == nil || h.ID != hi {
		t.Fatalf("a NAT'd gateway cannot be the hub; the endpoint-bearing one wins, got %+v", h)
	}
	// All NAT'd → no hub (B2 no-carrier), and siteTopoHasHub agrees.
	topo.gws = []sqlc.ListSiteGatewaysForOrgRow{{ID: lo, WgPublicKey: "Klo"}, {ID: hi, WgPublicKey: "Khi"}}
	if electSiteHub(topo, now) != nil || siteTopoHasHub(topo) {
		t.Fatal("all-NAT'd → no hub (both electSiteHub and siteTopoHasHub must say so)")
	}
}

// TestSiteLinkNoHubNoRoutes — S8.2 B2: no gateway has a public endpoint (all NAT'd) → no carrier, so
// siteLinkGraphFrom emits ZERO routes + ZERO peers (routes with no peer to carry them are the silent
// blackhole), and siteHubMissing flags the condition so it surfaces as site_hub_down.
func TestSiteLinkNoHubNoRoutes(t *testing.T) {
	siteA, siteB := uuid.New(), uuid.New()
	nodeA := uuid.New()
	topo := siteTopology{
		gws: []sqlc.ListSiteGatewaysForOrgRow{
			{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KA"},      // no endpoint
			{ID: uuid.New(), SiteID: pgtype.UUID{Bytes: siteB, Valid: true}, WgPublicKey: "KB"}, // no endpoint
		},
		subnets: map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}, siteB: {"10.2.0.0/24"}},
	}
	node := sqlc.Node{ID: nodeA, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}}
	if peers, routes := siteLinkGraphFrom(topo, node); len(peers) != 0 || len(routes) != 0 {
		t.Fatalf("no hub → no peers + no routes (no silent blackhole), got peers=%d routes=%d", len(peers), len(routes))
	}
	if !siteHubMissing(siteTopoHasHub(topo), topo, node) {
		t.Fatal("no hub + remote subnets → siteHubMissing must be true (surfaces site_hub_down)")
	}
	topo.gws[1].Endpoint = "b.example:51820" // give one gateway an endpoint → a hub now exists
	if siteHubMissing(siteTopoHasHub(topo), topo, node) {
		t.Fatal("a hub exists → siteHubMissing must be false")
	}
}

// TestDesiredStateFailsWholeFetchOnTopologyError — S8.2 F1/R1/B3 (terminal): a site-topology load error
// FAILS the whole DesiredState fetch (atomic + fail-static), so the agent holds last-good everything and
// tears nothing down. NOT the omit-and-teardown attempt (full-sweep reconcile deletes an omitted section).
func TestDesiredStateFailsWholeFetchOnTopologyError(t *testing.T) {
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
	org, node, site := uuid.New(), uuid.New(), uuid.New()
	ex := func(q string, a ...any) {
		if _, e := pool.Exec(ctx, q, a...); e != nil {
			t.Fatalf("seed %q: %v", q, e)
		}
	}
	ex(`INSERT INTO organizations (id,name,slug) VALUES ($1,'F1',$2)`, org, "f1-"+org.String()[:8])
	ex(`INSERT INTO sites (id,org_id,name) VALUES ($1,$2,'A')`, site, org)
	ex(`INSERT INTO nodes (id,org_id,name,cert_serial,agent_version,site_id) VALUES ($1,$2,'gw',$4,'0.1.0',$3)`, node, org, site, "f1s-"+node.String())
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })

	svc := &Service{pool: pool, q: sqlc.New(pool)}
	siteNode := sqlc.Node{ID: node, OrgID: org, SiteID: pgtype.UUID{Bytes: site, Valid: true}}

	svc.siteTopoLoad = func(context.Context, uuid.UUID) (siteTopology, error) {
		return siteTopology{}, errors.New("topology DB blip")
	}
	if _, err := svc.DesiredState(ctx, siteNode); err == nil {
		t.Fatal("F1: a topology-load error must FAIL the whole fetch (fail-static), not serve a partial artifact")
	}
	// With the real loader the fetch succeeds (site section present, no fault).
	svc.siteTopoLoad = svc.loadSiteTopology
	if _, err := svc.DesiredState(ctx, siteNode); err != nil {
		t.Fatalf("with topology loading OK, the fetch must succeed: %v", err)
	}
}

func sliceHas(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TestSiteLinkGraphHubSpokeAndFullSweep — S8.2 Slice-2 CP red: siteLinkGraph builds the hub-and-spoke
// site-link peers + per-node routes, and a site unbind sweeps them (full-sweep). The hub (a gateway
// with a public endpoint) peers with each spoke (AllowedIPs = that spoke's subnet); a spoke peers ONLY
// with the hub (AllowedIPs = all remote subnets, hub endpoint). Routes = every remote site subnet.
func TestSiteLinkGraphHubSpokeAndFullSweep(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	svc := &Service{q: sqlc.New(tx)}

	org := uuid.New()
	siteA, siteB := uuid.New(), uuid.New()
	nodeHub, nodeSpoke := uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := tx.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)`, org, "sl-"+org.String()[:8])
	ex(`INSERT INTO sites (id,org_id,name) VALUES ($1,$2,'A'),($3,$2,'B')`, siteA, org, siteB)
	// Hub = has a public endpoint; spoke = none. Both have WG keys + site bindings.
	ex(`INSERT INTO nodes (id,org_id,name,cert_serial,agent_version,wg_public_key,endpoint,site_id)
	    VALUES ($1,$2,'hub','s1','0.1.0','KHUB','hub.example:51820',$3)`, nodeHub, org, siteA)
	ex(`INSERT INTO nodes (id,org_id,name,cert_serial,agent_version,wg_public_key,endpoint,site_id)
	    VALUES ($1,$2,'spoke','s2','0.1.0','KSPOKE','',$3)`, nodeSpoke, org, siteB)
	ex(`INSERT INTO site_subnets (site_id,cidr,status) VALUES ($1,'10.1.0.0/24','approved'),($2,'10.2.0.0/24','approved')`, siteA, siteB)

	hubNode := sqlc.Node{ID: nodeHub, OrgID: org, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}}
	spokeNode := sqlc.Node{ID: nodeSpoke, OrgID: org, SiteID: pgtype.UUID{Bytes: siteB, Valid: true}}

	graph := func(node sqlc.Node) ([]Peer, []policyspec.Route) {
		topo, e := svc.loadSiteTopology(ctx, org)
		if e != nil {
			t.Fatalf("load topology: %v", e)
		}
		return siteLinkGraphFrom(topo, node)
	}

	// Hub: peers with the spoke (AllowedIPs = the spoke's subnet); routes to the spoke subnet.
	hp, hr := graph(hubNode)
	if len(hp) != 1 || hp[0].PublicKey != "KSPOKE" || !sliceHas(hp[0].AllowedIPs, "10.2.0.0/24") {
		t.Fatalf("hub must peer with the spoke (AllowedIPs = its subnet), got %+v", hp)
	}
	if hp[0].PersistentKeepalive != siteLinkKeepaliveSecs { // S8.3 CK: every site-link peer carries the keepalive
		t.Fatalf("site-link peer must carry keepalive=%d, got %d", siteLinkKeepaliveSecs, hp[0].PersistentKeepalive)
	}
	if len(hr) != 1 || hr[0].DstCIDR != "10.2.0.0/24" {
		t.Fatalf("hub routes must reach the spoke subnet, got %+v", hr)
	}

	// Spoke: peers ONLY with the hub (endpoint set, AllowedIPs = all remote); routes to the hub subnet.
	sp, sr := graph(spokeNode)
	if len(sp) != 1 || sp[0].PublicKey != "KHUB" || sp[0].Endpoint != "hub.example:51820" || !sliceHas(sp[0].AllowedIPs, "10.1.0.0/24") {
		t.Fatalf("spoke must peer with the hub (endpoint + remote AllowedIPs), got %+v", sp)
	}
	if len(sr) != 1 || sr[0].DstCIDR != "10.1.0.0/24" {
		t.Fatalf("spoke routes must reach the hub subnet, got %+v", sr)
	}

	// FULL-SWEEP: unbind the spoke's node from its site → the hub sees no site peer + no route.
	ex(`UPDATE nodes SET site_id=NULL WHERE id=$1`, nodeSpoke)
	hp2, hr2 := graph(hubNode)
	if len(hp2) != 0 || len(hr2) != 0 {
		t.Fatalf("after unbinding the spoke, the hub must have no site peer/route (full-sweep), got peers=%+v routes=%+v", hp2, hr2)
	}
}

// TestSpokePrimaryCarriesPoolStandbyEmpty — A3b D-A3b-1/4 + the failover-symmetry red. The device POOL
// rides the spoke's hub-PRIMARY peer AllowedIPs (the far half of device→remote-site transit: inbound wg
// admits device-sourced packets via the hub; outbound, replies to pool addrs crypto-route back). Primary
// ONLY — the standby stays AllowedIPs-EMPTY (the S8.6 single-valued invariant; pool on two peers would be
// the overlapping-AllowedIPs nondeterminism). Promotion (hubMembers reorder) moves the pool WITH the
// routes onto the new primary — no pool-special failover path.
func TestSpokePrimaryCarriesPoolStandbyEmpty(t *testing.T) {
	siteA, siteB := uuid.New(), uuid.New()
	hub1, hub2, spoke := uuid.New(), uuid.New(), uuid.New()
	g1 := sqlc.ListSiteGatewaysForOrgRow{ID: hub1, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KH1", Endpoint: "h1.example:51820"}
	g2 := sqlc.ListSiteGatewaysForOrgRow{ID: hub2, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KH2", Endpoint: "h2.example:51820"}
	gs := sqlc.ListSiteGatewaysForOrgRow{ID: spoke, SiteID: pgtype.UUID{Bytes: siteB, Valid: true}, WgPublicKey: "KS"}
	topo := siteTopology{
		gws:        []sqlc.ListSiteGatewaysForOrgRow{g1, g2, gs},
		subnets:    map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}, siteB: {"10.2.0.0/24"}},
		hubMembers: []sqlc.ListSiteGatewaysForOrgRow{g1, g2},
		poolCIDR:   "10.99.0.0/24",
	}
	spokeNode := sqlc.Node{ID: spoke, SiteID: pgtype.UUID{Bytes: siteB, Valid: true}}

	peers, _ := siteLinkGraphFrom(topo, spokeNode)
	var prim, stand *Peer
	for i := range peers {
		switch peers[i].PublicKey {
		case "KH1":
			prim = &peers[i]
		case "KH2":
			stand = &peers[i]
		}
	}
	if prim == nil || stand == nil {
		t.Fatalf("spoke must peer with primary + standby, got %+v", peers)
	}
	if !sliceHas(prim.AllowedIPs, "10.99.0.0/24") || !sliceHas(prim.AllowedIPs, "10.1.0.0/24") {
		t.Fatalf("hub-PRIMARY peer must carry the pool alongside the routes, got %v", prim.AllowedIPs)
	}
	if len(stand.AllowedIPs) != 0 {
		t.Fatalf("standby must stay AllowedIPs-EMPTY (single-valued invariant), got %v", stand.AllowedIPs)
	}

	// FAILOVER SYMMETRY: promotion (order flip) moves the pool onto the NEW primary; the demoted drains.
	topo.hubMembers = []sqlc.ListSiteGatewaysForOrgRow{g2, g1}
	peers2, _ := siteLinkGraphFrom(topo, spokeNode)
	for i := range peers2 {
		switch peers2[i].PublicKey {
		case "KH2":
			if !sliceHas(peers2[i].AllowedIPs, "10.99.0.0/24") {
				t.Fatalf("promotion must move the pool onto the new primary, got %v", peers2[i].AllowedIPs)
			}
		case "KH1":
			if len(peers2[i].AllowedIPs) != 0 {
				t.Fatalf("the demoted member must drain to empty (pool included), got %v", peers2[i].AllowedIPs)
			}
		}
	}

	// An empty pool (soft-deleted org edge) emits routes only — no empty-string AllowedIP artifact.
	topo.poolCIDR = ""
	peers3, _ := siteLinkGraphFrom(topo, spokeNode)
	for i := range peers3 {
		if peers3[i].PublicKey == "KH2" && sliceHas(peers3[i].AllowedIPs, "") {
			t.Fatalf("empty pool must not emit an empty AllowedIP, got %v", peers3[i].AllowedIPs)
		}
	}
}

// TestFinalizeAttachesPoolV6 — A3b: finalizeArtifact attaches PoolCIDR to a route-carrying site-gateway
// artifact and the content-derived version lands at 6; a topo WITHOUT routes (single-site org) returns the
// artifact UNCHANGED (no pool, pre-v6 bytes — the content-derived blast-radius guard).
func TestFinalizeAttachesPoolV6(t *testing.T) {
	siteA, siteB := uuid.New(), uuid.New()
	hub, spoke := uuid.New(), uuid.New()
	topo := siteTopology{
		gws: []sqlc.ListSiteGatewaysForOrgRow{
			{ID: hub, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KH", Endpoint: "h:51820"},
			{ID: spoke, SiteID: pgtype.UUID{Bytes: siteB, Valid: true}, WgPublicKey: "KS"},
		},
		subnets:  map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}, siteB: {"10.2.0.0/24"}},
		poolCIDR: "10.99.0.0/24",
	}
	svc := &Service{}
	spokeNode := sqlc.Node{ID: spoke, SiteID: pgtype.UUID{Bytes: siteB, Valid: true}}
	final := svc.finalizeArtifact(topo, spokeNode, nil)
	if final == nil || final.PoolCIDR != "10.99.0.0/24" {
		t.Fatalf("a route-carrying site artifact must carry the pool, got %+v", final)
	}
	if final.Version != 6 {
		t.Fatalf("pool-carrying artifact must derive v6 (content-derived), got %d", final.Version)
	}

	// single-site (no remote routes): unchanged — no pool, no v6 (the blast-radius guard).
	soloTopo := siteTopology{
		gws:      []sqlc.ListSiteGatewaysForOrgRow{{ID: hub, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}, WgPublicKey: "KH", Endpoint: "h:51820"}},
		subnets:  map[uuid.UUID][]string{siteA: {"10.1.0.0/24"}},
		poolCIDR: "10.99.0.0/24",
	}
	hubNode := sqlc.Node{ID: hub, SiteID: pgtype.UUID{Bytes: siteA, Valid: true}}
	if got := svc.finalizeArtifact(soloTopo, hubNode, nil); got != nil {
		t.Fatalf("a single-site (route-less) gateway must stay unchanged (nil artifact stays nil), got %+v", got)
	}
}
