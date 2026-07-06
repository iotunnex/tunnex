package tenancy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

type fakeRevoker struct{ called []uuid.UUID }

func (f *fakeRevoker) DeleteAllForUser(_ context.Context, id uuid.UUID) error {
	f.called = append(f.called, id)
	return nil
}

func TestDeactivationFreezesAndGuardsLastOwner(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
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

	orgA, orgB := uuid.New(), uuid.New()
	actor, member, soleOwner := uuid.New(), uuid.New(), uuid.New()
	stmts := [][]any{
		{"INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgA, "A", "da-" + orgA.String()},
		{"INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgB, "B", "db-" + orgB.String()},
		{"INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", actor, "actor@t", "Actor"},
		{"INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", member, "member@t", "Member"},
		{"INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", soleOwner, "owner@t", "Owner"},
		{"INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')", orgA, member},
		{"INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'owner')", orgB, soleOwner},
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s[0].(string), s[1:]...); err != nil {
			t.Fatalf("setup %q: %v", s[0], err)
		}
	}

	rev := &fakeRevoker{}
	svc := &MembershipService{q: sqlc.New(tx), revoker: rev}

	// Deactivating a regular member freezes the account and revokes sessions,
	// but keeps the membership (frozen, not deleted).
	if err := svc.DeactivateMember(ctx, actor, orgA, member); err != nil {
		t.Fatalf("deactivate member: %v", err)
	}
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM users WHERE id=$1", member).Scan(&status); err != nil || status != "deactivated" {
		t.Fatalf("status = %q err=%v, want deactivated", status, err)
	}
	if _, err := svc.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgA, UserID: member}); err != nil {
		t.Fatalf("membership should be preserved: %v", err)
	}
	if len(rev.called) != 1 || rev.called[0] != member {
		t.Fatalf("sessions not revoked for member: %v", rev.called)
	}
	// Audit: the deactivation recorded a user.deactivated event attributed to the
	// acting user (watch-item e — every mutation lands in audit_logs with actor).
	var deactivatedRows int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM audit_logs WHERE org_id=$1 AND actor_user_id=$2 AND action='user.deactivated' AND target_id=$3",
		orgA, actor, member.String()).Scan(&deactivatedRows); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if deactivatedRows != 1 {
		t.Fatalf("want 1 actor-attributed user.deactivated audit row, got %d", deactivatedRows)
	}

	// The last-owner invariant blocks deactivating an org's sole owner.
	if err := svc.DeactivateMember(ctx, actor, orgB, soleOwner); !isCode(err, "last_owner") {
		t.Fatalf("deactivate sole owner: want last_owner, got %v", err)
	}

	// Reactivation restores the frozen account.
	if err := svc.ReactivateMember(ctx, actor, orgA, member); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if err := tx.QueryRow(ctx, "SELECT status FROM users WHERE id=$1", member).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
}
