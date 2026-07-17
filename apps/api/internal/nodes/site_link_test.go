package nodes

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

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

	// Hub: peers with the spoke (AllowedIPs = the spoke's subnet); routes to the spoke subnet.
	hp, hr, err := svc.siteLinkGraph(ctx, hubNode)
	if err != nil {
		t.Fatalf("hub graph: %v", err)
	}
	if len(hp) != 1 || hp[0].PublicKey != "KSPOKE" || !sliceHas(hp[0].AllowedIPs, "10.2.0.0/24") {
		t.Fatalf("hub must peer with the spoke (AllowedIPs = its subnet), got %+v", hp)
	}
	if len(hr) != 1 || hr[0].DstCIDR != "10.2.0.0/24" {
		t.Fatalf("hub routes must reach the spoke subnet, got %+v", hr)
	}

	// Spoke: peers ONLY with the hub (endpoint set, AllowedIPs = all remote); routes to the hub subnet.
	sp, sr, err := svc.siteLinkGraph(ctx, spokeNode)
	if err != nil {
		t.Fatalf("spoke graph: %v", err)
	}
	if len(sp) != 1 || sp[0].PublicKey != "KHUB" || sp[0].Endpoint != "hub.example:51820" || !sliceHas(sp[0].AllowedIPs, "10.1.0.0/24") {
		t.Fatalf("spoke must peer with the hub (endpoint + remote AllowedIPs), got %+v", sp)
	}
	if len(sr) != 1 || sr[0].DstCIDR != "10.1.0.0/24" {
		t.Fatalf("spoke routes must reach the hub subnet, got %+v", sr)
	}

	// FULL-SWEEP: unbind the spoke's node from its site → the hub sees no site peer + no route.
	ex(`UPDATE nodes SET site_id=NULL WHERE id=$1`, nodeSpoke)
	hp2, hr2, err := svc.siteLinkGraph(ctx, hubNode)
	if err != nil {
		t.Fatalf("hub graph after unbind: %v", err)
	}
	if len(hp2) != 0 || len(hr2) != 0 {
		t.Fatalf("after unbinding the spoke, the hub must have no site peer/route (full-sweep), got peers=%+v routes=%+v", hp2, hr2)
	}
}
