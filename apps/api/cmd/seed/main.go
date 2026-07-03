// Command seed populates the database with a demo organization and owner user.
//
// Contract (S0.6):
//   - Idempotent: running it twice yields the same state (upserts on fixed IDs).
//   - Non-destructive: it refuses to run against a database that already holds
//     real (non-demo) data, unless TUNNEX_SEED_FORCE=true.
//   - Fixed IDs: the demo org/user use the documented constants in
//     internal/seeddata so tests can reference them without querying.
//
// Domain tables arrive in S1.1; until then there is nothing to seed and this
// command no-ops cleanly. The idempotency/safety scaffolding is in place so
// S1.1 only fills in the actual upserts (marked below).
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/seeddata"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("seed_config_error", slog.String("error", "DATABASE_URL is required"))
		os.Exit(1)
	}
	force := os.Getenv("TUNNEX_SEED_FORCE") == "true"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		logger.Error("seed_connect_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	// Pre-S1.1: the organizations table does not exist yet. No-op cleanly.
	hasOrgs, err := tableExists(ctx, pool, "organizations")
	if err != nil {
		logger.Error("seed_check_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if !hasOrgs {
		logger.Info("seed_skipped",
			slog.String("reason", "no seedable tables yet (pre-S1.1)"),
			slog.String("demo_org_id", seeddata.DemoOrgID),
		)
		return
	}

	// Non-destructive guard: refuse if the DB holds real (non-demo) orgs.
	realCount, err := countRealOrgs(ctx, pool)
	if err != nil {
		logger.Error("seed_check_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if realCount > 0 && !force {
		logger.Error("seed_refused",
			slog.Int64("real_orgs", realCount),
			slog.String("hint", "database has real data; set TUNNEX_SEED_FORCE=true to override"),
		)
		os.Exit(1)
	}

	// Idempotent upsert of the demo org + owner + membership (fixed IDs).
	q := sqlc.New(pool)
	orgID := uuid.MustParse(seeddata.DemoOrgID)
	userID := uuid.MustParse(seeddata.DemoOwnerUserID)

	if _, err := q.UpsertOrganization(ctx, sqlc.UpsertOrganizationParams{
		ID: orgID, Name: seeddata.DemoOrgName, Slug: seeddata.DemoOrgSlug,
	}); err != nil {
		logger.Error("seed_org_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if _, err := q.UpsertUser(ctx, sqlc.UpsertUserParams{
		ID: userID, Email: seeddata.DemoOwnerEmail, Name: seeddata.DemoOwnerName,
	}); err != nil {
		logger.Error("seed_user_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if _, err := q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{
		OrgID: orgID, UserID: userID, Role: "owner",
	}); err != nil {
		logger.Error("seed_membership_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("seed_complete",
		slog.String("demo_org_id", seeddata.DemoOrgID),
		slog.String("demo_owner_email", seeddata.DemoOwnerEmail),
	)
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'public' AND table_name = $1
		 )`, name).Scan(&exists)
	return exists, err
}

// countRealOrgs counts LIVE organizations that are not the fixed demo org.
// Soft-deleted orgs are excluded — they are not "real data" for the guard, and
// counting them would wrongly block a reseed after demo data was deleted.
func countRealOrgs(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM organizations WHERE id <> $1 AND deleted_at IS NULL`,
		seeddata.DemoOrgID).Scan(&n)
	return n, err
}
