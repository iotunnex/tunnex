//go:build enterprise

package policy_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// fakeNotifier records the NotifyMany fan-out so push-FIRES tests can assert which
// gateways a mutation signalled.
type fakeNotifier struct{ calls [][]uuid.UUID }

func (f *fakeNotifier) NotifyMany(ids []uuid.UUID) { f.calls = append(f.calls, ids) }
func (f *fakeNotifier) fired() bool                { return len(f.calls) > 0 }

// fixture seeds an org + verified owner + active node + one active FULL-TUNNEL device,
// returning the ids. Raw inserts keep the test independent of the higher services.
type fixture struct {
	org, user, node, device uuid.UUID
	ctx                     context.Context
}

func seed(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{org: uuid.New(), user: uuid.New(), node: uuid.New(), device: uuid.New()}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}
	exec(`INSERT INTO organizations (id, name, slug) VALUES ($1,$2,$3)`, f.org, "ZT Org", "zt-"+f.org.String()[:8])
	exec(`INSERT INTO users (id, email) VALUES ($1,$2)`, f.user, "owner-"+f.user.String()[:8]+"@ex.com")
	exec(`INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,'owner')`, f.org, f.user)
	exec(`INSERT INTO nodes (id, org_id, name, cert_serial) VALUES ($1,$2,'gw',$3)`, f.node, f.org, "serial-"+f.node.String())
	exec(`INSERT INTO devices (id, org_id, user_id, node_id, name, public_key, assigned_ip, full_tunnel)
	      VALUES ($1,$2,$3,$4,'laptop','pk','10.99.0.10',true)`, f.device, f.org, f.user, f.node)
	// An authed owner principal so mutations pass their own membership checks.
	f.ctx = authctx.WithOrg(authctx.WithPrincipal(ctx,
		&authctx.Principal{UserID: f.user, EmailVerified: true, Roles: map[uuid.UUID]string{f.org: "owner"}}), f.org)
	// cleanup cascades from the org.
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, f.org) })
	return f
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

// AffectedNodeIDs (S7.1-ledgered direct test): the revocation-push targeting function
// returns exactly the org's ACTIVE gateways.
func TestAffectedNodeIDsTargetsActiveOrgNodes(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	svc := policy.NewService(pool)

	ids, err := svc.AffectedNodeIDs(f.ctx, f.org)
	if err != nil {
		t.Fatalf("AffectedNodeIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != f.node {
		t.Fatalf("want [%s], got %v", f.node, ids)
	}

	// A REVOKED node drops out of the target set.
	if _, err := pool.Exec(f.ctx, `UPDATE nodes SET status='revoked' WHERE id=$1`, f.node); err != nil {
		t.Fatal(err)
	}
	if ids, _ := svc.AffectedNodeIDs(f.ctx, f.org); len(ids) != 0 {
		t.Fatalf("revoked node must not be a push target, got %v", ids)
	}
}

// Per-trigger push-FIRES: each compiler-input mutation signals the org's gateways.
func TestMutationsFirePush(t *testing.T) {
	pool := testPool(t)

	newSvc := func() (*policy.Service, *fakeNotifier) {
		n := &fakeNotifier{}
		s := policy.NewService(pool)
		s.SetNotifier(n)
		return s, n
	}

	t.Run("create group", func(t *testing.T) {
		f := seed(t, pool)
		s, n := newSvc()
		if _, err := s.CreateGroup(f.ctx, f.org, "eng", ""); err != nil {
			t.Fatal(err)
		}
		if !n.fired() || n.calls[0][0] != f.node {
			t.Fatalf("create group did not push the org node: %v", n.calls)
		}
	})

	t.Run("add + remove member", func(t *testing.T) {
		f := seed(t, pool)
		s, n := newSvc()
		g, err := s.CreateGroup(f.ctx, f.org, "admins", "")
		if err != nil {
			t.Fatal(err)
		}
		before := len(n.calls)
		if err := s.AddGroupMember(f.ctx, f.org, g.ID, f.user); err != nil {
			t.Fatal(err)
		}
		if len(n.calls) <= before {
			t.Fatal("add member did not push")
		}
		mid := len(n.calls)
		if err := s.RemoveGroupMember(f.ctx, f.org, g.ID, f.user); err != nil {
			t.Fatal(err)
		}
		if len(n.calls) <= mid {
			t.Fatal("remove member did not push")
		}
	})

	t.Run("resource + rule + mode", func(t *testing.T) {
		f := seed(t, pool)
		s, n := newSvc()
		g, _ := s.CreateGroup(f.ctx, f.org, "g", "")
		res, err := s.CreateResource(f.ctx, f.org, policyResource())
		if err != nil {
			t.Fatal(err)
		}
		rid := res.ID
		fired := len(n.calls)
		if _, err := s.CreatePolicyRule(f.ctx, f.org, ruleTo(g.ID, rid)); err != nil {
			t.Fatal(err)
		}
		if len(n.calls) <= fired {
			t.Fatal("create rule did not push")
		}
		before := len(n.calls)
		mode, affected, err := s.SetMode(f.ctx, f.org, policy.ModeEnforcing)
		if err != nil {
			t.Fatal(err)
		}
		if len(n.calls) <= before {
			t.Fatal("set mode did not push")
		}
		if mode != policy.ModeEnforcing {
			t.Fatalf("mode = %q", mode)
		}
		// Mode-enable ENUMERATION (2a): the seeded full-tunnel device is reported.
		if len(affected) != 1 || affected[0].ID != f.device {
			t.Fatalf("enable enforcing must enumerate the full-tunnel device, got %v", affected)
		}
		// Disabling returns no affected list.
		_, off, err := s.SetMode(f.ctx, f.org, policy.ModeOff)
		if err != nil {
			t.Fatal(err)
		}
		if len(off) != 0 {
			t.Fatalf("disabling must not enumerate devices, got %v", off)
		}
	})
}

func policyResource() policyspec.ResourceInput {
	return policyspec.ResourceInput{Name: "db", CIDR: "10.0.5.0/24", Protocol: "any"}
}

func ruleTo(srcGroup, dstResource uuid.UUID) policyspec.RuleInput {
	return policyspec.RuleInput{SrcGroupID: srcGroup, DstKind: "resource", DstResourceID: &dstResource}
}

// TestPerUserGrantDropsOnMemberRemoval is the S7.5.4 D1 rider proof (the F1
// committed-removal-must-push class): a per-user grant's src_user_id → memberships
// ON DELETE CASCADE deletes the rule row STRUCTURALLY when the member is removed —
// AND that removal must reach the WIRE: the compiled artifact, rebuilt after the
// cascade, must no longer contain that user's /32. Cascade-correct-in-DB but
// stale-in-compile would be the S7.5.2 committed-removal-must-push bug.
func TestPerUserGrantDropsOnMemberRemoval(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	ctx := f.ctx
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	// A second member (bob) with an active device — the subject of the per-user grant.
	bob, bobDev := uuid.New(), uuid.New()
	exec(`INSERT INTO users (id, email) VALUES ($1,$2)`, bob, "bob-"+bob.String()[:8]+"@ex.com")
	exec(`INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,'member')`, f.org, bob)
	exec(`INSERT INTO devices (id, org_id, user_id, node_id, name, public_key, assigned_ip)
	      VALUES ($1,$2,$3,$4,'bob-laptop','pkbob','10.99.0.11')`, bobDev, f.org, bob, f.node)

	s := policy.NewService(pool)
	s.SetNotifier(&fakeNotifier{})
	res, err := s.CreateResource(ctx, f.org, policyResource())
	if err != nil {
		t.Fatal(err)
	}
	rid := res.ID
	// A PER-USER grant for bob (not a group).
	if _, err := s.CreatePolicyRule(ctx, f.org, policyspec.RuleInput{
		SrcKind: "user", SrcUserID: &bob, DstKind: "resource", DstResourceID: &rid,
	}); err != nil {
		t.Fatalf("create per-user rule: %v", err)
	}
	if _, _, err := s.SetMode(ctx, f.org, policy.ModeEnforcing); err != nil {
		t.Fatal(err)
	}

	// BEFORE removal: bob's /32 is granted the resource in the compiled artifact.
	compiledHas := func(srcIP string) bool {
		snap, err := s.BuildSnapshot(context.Background(), f.org)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for _, c := range policy.Compile(snap) {
			for _, e := range c.Allow {
				if e.SrcIP == srcIP && e.DstCIDR == "10.0.5.0/24" {
					return true
				}
			}
		}
		return false
	}
	if !compiledHas("10.99.0.11") {
		t.Fatal("per-user grant must put bob's /32 in the compiled artifact before removal")
	}

	// REMOVE bob from the org (delete the memberships row — the cascade trigger).
	exec(`DELETE FROM memberships WHERE org_id=$1 AND user_id=$2`, f.org, bob)

	// (a) STRUCTURAL cascade: the per-user policy_rules row is gone.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM policy_rules WHERE org_id=$1 AND src_user_id=$2`, f.org, bob).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("membership removal must cascade-delete the per-user grant, %d rows remain", n)
	}
	// (b) WIRE freshness: the rebuilt artifact no longer grants bob's /32.
	if compiledHas("10.99.0.11") {
		t.Fatal("cascade-correct but gateway-STALE: bob's /32 still in the compiled artifact after removal")
	}
}

// tempGrant creates a group→resource rule expiring at `at` (raw, so tests can place
// it in the past to simulate a lapsed grant the API would refuse to create).
func tempGrant(t *testing.T, pool *pgxpool.Pool, f fixture, at time.Time) (ruleID, groupID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	groupID, res := uuid.New(), uuid.New()
	mustExec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	uniq := groupID.String()[:8] // group/resource names are unique per org
	mustExec(`INSERT INTO user_groups (id, org_id, name) VALUES ($1,$2,$3)`, groupID, f.org, "g-"+uniq)
	mustExec(`INSERT INTO group_members (org_id, group_id, user_id) VALUES ($1,$2,$3)`, f.org, groupID, f.user)
	mustExec(`INSERT INTO resources (id, org_id, name, cidr, protocol) VALUES ($1,$2,$3,'10.0.5.0/24','any')`, res, f.org, "db-"+uniq)
	ruleID = uuid.New()
	mustExec(`INSERT INTO policy_rules (id, org_id, src_kind, src_group_id, dst_kind, dst_resource_id, expires_at)
	          VALUES ($1,$2,'group',$3,'resource',$4,$5)`, ruleID, f.org, groupID, res, at)
	return ruleID, groupID
}

// code extracts an apierr code for asserting typed 4xx failures.
func code(err error) string {
	var a *apierr.Error
	if err != nil && errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func auditCount(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action=$2`, org, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestExtendGrantWindow — the happy path: a live temporary grant's window moves in place.
func TestExtendGrantWindow(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	rid, _ := tempGrant(t, pool, f, time.Now().Add(30*time.Minute))
	s := policy.NewService(pool)
	s.SetNotifier(&fakeNotifier{})
	newExp := time.Now().Add(4 * time.Hour)
	r, err := s.ExtendGrant(f.ctx, f.org, rid, newExp)
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if !r.ExpiresAt.Valid || r.ExpiresAt.Time.Sub(newExp).Abs() > time.Second {
		t.Fatalf("window not moved: %+v", r.ExpiresAt)
	}
	if auditCount(t, pool, f.org, "policy.grant_extended") != 1 {
		t.Fatal("extend must audit policy.grant_extended")
	}
}

// TestExtendRefusesPermanentAndLapsed — the two 409s: a permanent grant has no window,
// and a lapsed grant is terminal.
func TestExtendRefusesPermanentAndLapsed(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	s := policy.NewService(pool)
	s.SetNotifier(&fakeNotifier{})

	// permanent: create a normal rule (no expiry) -> not_temporary.
	perm, _ := tempGrant(t, pool, f, time.Now().Add(time.Hour))
	if _, err := pool.Exec(context.Background(), `UPDATE policy_rules SET expires_at=NULL WHERE id=$1`, perm); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExtendGrant(f.ctx, f.org, perm, time.Now().Add(time.Hour)); code(err) != "not_temporary" {
		t.Fatalf("permanent grant extend must be 409 not_temporary, got %v", err)
	}

	// lapsed: a grant already past its expiry -> grant_lapsed.
	lapsed, _ := tempGrant(t, pool, f, time.Now().Add(-time.Minute))
	if _, err := s.ExtendGrant(f.ctx, f.org, lapsed, time.Now().Add(time.Hour)); code(err) != "grant_lapsed" {
		t.Fatalf("lapsed grant extend must be 409 grant_lapsed, got %v", err)
	}
}

// TestExtendVsSweepRace is the disposition RED: extend and the expiry sweeper compose on
// the row lock, so a grant at its lapse boundary resolves DETERMINISTICALLY to
// extended-OR-409, never torn. The FOR UPDATE lock guarantees that under real concurrency
// exactly ONE of these two serial orderings occurs — both are asserted correct here.
func TestExtendVsSweepRace(t *testing.T) {
	pool := testPool(t)
	s := policy.NewService(pool)
	s.SetNotifier(&fakeNotifier{})

	t.Run("sweep wins -> extend is 409 grant_lapsed, exactly one action", func(t *testing.T) {
		f := seed(t, pool)
		exp := time.Now().Add(-time.Second) // already lapsed
		rid, _ := tempGrant(t, pool, f, exp)
		// Sweeper claims it. n is SYSTEM-WIDE (may include other fixtures' leaked expired
		// grants in the shared DB), so assert on THIS org's audit, not the global count.
		if _, err := s.SweepExpiredGrants(context.Background(), exp.Add(-time.Hour), time.Now()); err != nil {
			t.Fatal(err)
		}
		if auditCount(t, pool, f.org, "policy.grant_expired") != 1 {
			t.Fatal("sweep must audit grant_expired once for this org")
		}
		// Extend now finds it lapsed -> 409. No double-action (no grant_extended).
		if _, err := s.ExtendGrant(f.ctx, f.org, rid, time.Now().Add(time.Hour)); code(err) != "grant_lapsed" {
			t.Fatalf("post-sweep extend must be grant_lapsed, got %v", err)
		}
		if auditCount(t, pool, f.org, "policy.grant_extended") != 0 {
			t.Fatal("a lapsed grant must NOT also record an extend (torn state)")
		}
	})

	t.Run("extend wins -> sweep does NOT falsely expire it", func(t *testing.T) {
		f := seed(t, pool)
		exp := time.Now().Add(2 * time.Second) // live, near boundary
		rid, _ := tempGrant(t, pool, f, exp)
		// Extend moves the window to the future FIRST.
		if _, err := s.ExtendGrant(f.ctx, f.org, rid, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("extend: %v", err)
		}
		// A sweep whose window would have covered the OLD expiry must not fire — the row
		// now has a future expires_at, so it doesn't match expires_at <= now().
		if _, err := s.SweepExpiredGrants(context.Background(), exp.Add(-time.Hour), time.Now().Add(10*time.Second)); err != nil {
			t.Fatal(err)
		}
		if auditCount(t, pool, f.org, "policy.grant_expired") != 0 {
			t.Fatal("an extended grant must NOT be swept-expired (this org)")
		}
	})
}

// TestSweepPushesOrgWide — a lapsed grant's expiry pushes the org's gateways (F1: the
// /32 must leave every gateway, not just the subject's node) + audits (system actor).
func TestSweepPushesOrgWide(t *testing.T) {
	pool := testPool(t)
	f := seed(t, pool)
	tempGrant(t, pool, f, time.Now().Add(-time.Second))
	n := &fakeNotifier{}
	s := policy.NewService(pool)
	s.SetNotifier(n)
	if _, err := s.SweepExpiredGrants(context.Background(), time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The sweep is system-wide (may push several orgs from the shared DB); assert THIS
	// org's gateway is among the pushes.
	pushedThisNode := false
	for _, call := range n.calls {
		for _, id := range call {
			if id == f.node {
				pushedThisNode = true
			}
		}
	}
	if !pushedThisNode {
		t.Fatalf("expiry sweep must push this org's gateway (%s), got %v", f.node, n.calls)
	}
	var actor *string
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_system FROM audit_logs WHERE org_id=$1 AND action='policy.grant_expired'`, f.org).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor == nil || *actor != "policy-grants" {
		t.Fatalf("expiry must be a SYSTEM-actor audit (policy-grants), got %v", actor)
	}
}

// TestAuditedDeletesPersistMetadata pins the S7.4a-walk finding: every audited DELETE goes
// through writeAudit with nil meta, which inserted SQL NULL into audit_logs.metadata (NOT
// NULL) → 23502 → the mutation 500'd + rolled back (so the rule/group/resource could never
// be deleted via the UI). RED on main for all THREE nil-meta callsites; GREEN once writeAudit
// defaults nil → '{}'. (Latent because no box proof ever deleted an audited entity on the wire.)
func TestAuditedDeletesPersistMetadata(t *testing.T) {
	pool := testPool(t)

	assertAudit := func(t *testing.T, f fixture, action, targetID string) {
		t.Helper()
		var meta []byte
		if err := pool.QueryRow(f.ctx,
			`SELECT metadata FROM audit_logs WHERE org_id=$1 AND action=$2 AND target_id=$3`,
			f.org, action, targetID).Scan(&meta); err != nil {
			t.Fatalf("%s audit row missing: %v", action, err)
		}
		if len(meta) == 0 || string(meta) == "null" {
			t.Fatalf("%s metadata must be non-null JSON, got %q", action, meta)
		}
	}

	t.Run("group.deleted", func(t *testing.T) {
		f := seed(t, pool)
		s := policy.NewService(pool)
		g, err := s.CreateGroup(f.ctx, f.org, "doomed", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteGroup(f.ctx, f.org, g.ID); err != nil {
			t.Fatalf("audited delete errored (nil-meta NOT NULL bug): %v", err)
		}
		assertAudit(t, f, "group.deleted", g.ID.String())
	})

	t.Run("resource.deleted", func(t *testing.T) {
		f := seed(t, pool)
		s := policy.NewService(pool)
		r, err := s.CreateResource(f.ctx, f.org, policyResource())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteResource(f.ctx, f.org, r.ID); err != nil {
			t.Fatalf("audited delete errored (nil-meta NOT NULL bug): %v", err)
		}
		assertAudit(t, f, "resource.deleted", r.ID.String())
	})

	t.Run("policy.rule_deleted", func(t *testing.T) {
		f := seed(t, pool)
		s := policy.NewService(pool)
		g, _ := s.CreateGroup(f.ctx, f.org, "g", "")
		r, err := s.CreateResource(f.ctx, f.org, policyResource())
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreatePolicyRule(f.ctx, f.org, ruleTo(g.ID, r.ID))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePolicyRule(f.ctx, f.org, rule.ID); err != nil {
			t.Fatalf("audited delete errored (nil-meta NOT NULL bug): %v", err)
		}
		assertAudit(t, f, "policy.rule_deleted", rule.ID.String())
	})
}
