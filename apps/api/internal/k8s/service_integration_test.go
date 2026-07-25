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
	if _, err := svc.RegisterCluster(ctx, org, site, "c-pool", pfx("10.99.0.0/25"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "pool") {
		t.Fatalf("want a pool-class overlap refusal, got %v", err)
	}
	// Overlaps the approved site subnet.
	if _, err := svc.RegisterCluster(ctx, org, site, "c-site", pfx("10.20.0.128/25"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "site_subnet") {
		t.Fatalf("want a site_subnet-class overlap refusal, got %v", err)
	}
	// A disjoint range is accepted.
	if _, err := svc.RegisterCluster(ctx, org, site, "c-ok", pfx("100.64.0.0/16"), pfx("10.96.0.0/12"), "k8s.acme.com"); err != nil {
		t.Fatalf("a disjoint VIP range must be accepted, got %v", err)
	}
}

// TestRegisterClusterRejectsBadName: the cluster name is a DNS label and the zone a DNS domain (they build
// the exposed-Service hostname); malformed values are refused with typed teaching errors, never reach the wire.
func TestRegisterClusterRejectsBadName(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	if _, err := svc.RegisterCluster(ctx, org, site, "Prod Cluster", pfx("100.64.0.0/16"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "invalid_cluster_name") {
		t.Fatalf("want invalid_cluster_name for a non-label name, got %v", err)
	}
	if _, err := svc.RegisterCluster(ctx, org, site, "prod", pfx("100.64.0.0/16"), pfx("10.96.0.0/12"), "not a domain"); err == nil || !strings.Contains(err.Error(), "invalid_dns_zone") {
		t.Fatalf("want invalid_dns_zone for a malformed zone, got %v", err)
	}
}

// TestRegisterClusterRejectsZoneCollidingForwardedDomain — S10.3 (A) cross-mechanism one-zone-one-resolver:
// a cluster whose DNS zone <cluster>.<dns_zone> collides with a domain already forwarded by a site is refused
// (the mirror of the check in sites.SetDNSForward).
func TestRegisterClusterRejectsZoneCollidingForwardedDomain(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	// The site already forwards prod.k8s.acme.com.
	if _, e := pool.Exec(ctx, `UPDATE sites SET dns_forwarding=$2 WHERE id=$1`, site, `[{"domain":"prod.k8s.acme.com","resolver_ip":"10.20.0.53"}]`); e != nil {
		t.Fatal(e)
	}
	// Registering cluster "prod" in zone "k8s.acme.com" would claim that exact zone → refused.
	if _, err := svc.RegisterCluster(ctx, org, site, "prod", pfx("100.64.0.0/16"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "dns_domain_conflict") {
		t.Fatalf("a cluster zone colliding with a forwarded domain must refuse, got %v", err)
	}
}

// TestRegisterClusterReservesDNSVIP: the DNS VIP is the range's first allocatable (.2), reserved at
// registration so a Service can NEVER be handed it (the gateway answers DNS on it). A range too small to
// fit the DNS VIP PLUS one Service VIP is refused honestly.
func TestRegisterClusterReservesDNSVIP(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	// /30 has exactly ONE allocatable (.2) — it would be all DNS, no Service room: refused.
	if _, err := svc.RegisterCluster(ctx, org, site, "tiny", pfx("100.66.0.0/30"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "vip_range_too_small") {
		t.Fatalf("a range with no room past the DNS VIP must refuse (too_small), got %v", err)
	}
	c, err := svc.RegisterCluster(ctx, org, site, "dnsr", pfx("100.66.0.0/29"), pfx("10.96.0.0/12"), "k8s.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.DnsVip == nil || c.DnsVip.String() != "100.66.0.2" {
		t.Fatalf("DNS VIP must be reserved at .2, got %v", c.DnsVip)
	}
	// The first exposed Service gets .3 — proof .2 is reserved and never handed out.
	s1, err := svc.ExposeService(ctx, org, c.ID, "api", "prod", "tcp", nil, nil)
	if err != nil {
		t.Fatalf("first expose: %v", err)
	}
	if s1.Vip.String() != "100.66.0.3" {
		t.Fatalf("first Service VIP must skip the reserved .2, got %s", s1.Vip)
	}
}

// TestSecondClusterCannotOverlapFirst proves the collector's 7th-caller path LIVE: a second cluster's VIP
// range is checked against the FIRST cluster's range (fed in by Collect) and refused with the vip_range class.
func TestSecondClusterCannotOverlapFirst(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)

	if _, err := svc.RegisterCluster(ctx, org, site, "a", pfx("100.64.0.0/16"), pfx("10.96.0.0/12"), "k8s.acme.com"); err != nil {
		t.Fatalf("first cluster: %v", err)
	}
	if _, err := svc.RegisterCluster(ctx, org, site, "b", pfx("100.64.5.0/24"), pfx("10.96.0.0/12"), "k8s.acme.com"); err == nil || !strings.Contains(err.Error(), "vip_range") {
		t.Fatalf("a second cluster overlapping the first's VIP range must be refused (vip_range class), got %v", err)
	}
}

// TestExposeAllocatesThenExhausts: a /29 range yields the DNS VIP (.2, reserved) plus four Service VIPs;
// the fifth expose is refused HONESTLY (vip_range_exhausted), never silently reusing an address.
func TestExposeAllocatesThenExhausts(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, site := seedOrgSite(t, pool)
	c, err := svc.RegisterCluster(ctx, org, site, "small", pfx("100.66.0.0/29"), pfx("10.96.0.0/12"), "k8s.acme.com") // .2 DNS + .3-.6 Services
	if err != nil {
		t.Fatal(err)
	}
	svc1, err := svc.ExposeService(ctx, org, c.ID, "api", "prod", "tcp", nil, nil)
	if err != nil {
		t.Fatalf("first expose: %v", err)
	}
	if svc1.Vip.String() != "100.66.0.3" {
		t.Fatalf("first Service VIP skips the reserved DNS .2, got %s", svc1.Vip)
	}
	// Consume the remaining three Service VIPs (.4, .5, .6).
	for i, n := range []string{"web", "cache", "queue"} {
		if _, err := svc.ExposeService(ctx, org, c.ID, n, "prod", "tcp", nil, nil); err != nil {
			t.Fatalf("expose %d (%s): %v", i, n, err)
		}
	}
	if _, err := svc.ExposeService(ctx, org, c.ID, "extra", "prod", "tcp", nil, nil); err == nil || !strings.Contains(err.Error(), "exhausted") {
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
	a, err := svc.RegisterCluster(ctx, org, site, "ca", pfx("100.64.0.0/28"), pfx("10.96.0.0/12"), "k8s.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.RegisterCluster(ctx, org, site, "cb", pfx("100.65.0.0/28"), pfx("10.96.0.0/12"), "k8s.acme.com")
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
	// Each got the low Service VIP of its OWN range (.3, past the reserved DNS .2) — used-set is per-cluster.
	if sa.Vip.String() != "100.64.0.3" || sb.Vip.String() != "100.65.0.3" {
		t.Fatalf("cluster-scoped allocation expected 100.64.0.3 / 100.65.0.3, got %s / %s", sa.Vip, sb.Vip)
	}
}
