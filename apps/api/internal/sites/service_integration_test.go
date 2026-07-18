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
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org); _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor) })
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
