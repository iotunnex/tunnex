// Command preflight answers one question before an operator commits an upgrade: would a rolling upgrade of
// this deployment succeed? (S11 D1.)
//
// IT CHANGES NOTHING. Every check is a read. The failure direction is deliberate: preflight REFUSES LOUDLY
// and exits non-zero rather than warning and proceeding, because a warning printed into a deploy log is a
// warning nobody reads — and the failures it looks for are exactly the ones that strand a fleet halfway
// through a roll.
//
// It is a separate binary from the server on purpose: an operator runs it BEFORE swapping images, so it must
// not require the new control plane to already be running.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/config"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// check is one preflight verdict. A check that cannot be evaluated is NOT a pass — it is reported as unknown
// and refuses, because "I could not tell" and "it is fine" are different answers and only one may proceed.
type check struct {
	name   string
	ok     bool
	detail string
}

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		refuse([]check{{"database reachable", false,
			"cannot connect: " + err.Error() + " — an upgrade cannot be assessed, let alone performed"}})
	}
	defer pool.Close()

	checks := []check{
		databaseReachable(ctx, pool),
		migrationsClean(ctx, pool),
		agentCompatWindow(ctx, pool),
		backupExists(),
	}

	failed := 0
	fmt.Println("Tunnex upgrade preflight")
	fmt.Printf("  control plane protocol version: %d (supports agents at v%d and v%d)\n\n",
		policyspec.ProtocolVersion, policyspec.ProtocolVersion-1, policyspec.ProtocolVersion)
	for _, c := range checks {
		mark := "ok  "
		if !c.ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("  [%s] %-28s %s\n", mark, c.name, c.detail)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\nREFUSING: %d check(s) failed. Nothing was changed.\n"+
			"Resolve the above before rolling. See docs/upgrade.md.\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nAll checks passed. A rolling upgrade is safe to proceed:\n" +
		"  1. migrate the database   2. roll the control-plane replicas   3. agents reconcile on their own")
}

func databaseReachable(ctx context.Context, pool *pgxpool.Pool) check {
	if err := pool.Ping(ctx); err != nil {
		return check{"database reachable", false, err.Error()}
	}
	return check{"database reachable", true, "connected"}
}

// migrationsClean refuses on a DIRTY migration state. A dirty schema means a previous migration failed
// part-way, so the database is in neither the old shape nor the new one — rolling onto that is how a
// half-migrated deployment becomes an unrecoverable one.
func migrationsClean(ctx context.Context, pool *pgxpool.Pool) check {
	var version int64
	var dirty bool
	err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty)
	if err != nil {
		return check{"migration state", false, "cannot read schema_migrations: " + err.Error()}
	}
	if dirty {
		return check{"migration state", false, fmt.Sprintf(
			"DIRTY at version %d — a previous migration failed part-way. Resolve it before upgrading; "+
				"rolling onto a half-migrated schema is how a recoverable state becomes an unrecoverable one",
			version)}
	}
	return check{"migration state", true, fmt.Sprintf("clean at version %d", version)}
}

// agentCompatWindow is the check that gives preflight its point.
//
// The CP supports agents at N and N-1 (policyspec.SupportedWindow). An agent reports the highest artifact
// version it can apply; one below the window will REFUSE its artifact after the upgrade — fail-closed, so it
// keeps enforcing its last-known policy rather than mis-enforcing, but it stops receiving updates until it is
// upgraded. That is worth knowing BEFORE the roll, not after, and the remedy (upgrade those agents first) is
// only available beforehand.
//
// An agent reporting version 0 has never reported one (a pre-CW agent). That is UNKNOWN, not fine: it is
// named as such rather than counted as a pass.
func agentCompatWindow(ctx context.Context, pool *pgxpool.Pool) check {
	oldest := policyspec.ProtocolVersion - policyspec.SupportedWindow + 1

	// The agent's reported ceiling lives in nodes.capabilities (jsonb), written by ReportWGInfo — read it
	// from there rather than from a status table, because that is where it actually is.
	rows, err := pool.Query(ctx, `
		SELECT name, COALESCE((capabilities->>'max_policy_version')::int, 0)
		  FROM nodes
		 WHERE revoked_at IS NULL`)
	if err != nil {
		// The column/table may not exist on an older schema — report honestly rather than guessing.
		return check{"agent version window", false,
			"cannot read agent versions (" + err.Error() + ") — cannot confirm the fleet is in the window"}
	}
	defer rows.Close()

	var tooOld, unknown []string
	total := 0
	for rows.Next() {
		var name string
		var v int
		if err := rows.Scan(&name, &v); err != nil {
			return check{"agent version window", false, "scan: " + err.Error()}
		}
		total++
		switch {
		case v == 0:
			unknown = append(unknown, name)
		case v < oldest:
			tooOld = append(tooOld, fmt.Sprintf("%s (v%d)", name, v))
		}
	}
	if err := rows.Err(); err != nil {
		return check{"agent version window", false, err.Error()}
	}

	switch {
	case len(tooOld) > 0:
		return check{"agent version window", false, fmt.Sprintf(
			"%d of %d gateway(s) below the supported window (oldest supported: v%d): %v — after the upgrade "+
				"they REFUSE their artifact and stop receiving policy updates (they keep enforcing the last "+
				"one). Upgrade these agents FIRST, then the control plane",
			len(tooOld), total, oldest, sample(tooOld, 8))}
	case len(unknown) > 0:
		return check{"agent version window", false, fmt.Sprintf(
			"%d of %d gateway(s) have never reported a supported version: %v — this is UNKNOWN, not fine. "+
				"Confirm they are reachable and reporting before rolling",
			len(unknown), total, sample(unknown, 8))}
	case total == 0:
		return check{"agent version window", true, "no gateways enrolled"}
	}
	return check{"agent version window", true,
		fmt.Sprintf("all %d gateway(s) at v%d or newer", total, oldest)}
}

// backupExists is a REMINDER with teeth, and it is why D1 and D2 were ruled in this order.
//
// The upgrade path is FORWARD-ONLY: there is no downgrade. The rollback is restore-from-backup, which needs
// the dump AND the separately-custodied master key. preflight cannot verify an operator's offsite backup, so
// it asks — and refuses without an explicit acknowledgement rather than printing advice into a log.
func backupExists() check {
	if os.Getenv("TUNNEX_PREFLIGHT_BACKUP_CONFIRMED") == "yes" {
		return check{"rollback plan", true, "operator confirmed a current backup + master key"}
	}
	return check{"rollback plan", false,
		"unconfirmed. This upgrade is FORWARD-ONLY — there is no downgrade, and the rollback is " +
			"restore-from-backup, which requires the master key you custody SEPARATELY. Take a backup " +
			"(docs/backup-restore.md), verify it with `backupctl verify`, then re-run with " +
			"TUNNEX_PREFLIGHT_BACKUP_CONFIRMED=yes"}
}

// sample renders at most n names plus a count — an operator facing 186 gateway names learns nothing from the
// 187th. The COUNT is the actionable number; the names are a starting point for investigation.
func sample(names []string, n int) string {
	if len(names) <= n {
		return fmt.Sprint(names)
	}
	return fmt.Sprintf("%v and %d more", names[:n], len(names)-n)
}

func refuse(checks []check) {
	for _, c := range checks {
		fmt.Fprintf(os.Stderr, "  [FAIL] %s: %s\n", c.name, c.detail)
	}
	fmt.Fprintln(os.Stderr, "\nREFUSING. Nothing was changed.")
	os.Exit(1)
}
