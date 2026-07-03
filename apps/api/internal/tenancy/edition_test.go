package tenancy

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise"
)

// TestOrgLimitByEdition proves the enterprise boundary behaves differently per
// build: the open build (no tag) caps org creation at one with a typed
// org_limit_reached error; the enterprise build (-tags enterprise) does not.
//
// It runs inside a transaction that zeroes live orgs and is always rolled back,
// so it is deterministic and never touches real data. Run in both modes via
// `make test-editions`.
func TestOrgLimitByEdition(t *testing.T) {
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
	defer tx.Rollback(ctx) //nolint:errcheck // rollback is the point

	// Start from a clean slate within the (rolled-back) transaction.
	if _, err := tx.Exec(ctx, "UPDATE organizations SET deleted_at = now() WHERE deleted_at IS NULL"); err != nil {
		t.Fatalf("reset orgs: %v", err)
	}

	creator := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)",
		creator, "creator-"+creator.String()+"@t", "Creator"); err != nil {
		t.Fatalf("create creator: %v", err)
	}

	svc := &Service{q: sqlc.New(tx)}

	if _, err := svc.CreateOrganization(ctx, creator, "Edition A", "edition-test-a"); err != nil {
		t.Fatalf("first org must always succeed: %v", err)
	}

	_, err = svc.CreateOrganization(ctx, creator, "Edition B", "edition-test-b")

	if enterprise.Unlimited {
		if err != nil {
			t.Fatalf("enterprise build: second org should succeed, got %v", err)
		}
		t.Logf("enterprise edition: multiple orgs allowed ✓")
		return
	}

	// Open build: the second org must be rejected with the typed envelope error.
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("open build: expected *apierr.Error, got %v", err)
	}
	if apiErr.Code != "org_limit_reached" || apiErr.Status != 403 {
		t.Fatalf("open build: expected 403 org_limit_reached, got %d %s", apiErr.Status, apiErr.Code)
	}
	t.Logf("open edition: second org rejected with %s ✓", apiErr.Code)
}
