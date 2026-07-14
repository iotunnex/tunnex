//go:build enterprise

package policy_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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
