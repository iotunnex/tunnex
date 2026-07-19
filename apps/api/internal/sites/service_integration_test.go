package sites

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"reflect"
	"strings"
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

// TestDeleteSiteCascadesAndAudits — S8.3 D4: deleteSite removes the site and CASCADES its subnets + any
// policy rule naming it (src/dst), and UNBINDS the gateway. GetReferences reports the real counts the UI
// previews; the audit records those counts (never "may affect"); a re-delete is a clean 404.
func TestDeleteSiteCascadesAndAudits(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	q := sqlc.New(pool)
	ctx := context.Background()

	org, user, node, grp := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id, name, slug) VALUES ($1,'S',$2)`, org, "del-"+org.String()[:8])
	ex(`INSERT INTO users (id, email, name) VALUES ($1,$2,'U')`, user, "del-"+user.String()[:8]+"@t.io")
	ex(`INSERT INTO nodes (id, org_id, name, cert_serial) VALUES ($1,$2,'gw',$3)`, node, org, "cs-"+node.String()[:8])
	ex(`INSERT INTO user_groups (id, org_id, name) VALUES ($1,$2,'g')`, grp, org)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })

	site, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.BindNode(ctx, org, site.ID, node); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix("10.20.0.0/24")); err != nil {
		t.Fatalf("subnet: %v", err)
	}
	// A policy rule naming the site as dst → referenced (must cascade on delete).
	ex(`INSERT INTO policy_rules (id, org_id, src_kind, src_group_id, dst_kind, dst_site_id)
	    VALUES ($1,$2,'group',$3,'site',$4)`, uuid.New(), org, grp, site.ID)

	// GetReferences reports the real cascade counts the UI previews.
	refs, err := svc.GetReferences(ctx, org, site.ID)
	if err != nil || refs.RuleCount != 1 || refs.SubnetCount != 1 {
		t.Fatalf("references must be {rules:1, subnets:1}, got %+v (%v)", refs, err)
	}

	if err := svc.DeleteSite(ctx, user, org, site.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Site gone; the referencing rule + subnet cascaded; the gateway unbound.
	if _, err := svc.GetSite(ctx, org, site.ID); err == nil {
		t.Fatal("site must be gone after delete")
	}
	var rules, subs int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM policy_rules WHERE dst_site_id=$1`, site.ID).Scan(&rules)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM site_subnets WHERE site_id=$1`, site.ID).Scan(&subs)
	if rules != 0 || subs != 0 {
		t.Fatalf("cascade must remove referencing rules + subnets, got rules=%d subs=%d", rules, subs)
	}
	n, err := q.GetSiteNode(ctx, pgtype.UUID{Bytes: site.ID, Valid: true})
	if err == nil {
		t.Fatalf("the gateway must be unbound after site delete, still bound: %v", n.ID)
	}
	// The audit row records the REAL cascade counts (never "may affect").
	var auditRules, auditSubs int
	err = pool.QueryRow(ctx, `SELECT (metadata->>'rules_deleted')::int, (metadata->>'subnets_released')::int
	    FROM audit_logs WHERE org_id=$1 AND action='site.deleted'`, org).Scan(&auditRules, &auditSubs)
	if err != nil || auditRules != 1 || auditSubs != 1 {
		t.Fatalf("site.deleted audit must record real counts {1,1}, got {%d,%d} (%v)", auditRules, auditSubs, err)
	}
	// A re-delete is a clean 404, not a crash.
	if err := svc.DeleteSite(ctx, user, org, site.ID); err == nil {
		t.Fatal("re-deleting an absent site must be a 404")
	}
}

// TestApproveSubnetDisjointness — S8.1 Slice-4 D5/D7: approval runs the ONE disjointness validator.
// A subnet overlapping an APPROVED site subnet or the pool is REFUSED (typed, stays pending, audited);
// an ADJACENT (touching-but-disjoint) subnet is APPROVED; approvals + refusals are both audited.
func TestApproveSubnetDisjointness(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "adv-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	site, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	add := func(cidr string) sqlc.SiteSubnet {
		s, e := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix(cidr))
		if e != nil {
			t.Fatalf("add %s: %v", cidr, e)
		}
		return s
	}
	status := func(id uuid.UUID) string {
		var st string
		_ = pool.QueryRow(ctx, `SELECT status FROM site_subnets WHERE id=$1`, id).Scan(&st)
		return st
	}

	// Disjoint → APPROVED.
	s1 := add("10.20.0.0/24")
	if err := svc.ApproveSubnet(ctx, actor, org, s1.ID); err != nil {
		t.Fatalf("disjoint approve: %v", err)
	}
	if status(s1.ID) != "approved" {
		t.Fatal("disjoint subnet must be approved")
	}
	// Overlaps the approved s1 → REFUSED (stays pending).
	s2 := add("10.20.0.128/25")
	if err := svc.ApproveSubnet(ctx, actor, org, s2.ID); err == nil || status(s2.ID) != "pending" {
		t.Fatalf("overlapping-approved must be refused + stay pending (err=%v status=%s)", err, status(s2.ID))
	}
	// Overlaps the POOL (10.99.0.0/24) → REFUSED.
	s3 := add("10.99.0.0/25")
	if err := svc.ApproveSubnet(ctx, actor, org, s3.ID); err == nil {
		t.Fatal("overlapping-pool must be refused")
	}
	// ADJACENT to approved s1 (10.20.1.0/24 touches but does not overlap 10.20.0.0/24) → APPROVED.
	s4 := add("10.20.1.0/24")
	if err := svc.ApproveSubnet(ctx, actor, org, s4.ID); err != nil || status(s4.ID) != "approved" {
		t.Fatalf("adjacent-but-disjoint must approve (err=%v status=%s)", err, status(s4.ID))
	}

	var refused, approved int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='site.subnet_approval_refused'`, org).Scan(&refused)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='site.subnet_approved'`, org).Scan(&approved)
	if refused < 2 {
		t.Fatalf("both refusals must be audited (outcome-not-error), got %d", refused)
	}
	if approved < 2 {
		t.Fatalf("both approvals must be audited, got %d", approved)
	}
}

// TestApproveSubnetCrossSiteDuplicate is the #1 story-end-review regression red. site_subnets
// uniqueness is per-SITE, so two DIFFERENT sites CAN advertise the same CIDR — and the disjointness
// check must catch it. A prior candidate-self-filter (a.Cidr != sub.Cidr) BYPASSED exactly this class;
// the original red's fixture had only ONE site, so it passed green over the bypass. This is the missing
// fixture family: two sites, same CIDR (exact) + a contained CIDR (containment) — both must refuse.
func TestApproveSubnetCrossSiteDuplicate(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "xd-"+org.String()[:8]); e != nil {
		t.Fatalf("org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "x-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	siteA, _ := svc.RegisterSite(ctx, org, "site-a")
	siteB, _ := svc.RegisterSite(ctx, org, "site-b")
	addTo := func(siteID uuid.UUID, cidr string) sqlc.SiteSubnet {
		s, e := svc.AddSubnet(ctx, org, siteID, netip.MustParsePrefix(cidr))
		if e != nil {
			t.Fatalf("add %s: %v", cidr, e)
		}
		return s
	}
	status := func(id uuid.UUID) string {
		var st string
		_ = pool.QueryRow(ctx, `SELECT status FROM site_subnets WHERE id=$1`, id).Scan(&st)
		return st
	}
	// Approve 10.30.0.0/24 on site A.
	a1 := addTo(siteA.ID, "10.30.0.0/24")
	if err := svc.ApproveSubnet(ctx, actor, org, a1.ID); err != nil {
		t.Fatalf("approve A: %v", err)
	}
	// site B, EXACT-DUPLICATE CIDR across sites → REFUSED (the bypass class the filter exempted).
	b1 := addTo(siteB.ID, "10.30.0.0/24")
	if err := svc.ApproveSubnet(ctx, actor, org, b1.ID); err == nil || status(b1.ID) != "pending" {
		t.Fatalf("an exact-duplicate CIDR across sites must be refused (err=%v status=%s)", err, status(b1.ID))
	}
	// site B, CONTAINED CIDR (10.30.0.0/25 ⊂ site A's /24) → REFUSED (containment via the same path).
	b2 := addTo(siteB.ID, "10.30.0.0/25")
	if err := svc.ApproveSubnet(ctx, actor, org, b2.ID); err == nil || status(b2.ID) != "pending" {
		t.Fatalf("a contained CIDR across sites must be refused (err=%v status=%s)", err, status(b2.ID))
	}
}

// TestUnbindSiteNode is the #3 fold red (D6 replace-node via API): UnbindSiteNode detaches the site's
// gateway; a second unbind (no gateway) is a typed 404.
func TestUnbindSiteNode(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	q := sqlc.New(pool)
	ctx := context.Background()
	org, node := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'S',$2)`, org, "ub-"+org.String()[:8]); e != nil {
		t.Fatalf("org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO nodes (id,org_id,name,cert_serial) VALUES ($1,$2,'gw',$3)`, node, org, "cs-ub-"+node.String()[:8]); e != nil {
		t.Fatalf("node: %v", e)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })
	site, _ := svc.RegisterSite(ctx, org, "hq")
	if err := svc.BindNode(ctx, org, site.ID, node); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Unbind detaches the gateway.
	if err := svc.UnbindSiteNode(ctx, org, site.ID); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if _, err := q.GetSiteNode(ctx, pgtype.UUID{Bytes: site.ID, Valid: true}); err == nil {
		t.Fatal("the site must have no bound gateway after unbind")
	}
	// A second unbind (no gateway) is a typed 404.
	if err := svc.UnbindSiteNode(ctx, org, site.ID); err == nil {
		t.Fatal("unbinding a site with no gateway must 404")
	}
}

// TestDNSForwardCRUD — S8.4 D7: a forwarded zone is added (resolver validated inside an approved subnet),
// a duplicate domain on ANOTHER site is refused (D1-addition), removal is audited + full-sweep.
func TestDNSForwardCRUD(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "dns-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	siteA, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	sub, err := svc.AddSubnet(ctx, org, siteA.ID, netip.MustParsePrefix("10.20.0.0/24"))
	if err != nil {
		t.Fatalf("add subnet: %v", err)
	}
	if err := svc.ApproveSubnet(ctx, actor, org, sub.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Resolver NOT in an approved subnet → refused.
	if err := svc.SetDNSForward(ctx, actor, org, siteA.ID, "corp.local", "192.168.9.9"); err == nil {
		t.Fatal("a resolver outside the site's approved subnets must be refused")
	}
	// Resolver inside the approved subnet → accepted + audited + compiled.
	if err := svc.SetDNSForward(ctx, actor, org, siteA.ID, "Corp.Local.", "10.20.0.53"); err != nil {
		t.Fatalf("set dns forward: %v", err)
	}
	fwds, _ := svc.q.ListSiteDNSForwardsForOrg(ctx, org)
	if len(fwds) == 0 {
		t.Fatal("the forward must be persisted")
	}
	var audited int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='site.dns_forwarding_set'`, org).Scan(&audited)
	if audited != 1 {
		t.Fatalf("set must be audited; got %d", audited)
	}

	// Another site claiming the same domain (normalized) → conflict.
	siteB, _ := svc.RegisterSite(ctx, org, "branch")
	subB, _ := svc.AddSubnet(ctx, org, siteB.ID, netip.MustParsePrefix("10.30.0.0/24"))
	_ = svc.ApproveSubnet(ctx, actor, org, subB.ID)
	if err := svc.SetDNSForward(ctx, actor, org, siteB.ID, "corp.local", "10.30.0.53"); err == nil {
		t.Fatal("a domain already forwarded by another site must conflict (one zone → one resolver)")
	}

	// Remove → gone + audited.
	if err := svc.RemoveDNSForward(ctx, actor, org, siteA.ID, "corp.local"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var removed int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='site.dns_forwarding_removed'`, org).Scan(&removed)
	if removed != 1 {
		t.Fatalf("remove must be audited; got %d", removed)
	}
	// After removal siteB may now claim it (no longer a conflict).
	if err := svc.SetDNSForward(ctx, actor, org, siteB.ID, "corp.local", "10.30.0.53"); err != nil {
		t.Fatalf("after removal the domain is free for another site: %v", err)
	}
}

// TestRemoveSubnet — WF-5: a mis-advertised subnet is removable without deleting the whole site; the
// removal is audited, and it is org-scoped (a foreign org can't remove it).
func TestRemoveSubnet(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "rm-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	site, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	sub, err := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix("10.20.0.0/24"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Cross-org safety: a DIFFERENT org can't remove this subnet.
	if err := svc.RemoveSubnet(ctx, actor, uuid.New(), sub.ID); err == nil {
		t.Fatal("removing a subnet from a foreign org must fail (subnet_not_found)")
	}
	var stillThere int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM site_subnets WHERE id=$1`, sub.ID).Scan(&stillThere)
	if stillThere != 1 {
		t.Fatal("a cross-org remove must NOT delete the subnet")
	}
	// Owning org removes it → gone + audited.
	if err := svc.RemoveSubnet(ctx, actor, org, sub.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	var gone int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM site_subnets WHERE id=$1`, sub.ID).Scan(&gone)
	if gone != 0 {
		t.Fatal("the subnet must be removed")
	}
	var audited int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='site.subnet_removed'`, org).Scan(&audited)
	if audited != 1 {
		t.Fatalf("the removal must be audited (site.subnet_removed); got %d", audited)
	}
}

// TestRemoveSubnetSweepsDependentDNS (F4) — the full-sweep law's DNS instance: removing a subnet also
// removes any DNS forward whose resolver lived inside it (that resolver is now unrouted), in the same tx;
// a forward with a resolver elsewhere survives. The swept set is named in the audit.
func TestRemoveSubnetSweepsDependentDNS(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "sw-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	site, err := svc.RegisterSite(ctx, org, "hq")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Two approved subnets; a forward resolver in each.
	subA, _ := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix("10.20.0.0/24"))
	subB, _ := svc.AddSubnet(ctx, org, site.ID, netip.MustParsePrefix("10.30.0.0/24"))
	if err := svc.ApproveSubnet(ctx, actor, org, subA.ID); err != nil {
		t.Fatalf("approve A: %v", err)
	}
	if err := svc.ApproveSubnet(ctx, actor, org, subB.ID); err != nil {
		t.Fatalf("approve B: %v", err)
	}
	if err := svc.SetDNSForward(ctx, actor, org, site.ID, "corp.local", "10.20.0.53"); err != nil {
		t.Fatalf("forward A: %v", err)
	}
	if err := svc.SetDNSForward(ctx, actor, org, site.ID, "branch.local", "10.30.0.53"); err != nil {
		t.Fatalf("forward B: %v", err)
	}
	// Remove subnet A → corp.local (resolver 10.20.0.53) swept; branch.local survives.
	if err := svc.RemoveSubnet(ctx, actor, org, subA.ID); err != nil {
		t.Fatalf("remove subnet A: %v", err)
	}
	left, _ := svc.ListDNSForwards(ctx, org, site.ID)
	if len(left) != 1 || left[0].Domain != "branch.local" {
		t.Fatalf("only the in-subnet forward must be swept; left=%+v", left)
	}
	var meta string
	_ = pool.QueryRow(ctx, `SELECT metadata::text FROM audit_logs WHERE org_id=$1 AND action='site.subnet_removed' ORDER BY created_at DESC LIMIT 1`, org).Scan(&meta)
	if !strings.Contains(meta, "corp.local") {
		t.Fatalf("the removal audit must name the swept forward; meta=%s", meta)
	}
}

// TestListRoutedRanges (S8.5 Slice 2b) — the routed-ranges channel: APPROVED-only, org-scoped (the
// comparison-set law's hidden collision: the SAME CIDR declared in two orgs, org A sees only its own),
// canonical + sorted + deterministic, and EMPTY is a first-class answer.
func TestListRoutedRanges(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	orgA, orgB, orgC, actor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, org := range []uuid.UUID{orgA, orgB, orgC} {
		if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "rr-"+org.String()[:8]); e != nil {
			t.Fatalf("seed org %d: %v", i, e)
		}
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		for _, org := range []uuid.UUID{orgA, orgB, orgC} {
			_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		}
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	approve := func(org uuid.UUID, site string, cidr string) {
		s, err := svc.RegisterSite(ctx, org, site)
		if err != nil {
			t.Fatalf("register %s: %v", site, err)
		}
		sub, err := svc.AddSubnet(ctx, org, s.ID, netip.MustParsePrefix(cidr))
		if err != nil {
			t.Fatalf("add %s: %v", cidr, err)
		}
		if err := svc.ApproveSubnet(ctx, actor, org, sub.ID); err != nil {
			t.Fatalf("approve %s: %v", cidr, err)
		}
	}
	// org A: two APPROVED (added out of sorted order) + one PENDING (advertised, NOT approved).
	approve(orgA, "hq", "10.20.0.0/24")
	approve(orgA, "dc", "10.10.0.0/24")
	siteP, _ := svc.RegisterSite(ctx, orgA, "pending-site")
	if _, err := svc.AddSubnet(ctx, orgA, siteP.ID, netip.MustParsePrefix("10.30.0.0/24")); err != nil {
		t.Fatalf("add pending: %v", err)
	} // left pending
	// org B: the SAME 10.20.0.0/24 (cross-org — allowed; disjointness is per-org) + one of its own.
	approve(orgB, "b1", "10.20.0.0/24")
	approve(orgB, "b2", "172.16.0.0/24")

	// (1) approved-only + cross-org: org A returns ONLY its two APPROVED, sorted+canonical, pending EXCLUDED,
	// org B's collision EXCLUDED.
	got, err := svc.ListRoutedRanges(ctx, orgA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if want := []string{"10.10.0.0/24", "10.20.0.0/24"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("org A ranges: got %v want %v (pending must be absent, org B's collision must not leak)", got, want)
	}
	// (2) deterministic: a second call is byte-identical (the client's churn-free merge depends on it).
	if got2, _ := svc.ListRoutedRanges(ctx, orgA); !reflect.DeepEqual(got, got2) {
		t.Fatalf("non-deterministic: %v vs %v", got, got2)
	}
	// (3) org B sees only its own (the collision belongs to whoever declared it, scoped).
	if gotB, _ := svc.ListRoutedRanges(ctx, orgB); !reflect.DeepEqual(gotB, []string{"10.20.0.0/24", "172.16.0.0/24"}) {
		t.Fatalf("org B ranges: %v", gotB)
	}
	// (4) empty is first-class: a no-ranges org returns [] (non-nil, len 0), zero client work downstream.
	gotC, err := svc.ListRoutedRanges(ctx, orgC)
	if err != nil {
		t.Fatalf("list C: %v", err)
	}
	if gotC == nil || len(gotC) != 0 {
		t.Fatalf("empty must be a non-nil [], got %#v", gotC)
	}
}

// TestListRoutedForwardsGating (S8.5 Slice 3, D4) — the DNS handoff's split-horizon gate: a forward is
// handed to a device ONLY if its resolver is REACHABLE via the device's routed ranges. Two sites each
// declare an approved subnet + a forwarded zone; with BOTH ranges routed, both forwards return — but drop
// one range from the routed set and its site's forward vanishes (a resolver you can't reach is never a
// SERVFAIL generator wearing a feature's face). Empty ranges → zero forwards, first-class.
func TestListRoutedForwardsGating(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	org, actor := uuid.New(), uuid.New()
	if _, e := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'S',$2,'10.99.0.0/24')`, org, "rf-"+org.String()[:8]); e != nil {
		t.Fatalf("seed org: %v", e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com"); e != nil {
		t.Fatalf("seed actor: %v", e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	mkSite := func(name, cidr, domain, resolver string) {
		s, err := svc.RegisterSite(ctx, org, name)
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		sub, err := svc.AddSubnet(ctx, org, s.ID, netip.MustParsePrefix(cidr))
		if err != nil {
			t.Fatalf("add %s: %v", cidr, err)
		}
		if err := svc.ApproveSubnet(ctx, actor, org, sub.ID); err != nil {
			t.Fatalf("approve %s: %v", cidr, err)
		}
		if err := svc.SetDNSForward(ctx, actor, org, s.ID, domain, resolver); err != nil {
			t.Fatalf("forward %s: %v", domain, err)
		}
	}
	mkSite("hq", "10.20.0.0/24", "corp.local", "10.20.0.53")
	mkSite("branch", "10.30.0.0/24", "branch.internal", "10.30.0.53")

	ranges, err := svc.ListRoutedRanges(ctx, org)
	if err != nil {
		t.Fatalf("ranges: %v", err)
	}
	// (1) BOTH ranges routed → BOTH forwards reachable, domain-sorted + deduped.
	both, err := svc.ListRoutedForwards(ctx, org, ranges)
	if err != nil {
		t.Fatalf("forwards: %v", err)
	}
	want := []DNSForward{{Domain: "branch.internal", ResolverIP: "10.30.0.53"}, {Domain: "corp.local", ResolverIP: "10.20.0.53"}}
	if !reflect.DeepEqual(both, want) {
		t.Fatalf("both-reachable forwards: got %v want %v", both, want)
	}
	// (2) GATE: drop the branch range from the routed set → branch's resolver is unreachable → its forward
	// is EXCLUDED (by construction, not assumed). Only corp.local survives.
	gated, err := svc.ListRoutedForwards(ctx, org, []string{"10.20.0.0/24"})
	if err != nil {
		t.Fatalf("gated forwards: %v", err)
	}
	if !reflect.DeepEqual(gated, []DNSForward{{Domain: "corp.local", ResolverIP: "10.20.0.53"}}) {
		t.Fatalf("gated forwards: got %v — an unreachable resolver must NOT be handed over", gated)
	}
	// (3) empty ranges → zero forwards, non-nil [].
	none, err := svc.ListRoutedForwards(ctx, org, nil)
	if err != nil {
		t.Fatalf("empty-range forwards: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("no reachable ranges must yield a non-nil [], got %#v", none)
	}
}

// TestRouteLANByteIdentical (S8.5 Slice 2d, D1) — the one-screen RouteLAN produces DB state + an audit
// trail BYTE-IDENTICAL to the four-step long ceremony. Same code composed, so the short path is exactly
// as auditable as the long one (four constituent events, never a composite).
func TestRouteLANByteIdentical(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool)
	ctx := context.Background()
	orgA, orgB, actor, nodeA, nodeB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'A',$2,'10.99.0.0/24')`, orgA, "rla-"+orgA.String()[:8])
	ex(`INSERT INTO organizations (id,name,slug,pool_cidr) VALUES ($1,'B',$2,'10.99.0.0/24')`, orgB, "rlb-"+orgB.String()[:8])
	ex(`INSERT INTO users (id,email) VALUES ($1,$2)`, actor, "a-"+actor.String()[:8]+"@ex.com")
	ex(`INSERT INTO nodes (id,org_id,name,cert_serial) VALUES ($1,$2,'gw',$3)`, nodeA, orgA, "cs-a-"+nodeA.String()[:8])
	ex(`INSERT INTO nodes (id,org_id,name,cert_serial) VALUES ($1,$2,'gw',$3)`, nodeB, orgB, "cs-b-"+nodeB.String()[:8])
	t.Cleanup(func() {
		for _, org := range []uuid.UUID{orgA, orgB} {
			_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org)
		}
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	cidr := netip.MustParsePrefix("10.20.0.0/24")

	// SHORT path (org A): one call.
	siteA, subA, err := svc.RouteLAN(ctx, actor, orgA, nodeA, "hq", cidr)
	if err != nil {
		t.Fatalf("routeLAN: %v", err)
	}
	// LONG path (org B): the four manual steps.
	siteB, err := svc.RegisterSite(ctx, orgB, "hq")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.BindNode(ctx, orgB, siteB.ID, nodeB); err != nil {
		t.Fatalf("bind: %v", err)
	}
	subB, err := svc.AddSubnet(ctx, orgB, siteB.ID, cidr)
	if err != nil {
		t.Fatalf("advertise: %v", err)
	}
	if err := svc.ApproveSubnet(ctx, actor, orgB, subB.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// DB STATE: the gateway is bound to the site; the subnet is approved — in BOTH orgs.
	boundNode := func(node uuid.UUID) string {
		var sid uuid.UUID
		_ = pool.QueryRow(ctx, `SELECT site_id FROM nodes WHERE id=$1`, node).Scan(&sid)
		return sid.String()
	}
	if boundNode(nodeA) != siteA.ID.String() || boundNode(nodeB) != siteB.ID.String() {
		t.Fatalf("both gateways must be bound: A=%s(site %s) B=%s(site %s)", boundNode(nodeA), siteA.ID, boundNode(nodeB), siteB.ID)
	}
	subStatus := func(sub uuid.UUID) string {
		var st string
		_ = pool.QueryRow(ctx, `SELECT status FROM site_subnets WHERE id=$1`, sub).Scan(&st)
		return st
	}
	if subStatus(subA.ID) != "approved" || subStatus(subB.ID) != "approved" {
		t.Fatalf("both subnets must be approved: A=%s B=%s", subStatus(subA.ID), subStatus(subB.ID))
	}

	// AUDIT TRAIL: the same multiset of actions in both orgs (the four constituent events, never a composite).
	auditActions := func(org uuid.UUID) []string {
		rows, e := pool.Query(ctx, `SELECT action FROM audit_logs WHERE org_id=$1 ORDER BY action`, org)
		if e != nil {
			t.Fatalf("audit query: %v", e)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			out = append(out, a)
		}
		return out
	}
	aA, aB := auditActions(orgA), auditActions(orgB)
	if !reflect.DeepEqual(aA, aB) {
		t.Fatalf("audit trails must be IDENTICAL (same code, four constituent events): short=%v long=%v", aA, aB)
	}
	if len(aA) == 0 {
		t.Fatal("expected constituent audit events, got none")
	}
}
