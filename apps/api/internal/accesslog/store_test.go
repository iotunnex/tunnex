package accesslog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

var maxUUID = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")

func storeQ(t *testing.T) (*sqlc.Queries, *pgxpool.Pool) {
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
	return sqlc.New(pool), pool
}

func TestStoreInsertListSweep(t *testing.T) {
	q, pool := storeQ(t)
	ctx := context.Background()
	org := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug) VALUES ($1,'ae',$2)`, org, "ae-"+org.String()[:8]); err != nil {
		t.Fatalf("org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })

	rule := uuid.New()
	// A fixed OLD created_at so the CROSS-ORG age sweep below can target ONLY this test's
	// rows (packages test-run in parallel; other tests insert access_events at ~now, which a
	// mid-cutoff excludes). created_at is the sweep/keyset clock — not the agent OccurredAt.
	oldTime := time.Unix(1_000_000, 0).UTC() // ~1970
	mk := func(seq int64, d Decision) Event {
		return Event{ID: uuid.New(), CreatedAt: oldTime, Seq: seq, OrgID: org, OccurredAt: time.Now().UTC(), Decision: d,
			RuleID: &rule, SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp", DstPort: 5432}
	}
	// Insert 5 events; a duplicate (org,seq) is idempotent (0 rows).
	for i := int64(1); i <= 5; i++ {
		if n, err := q.InsertAccessEvent(ctx, InsertParams(mk(i, DecisionDeny))); err != nil || n != 1 {
			t.Fatalf("insert %d: n=%d err=%v", i, n, err)
		}
	}
	if n, _ := q.InsertAccessEvent(ctx, InsertParams(mk(5, DecisionDeny))); n != 0 {
		t.Fatal("duplicate (org,seq) must be idempotent (0 rows)")
	}

	// Keyset first page (far-future cursor) → newest-first, FromRow round-trips.
	rows, err := q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{
		OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 10,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	if e := FromRow(rows[0]); e.RuleID == nil || *e.RuleID != rule || e.DstPort != 5432 || e.Decision != DecisionDeny {
		t.Fatalf("FromRow round-trip wrong: %+v", e)
	}

	// MaxSeq resume high-water.
	if hi, _ := q.MaxAccessEventSeqForOrg(ctx, org); hi != 5 {
		t.Fatalf("max seq = %d, want 5", hi)
	}

	// Cap sweep: keep newest 2 → deletes 3.
	if n, err := q.SweepAccessEventsOverCap(ctx, sqlc.SweepAccessEventsOverCapParams{OrgID: org, KeepNewest: 2}); err != nil || n != 3 {
		t.Fatalf("cap sweep deleted %d (err %v), want 3", n, err)
	}
	// Age sweep (CROSS-ORG): a cutoff just after this test's OLD rows deletes only them (the
	// remaining 2), never concurrent packages' ~now rows.
	if n, err := q.SweepAccessEventsByAge(ctx, oldTime.Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("age sweep deleted %d (err %v), want 2", n, err)
	}
}
