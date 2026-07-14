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

type stubGrants struct {
	dstResource *uuid.UUID
	known       uuid.UUID
}

func (s stubGrants) ResolveGrant(_ context.Context, _ uuid.UUID, ruleID uuid.UUID) (*uuid.UUID, *uuid.UUID, bool) {
	if ruleID == s.known {
		return s.dstResource, nil, true
	}
	return nil, nil, false // deleted / unknown rule
}

func ingestPool(t *testing.T) (*sqlc.Queries, *pgxpool.Pool, uuid.UUID) {
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
	org := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO organizations (id,name,slug) VALUES ($1,'ig',$2)`, org, "ig-"+org.String()[:8]); err != nil {
		t.Fatalf("org: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, org) })
	return sqlc.New(pool), pool, org
}

func TestIngestEnrichAggregateGapSeq(t *testing.T) {
	q, _, org := ingestPool(t)
	ctx := context.Background()
	node := uuid.New()
	rule := uuid.New()
	res := uuid.New()
	jw, err := NewJSONLWriter(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	ing := NewIngester(q, jw, stubGrants{dstResource: &res, known: rule}, func() time.Time { return time.Unix(1000, 0).UTC() })

	now := time.Now().UTC()
	batch := []WireEvent{
		{OccurredAt: now, Verdict: "allow", RuleID: rule.String(), SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp", DstPort: 5432},
	}
	// A port scan: 20 denies from one src (> threshold 5) → must collapse to ONE aggregate.
	for p := 0; p < 20; p++ {
		batch = append(batch, WireEvent{OccurredAt: now, Verdict: "deny", SrcIP: "10.99.0.66", DstIP: "10.0.5.5", Protocol: "tcp", DstPort: p + 1})
	}
	// Report also dropped 7 events → a gap marker.
	if err := ing.IngestBatch(ctx, org, node, batch, 7); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	rows, err := q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Expect exactly 3 rows: 1 allow, 1 deny_aggregate, 1 gap (the 20 denies collapsed).
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (allow + deny_aggregate + gap), got %d", len(rows))
	}
	var sawAllow, sawAgg, sawGap bool
	seqs := map[int64]bool{}
	for _, r := range rows {
		seqs[r.Seq] = true
		e := FromRow(r)
		switch e.Decision {
		case DecisionAllow:
			sawAllow = true
			if e.RuleID == nil || *e.RuleID != rule || e.DstResourceID == nil || *e.DstResourceID != res {
				t.Fatalf("allow must be grant-enriched (rule + dst resource): %+v", e)
			}
			if e.SrcDeviceID != nil || e.SrcUserID != nil {
				t.Fatalf("device/user must be NIL (no IP-map attribution): %+v", e)
			}
		case DecisionDenyAggregate:
			sawAgg = true
			if e.DenyCount != 20 || e.WindowEnd == nil {
				t.Fatalf("deny_aggregate must carry count 20 + window end: %+v", e)
			}
		case DecisionGap:
			sawGap = true
			if e.DenyCount != 7 {
				t.Fatalf("gap must carry the dropped count 7: %+v", e)
			}
		}
	}
	if !sawAllow || !sawAgg || !sawGap {
		t.Fatalf("missing an event kind: allow=%v agg=%v gap=%v", sawAllow, sawAgg, sawGap)
	}
	// Seqs are the monotonic 1..3 (per-org), no rewind.
	if !seqs[1] || !seqs[2] || !seqs[3] {
		t.Fatalf("per-org seq must be monotonic 1..3, got %v", seqs)
	}
}

// A deny burst AT the threshold stays individual (not collapsed) — aggregation only fires
// past the bound.
func TestIngestDenyUnderThresholdNotAggregated(t *testing.T) {
	q, _, org := ingestPool(t)
	ctx := context.Background()
	ing := NewIngester(q, nil, stubGrants{}, nil)
	batch := []WireEvent{}
	for p := 0; p < DenyAggregateThreshold; p++ { // exactly threshold, not over
		batch = append(batch, WireEvent{OccurredAt: time.Now().UTC(), Verdict: "deny", SrcIP: "10.99.0.7", DstIP: "10.0.0.1", Protocol: "tcp", DstPort: p + 1})
	}
	if err := ing.IngestBatch(ctx, org, uuid.New(), batch, 0); err != nil {
		t.Fatal(err)
	}
	rows, _ := q.ListAccessDenies(ctx, sqlc.ListAccessDeniesParams{OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 100})
	if len(rows) != DenyAggregateThreshold {
		t.Fatalf("at-threshold denies must stay individual: got %d, want %d", len(rows), DenyAggregateThreshold)
	}
	for _, r := range rows {
		if r.Decision != string(DecisionDeny) {
			t.Fatalf("want plain deny, got %q", r.Decision)
		}
	}
}
