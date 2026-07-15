package tenancy

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// DeactivateMemberBySync must audit to a NAMED system actor (actor_system='idp-sync', actor_user_id
// NULL — not a borrowed admin) with the CAUSE in metadata, so a compliance reader sees "revoked by
// idp-sync because disabled_in_directory". Same discipline as device.self_approved.
func TestDeactivateMemberBySyncAuditsSystemActorWithCause(t *testing.T) {
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

	org, member := uuid.New(), uuid.New()
	for _, s := range [][]any{
		{"INSERT INTO organizations (id,name,slug) VALUES ($1,'O',$2)", org, "sync-" + org.String()},
		{"INSERT INTO users (id,email,name) VALUES ($1,$2,'M')", member, member.String()[:8] + "@t.io"},
		{"INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')", org, member},
	} {
		if _, err := tx.Exec(ctx, s[0].(string), s[1:]...); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	svc := &MembershipService{q: sqlc.New(tx), revoker: &fakeRevoker{}}
	if err := svc.DeactivateMemberBySync(ctx, org, member, "disabled_in_directory"); err != nil {
		t.Fatalf("DeactivateMemberBySync: %v", err)
	}

	// The user is frozen.
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, member).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "deactivated" {
		t.Fatalf("user status = %q, want deactivated", status)
	}

	// The audit row names the system actor, has NO human actor, and carries the cause.
	var actorSystem *string
	var actorUser *uuid.UUID
	var action string
	var meta []byte
	err = tx.QueryRow(ctx,
		`SELECT actor_system, actor_user_id, action, metadata FROM audit_logs
		 WHERE org_id=$1 AND action='user.deactivated' ORDER BY created_at DESC LIMIT 1`, org).
		Scan(&actorSystem, &actorUser, &action, &meta)
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if actorUser != nil {
		t.Errorf("actor_user_id must be NULL for a system action, got %v", actorUser)
	}
	if actorSystem == nil || *actorSystem != "idp-sync" {
		t.Fatalf("actor_system must be 'idp-sync', got %v", actorSystem)
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if m["cause"] != "disabled_in_directory" {
		t.Fatalf("audit metadata cause = %v, want disabled_in_directory", m["cause"])
	}
}
