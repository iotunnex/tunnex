//go:build enterprise

package idpsync_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/idpsync"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
	"github.com/tunnexio/tunnex/apps/api/internal/idpsyncspec"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

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

type nopPusher struct{ calls int }

func (p *nopPusher) PushOrgNodes(context.Context, uuid.UUID) { p.calls++ }

type nopDeprov struct{}

func (nopDeprov) DeactivateForSync(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return true, nil
}

func testSealer(t *testing.T) *crypto.Sealer {
	t.Helper()
	s, err := crypto.NewSealer(make([]byte, 32)) // all-zero key is fine for a test
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

func newService(t *testing.T, pool *pgxpool.Pool) *idpsync.Service {
	return idpsync.NewService(pool, testSealer(t), &nopPusher{}, nopDeprov{}, testLogger())
}

// D1 refuse-unless-empty: a POPULATED manual group cannot be converted to directory sync. This is
// the app-layer half of disjointness the schema CHECK can't express (it can't see member count).
func TestMapGroup_RefusesPopulatedManualGroup(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	org, user := uuid.New(), uuid.New()
	grp := uuid.New()
	exec(t, pool, `INSERT INTO organizations (id,name,slug) VALUES ($1,'o',$2)`, org, "o-"+org.String()[:8])
	exec(t, pool, `INSERT INTO users (id,email,name) VALUES ($1,$2,'u')`, user, user.String()[:8]+"@t.io")
	exec(t, pool, `INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')`, org, user)
	exec(t, pool, `INSERT INTO user_groups (id,org_id,name,origin) VALUES ($1,$2,'eng','manual')`, grp, org)
	exec(t, pool, `INSERT INTO group_members (org_id,group_id,user_id,origin) VALUES ($1,$2,$3,'manual')`, org, grp, user)

	svc := newService(t, pool)
	mustConfig(t, svc, ctx, org)

	_, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "g-1", GroupID: &grp})
	if !hasCode(err, 409, "group_not_empty") {
		t.Fatalf("binding a populated manual group must be 409 group_not_empty, got %v", err)
	}

	// After emptying it, the same bind SUCCEEDS and flips origin to idp_sync.
	exec(t, pool, `DELETE FROM group_members WHERE org_id=$1 AND group_id=$2`, org, grp)
	g, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "g-1", GroupID: &grp})
	if err != nil {
		t.Fatalf("binding an EMPTY manual group must succeed, got %v", err)
	}
	if g.Origin != "idp_sync" || g.IdpProvider == nil || *g.IdpProvider != "microsoft" || g.IdpGroupID == nil || *g.IdpGroupID != "g-1" {
		t.Fatalf("bound group not flipped to idp_sync/microsoft/g-1: %+v", g)
	}

	// A second bind of the now-synced group is refused (already directory-managed).
	if _, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "g-2", GroupID: &grp}); !hasCode(err, 409, "group_already_synced") {
		t.Fatalf("re-binding a synced group must be 409 group_already_synced, got %v", err)
	}
}

// Creating a fresh idp_sync group works and the same (provider,idp_group_id) can't be mapped twice.
func TestMapGroup_CreateAndDuplicate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	org := uuid.New()
	exec(t, pool, `INSERT INTO organizations (id,name,slug) VALUES ($1,'o',$2)`, org, "o-"+org.String()[:8])
	svc := newService(t, pool)
	mustConfig(t, svc, ctx, org)

	if _, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "grp-eng", Name: "Engineering"}); err != nil {
		t.Fatalf("create idp_sync group: %v", err)
	}
	if _, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "grp-eng", Name: "Dup"}); !hasCode(err, 409, "conflict") {
		t.Fatalf("mapping the same directory group twice must 409, got %v", err)
	}
}

// D1 other half: an idp_sync group's membership is reconciler-owned, so a MANUAL add/remove is
// refused (409). Together with refuse-unless-empty this makes manual and idp origins disjoint at
// the app layer, above the schema CHECK.
func TestManualEditOfSyncedGroupRefused(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	org, user := uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO organizations (id,name,slug) VALUES ($1,'o',$2)`, org, "o-"+org.String()[:8])
	exec(t, pool, `INSERT INTO users (id,email,name) VALUES ($1,$2,'u')`, user, user.String()[:8]+"@t.io")
	exec(t, pool, `INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')`, org, user)

	svc := newService(t, pool)
	mustConfig(t, svc, ctx, org)
	g, err := svc.MapGroup(ctx, org, "microsoft", idpsyncspec.MapInput{IdpGroupID: "grp-eng", Name: "Engineering"})
	if err != nil {
		t.Fatalf("create synced group: %v", err)
	}

	psvc := policy.NewService(pool)
	if err := psvc.AddGroupMember(ctx, org, g.ID, user); !hasCode(err, 409, "idp_managed_group") {
		t.Fatalf("manual AddGroupMember on a synced group must 409 idp_managed_group, got %v", err)
	}
	if err := psvc.RemoveGroupMember(ctx, org, g.ID, user); !hasCode(err, 409, "idp_managed_group") {
		t.Fatalf("manual RemoveGroupMember on a synced group must 409 idp_managed_group, got %v", err)
	}
}

func mustConfig(t *testing.T, svc *idpsync.Service, ctx context.Context, org uuid.UUID) {
	t.Helper()
	if _, err := svc.UpsertConfig(ctx, org, "microsoft", idpsyncspec.ConfigInput{
		ClientID: "cid", ClientSecret: "sec", TenantID: "tid", Enabled: true,
	}); err != nil {
		t.Fatalf("upsert config: %v", err)
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func hasCode(err error, status int, code string) bool {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == status && ae.Code == code
}
