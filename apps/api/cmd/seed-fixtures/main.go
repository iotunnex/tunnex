// Command seed-fixtures loads the DEMO FIXTURE SET on top of `seed` (S14.5).
//
// ⛔ WHY IT IS A SEPARATE COMMAND rather than more rows inside `seed`: `seed` establishes the org, its users
// and the auth surfaces every environment needs, including CI. These fixtures are a REVIEW AID — a populated
// network so the redesigned screens have a designed picture instead of a wall of empty states. Mixing them
// would put demo topology into every CI database and make the base seed's contract fuzzier.
//
// Same shape as `seed-enterprise`: layered, idempotent, and refusing to run against real data.
package main

import (
	"context"
	_ "embed"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/seeddata"
)

//go:embed fixtures.sql
var fixturesSQL string

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("seed_fixtures_failed", slog.String("error", "DATABASE_URL is not set"))
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Error("seed_fixtures_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	// THE SAME GUARD AS `seed`, and for the same reason: fixtures are demo data, and demo data must never
	// land beside somebody's production org. The demo org itself is excluded from the count, so a reseed of
	// a demo-only database is always allowed.
	var realOrgs int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM organizations WHERE id <> $1 AND deleted_at IS NULL`,
		seeddata.DemoOrgID).Scan(&realOrgs); err != nil {
		logger.Error("seed_fixtures_check_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if realOrgs > 0 && os.Getenv("TUNNEX_SEED_FORCE") != "true" {
		logger.Error("seed_fixtures_refused",
			slog.Int64("real_orgs", realOrgs),
			slog.String("hint", "database has real data; set TUNNEX_SEED_FORCE=true to override"),
		)
		os.Exit(1)
	}

	// The demo org must already exist — these fixtures hang off it. Failing loudly here beats a confusing
	// foreign-key error thirty statements into the transaction.
	var orgExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1 AND deleted_at IS NULL)`,
		seeddata.DemoOrgID).Scan(&orgExists); err != nil {
		logger.Error("seed_fixtures_check_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if !orgExists {
		logger.Error("seed_fixtures_refused",
			slog.String("error", "the demo org does not exist"),
			slog.String("hint", "run `make seed` first — these fixtures layer on top of it"),
		)
		os.Exit(1)
	}

	if _, err := pool.Exec(ctx, fixturesSQL); err != nil {
		logger.Error("seed_fixtures_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// ⛔ COUNTED, NOT ASSERTED — and the difference already misled a review.
	//
	// This line used to read "5 gateways, 4 sites, 6 subnets, 5 devices, 12 audit entries": the rows THIS FILE
	// INSERTS. The screen renders what EXISTS, which is those rows PLUS whatever `make seed` already wrote —
	// live totals were 6 gateways, 6 sites, 8 devices, 35 audit entries. The founder reviewed Gateways showing
	// "1/6" against a seeder that had just claimed 5.
	//
	// A fixture that reports its own INTENT rather than the resulting STATE is a census of the wrong thing.
	// These numbers now come from the database after the write, so the line cannot drift from what is there.
	var gateways, sites, subnets, devices, audits, clusters, services, rules, groups, members, resources, accessEvents, cliCreds, machineCreds int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM nodes               WHERE org_id = $1),
		       (SELECT count(*) FROM sites               WHERE org_id = $1),
		       (SELECT count(*) FROM site_subnets ss     WHERE EXISTS (SELECT 1 FROM sites s WHERE s.id = ss.site_id AND s.org_id = $1)),
		       (SELECT count(*) FROM devices             WHERE org_id = $1),
		       (SELECT count(*) FROM audit_logs          WHERE org_id = $1),
		       (SELECT count(*) FROM k8s_clusters        WHERE org_id = $1),
		       (SELECT count(*) FROM k8s_services        WHERE org_id = $1),
		       (SELECT count(*) FROM policy_rules        WHERE org_id = $1),
		       (SELECT count(*) FROM user_groups         WHERE org_id = $1),
		       (SELECT count(*) FROM group_members       WHERE org_id = $1),
		       (SELECT count(*) FROM resources           WHERE org_id = $1),
		       (SELECT count(*) FROM access_events       WHERE org_id = $1),
		       (SELECT count(*) FROM cli_credentials c   WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = c.user_id AND m.org_id = $1)),
		       (SELECT count(*) FROM machine_credentials WHERE org_id = $1)`,
		seeddata.DemoOrgID,
	).Scan(&gateways, &sites, &subnets, &devices, &audits, &clusters, &services, &rules, &groups, &members, &resources, &accessEvents, &cliCreds, &machineCreds); err != nil {
		logger.Error("seed_fixtures_count_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("seed_fixtures_ok",
		slog.String("org", seeddata.DemoOrgID),
		slog.Int("gateways", gateways), slog.Int("sites", sites), slog.Int("subnets", subnets),
		slog.Int("devices", devices), slog.Int("audit_entries", audits),
		slog.Int("k8s_clusters", clusters), slog.Int("k8s_services", services),
		slog.Int("policy_rules", rules), slog.Int("user_groups", groups), slog.Int("group_members", members),
		slog.Int("resources", resources), slog.Int("access_events", accessEvents),
		slog.Int("cli_credentials", cliCreds), slog.Int("machine_credentials", machineCreds),
		slog.String("note", "totals as they now EXIST (fixture + make seed), counted after the write; health kinds are DERIVED, not seeded"),
	)
}
