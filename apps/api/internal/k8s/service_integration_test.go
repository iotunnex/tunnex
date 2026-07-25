package k8s

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// seedOrgSite makes an org (device pool 10.99.0.0/24) + a site; returns their ids.
func seedOrgSite(t *testing.T, pool *pgxpool.Pool) (org, site uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, site = uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id, name, slug, pool_cidr) VALUES ($1,'K',$2,'10.99.0.0/24')`, org, "k8s-"+org.String()[:8])
	ex(`INSERT INTO sites (id, org_id, name) VALUES ($1,$2,'site')`, site, org)
	return org, site
}

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// TestRegisterClusterRejectsOverlap: a VIP range overlapping the device pool OR an approved site subnet
// is refused with the TYPED class in the teaching message (never silently accepted — ambiguous routing).
func TestRegisterClusterRejectsOverlap(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	// An approved site subnet the VIP range must also avoid.
	if _, e := pool.Exec(ctx, `INSERT INTO site_subnets (id, site_id, cidr, status) VALUES ($1,$2,'10.20.0.0/24','approved')`, uuid.New(), site); e != nil {
		t.Fatal(e)
	}

	// Overlaps the device pool.
	if _, err := svc.RegisterCluster(ctx, org, site, "c-pool", pfx("10.99.0.0/25")); err == nil || !strings.Contains(err.Error(), "pool") {
		t.Fatalf("want a pool-class overlap refusal, got %v", err)
	}
	// Overlaps the approved site subnet.
	if _, err := svc.RegisterCluster(ctx, org, site, "c-site", pfx("10.20.0.128/25")); err == nil || !strings.Contains(err.Error(), "site_subnet") {
		t.Fatalf("want a site_subnet-class overlap refusal, got %v", err)
	}
	// A disjoint range is accepted.
	if _, err := svc.RegisterCluster(ctx, org, site, "c-ok", pfx("100.64.0.0/16")); err != nil {
		t.Fatalf("a disjoint VIP range must be accepted, got %v", err)
	}
}

// TestSecondClusterCannotOverlapFirst proves the collector's 7th-caller path LIVE: a second cluster's VIP
// range is checked against the FIRST cluster's range (fed in by Collect) and refused with the vip_range class.
func TestSecondClusterCannotOverlapFirst(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)

	if _, err := svc.RegisterCluster(ctx, org, site, "a", pfx("100.64.0.0/16")); err != nil {
		t.Fatalf("first cluster: %v", err)
	}
	if _, err := svc.RegisterCluster(ctx, org, site, "b", pfx("100.64.5.0/24")); err == nil || !strings.Contains(err.Error(), "vip_range") {
		t.Fatalf("a second cluster overlapping the first's VIP range must be refused (vip_range class), got %v", err)
	}
}

// TestExposeAllocatesThenExhausts: a /30 range yields exactly one usable VIP; the second expose is refused
// HONESTLY (vip_range_exhausted), never silently reusing an address.
func TestExposeAllocatesThenExhausts(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	c, err := svc.RegisterCluster(ctx, org, site, "small", pfx("100.66.0.0/30")) // 1 usable host (.2)
	if err != nil {
		t.Fatal(err)
	}
	svc1, err := svc.ExposeService(ctx, org, c.ID, "api", "prod", "tcp", nil, nil)
	if err != nil {
		t.Fatalf("first expose: %v", err)
	}
	if svc1.Vip.String() != "100.66.0.2" {
		t.Fatalf("VIP allocated from the range low end, got %s", svc1.Vip)
	}
	if _, err := svc.ExposeService(ctx, org, c.ID, "web", "prod", "tcp", nil, nil); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("exposing past the range must refuse honestly (exhausted), got %v", err)
	}
}

// TestVIPAllocationClusterScoped: two clusters allocate from their OWN ranges (independent used-sets) —
// exposing in cluster A does not consume cluster B's addresses.
func TestVIPAllocationClusterScoped(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	a, err := svc.RegisterCluster(ctx, org, site, "ca", pfx("100.64.0.0/28"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.RegisterCluster(ctx, org, site, "cb", pfx("100.65.0.0/28"))
	if err != nil {
		t.Fatal(err)
	}
	sa, err := svc.ExposeService(ctx, org, a.ID, "s", "ns", "any", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := svc.ExposeService(ctx, org, b.ID, "s", "ns", "any", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Each got the low VIP of its OWN range — proof the used-set is per-cluster.
	if sa.Vip.String() != "100.64.0.2" || sb.Vip.String() != "100.65.0.2" {
		t.Fatalf("cluster-scoped allocation expected 100.64.0.2 / 100.65.0.2, got %s / %s", sa.Vip, sb.Vip)
	}
}
