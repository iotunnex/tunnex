package sites

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// TestSiteTransportCheckRefusesUnknown is the D4 refuse-don't-guess confirmation (Slice-2 ruling 1):
// the link_transport CHECK constraint must REJECT a non-wireguard value with a loud 23514, not
// silently accept it — the schema twin of the version gate's refuse-don't-guess. A future transport
// (ipsec) is unusable until its migration lands.
func TestSiteTransportCheckRefusesUnknown(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	org := uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'S',$2)`, org, "tck-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })

	_, err := pool.Exec(ctx, `INSERT INTO sites (org_id,name,link_transport) VALUES ($1,'ipsec-site','ipsec')`, org)
	if err == nil {
		t.Fatal("link_transport='ipsec' must be REFUSED by the CHECK until its migration lands (refuse-don't-guess)")
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		t.Fatalf("want a CHECK violation (23514), got %v", err)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestReplaceNodePreservesSiteEntity is the S8.1 D6 replace-node red: a SITE is an ENTITY that owns
// its gateway node, NOT an attribute of the node. Replacing a site's gateway (unbind old, bind new)
// must leave the site's identity AND its subnets intact — the exact operational reason the model is
// entity-not-attribute (a failed gateway box is swapped without losing the site). Also pins
// single-node v1: binding a second node to an occupied site is refused (site_has_gateway 409).
func TestReplaceNodePreservesSiteEntity(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	q := sqlc.New(pool)
	ctx := context.Background()

	org, nodeA, nodeB := uuid.New(), uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id, name, slug) VALUES ($1,'S',$2)`, org, "site-"+org.String()[:8])
	ex(`INSERT INTO nodes (id, org_id, name, cert_serial) VALUES ($1,$2,'gw-a',$3)`, nodeA, org, "cs-a-"+nodeA.String()[:8])
	ex(`INSERT INTO nodes (id, org_id, name, cert_serial) VALUES ($1,$2,'gw-b',$3)`, nodeB, org, "cs-b-"+nodeB.String()[:8])
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })

	site, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register site: %v", err)
	}
	if err := svc.BindNode(ctx, org, site.ID, nodeA); err != nil {
		t.Fatalf("bind A: %v", err)
	}
	sub, err := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix("10.20.0.0/24"))
	if err != nil {
		t.Fatalf("add subnet: %v", err)
	}

	// Single-node v1: binding B while A is still bound is REFUSED.
	if err := svc.BindNode(ctx, org, site.ID, nodeB); err == nil {
		t.Fatal("binding a second node to an occupied site must be refused (single-node v1)")
	}

	// REPLACE the node: unbind A, bind B.
	if err := svc.UnbindNode(ctx, org, nodeA); err != nil {
		t.Fatalf("unbind A: %v", err)
	}
	if err := svc.BindNode(ctx, org, site.ID, nodeB); err != nil {
		t.Fatalf("bind B after unbind: %v", err)
	}

	// The SITE entity survives the node swap (identity intact).
	got, err := svc.GetSite(ctx, org, site.ID)
	if err != nil || got.ID != site.ID || got.Name != site.Name {
		t.Fatalf("site identity must survive node replacement: %v (%+v)", err, got)
	}
	// The subnet survives — it is site-scoped, not node-scoped.
	subs, err := svc.ListSubnets(ctx, org, site.ID)
	if err != nil || len(subs) != 1 || subs[0].ID != sub.ID {
		t.Fatalf("site subnet must survive node replacement, got %d subnets (%v)", len(subs), err)
	}
	// B is now the site's gateway; A is unbound.
	n, err := q.GetSiteNode(ctx, pgtype.UUID{Bytes: site.ID, Valid: true})
	if err != nil || n.ID != nodeB {
		t.Fatalf("the replacement node B must be the site's gateway, got %v (%v)", n.ID, err)
	}
}
