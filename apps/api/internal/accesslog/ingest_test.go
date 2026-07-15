package accesslog

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

type failingStream struct{ calls int }

func (f *failingStream) Append(Event) error { f.calls++; return errors.New("disk full") }

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
	q, pool, org := ingestPool(t)
	ctx := context.Background()
	node := uuid.New()
	rule := uuid.New()
	res := uuid.New()
	jw, err := NewJSONLWriter(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	ing := NewIngester(pool, jw, stubGrants{dstResource: &res, known: rule}, nil, func() time.Time { return time.Unix(1000, 0).UTC() })

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
			// SIEM-sufficient: src + count + window [start=OccurredAt, end=WindowEnd].
			if e.SrcIP != "10.99.0.66" || e.DenyCount != 20 || e.WindowEnd == nil || e.OccurredAt.IsZero() {
				t.Fatalf("deny_aggregate must carry src + count 20 + window bounds: %+v", e)
			}
			if e.WindowEnd.Before(e.OccurredAt) {
				t.Fatalf("window end must be >= start: %+v", e)
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
	q, pool, org := ingestPool(t)
	ctx := context.Background()
	ing := NewIngester(pool, nil, stubGrants{}, nil, nil)
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

// A JSONL write failure is BEST-EFFORT: the batch still succeeds (PG has the events), the
// failure is LEGIBLE on Health, and the missing lines are a durable seq hole in the stream.
func TestIngestJSONLFailureIsBestEffortAndLegible(t *testing.T) {
	q, pool, org := ingestPool(t)
	ctx := context.Background()
	h := NewHealth()
	ing := NewIngester(pool, &failingStream{}, stubGrants{}, h, func() time.Time { return time.Unix(2000, 0).UTC() })
	batch := []WireEvent{{OccurredAt: time.Now().UTC(), Verdict: "allow", SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp"}}
	if err := ing.IngestBatch(ctx, org, uuid.New(), batch, 0); err != nil {
		t.Fatalf("a JSONL failure must NOT fail the batch (best-effort): %v", err)
	}
	rows, _ := q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 10})
	if len(rows) != 1 {
		t.Fatalf("PG must hold the event despite JSONL failure: got %d", len(rows))
	}
	if snap := h.Snapshot(); !snap.JSONLDegraded || snap.JSONLFailures == 0 {
		t.Fatalf("JSONL failure must be LEGIBLE on Health: %+v", snap)
	}
}

// seq is DB-derived + contiguous across batches — no burn, no false gap.
func TestIngestSeqContiguousAcrossBatches(t *testing.T) {
	q, pool, org := ingestPool(t)
	ctx := context.Background()
	ing := NewIngester(pool, nil, stubGrants{}, nil, nil)
	mk := func(ip string) []WireEvent {
		return []WireEvent{
			{OccurredAt: time.Now().UTC(), Verdict: "allow", SrcIP: ip, DstIP: "10.0.0.1", Protocol: "tcp"},
			{OccurredAt: time.Now().UTC(), Verdict: "allow", SrcIP: ip, DstIP: "10.0.0.2", Protocol: "tcp"},
		}
	}
	if err := ing.IngestBatch(ctx, org, uuid.New(), mk("10.99.0.1"), 0); err != nil {
		t.Fatal(err)
	}
	if err := ing.IngestBatch(ctx, org, uuid.New(), mk("10.99.0.2"), 0); err != nil {
		t.Fatal(err)
	}
	rows, _ := q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 10})
	got := map[int64]bool{}
	for _, r := range rows {
		got[r.Seq] = true
	}
	for s := int64(1); s <= 4; s++ {
		if !got[s] {
			t.Fatalf("seq must be contiguous 1..4 across batches (DB-derived, no burn), missing %d: %v", s, got)
		}
	}
}

// 6/n seam: a `terminated` wire event (a flow torn down by a rule-revoke) ingests as
// DecisionTerminated, enriched on the SAME rule_id as the revoked grant (the carried
// binding), and is NEVER aggregated.
func TestIngestTerminatedKeyedOnRuleID(t *testing.T) {
	q, pool, org := ingestPool(t)
	ctx := context.Background()
	rule := uuid.New()
	res := uuid.New()
	ing := NewIngester(pool, nil, stubGrants{dstResource: &res, known: rule}, nil, nil)
	batch := []WireEvent{
		{OccurredAt: time.Now().UTC(), Verdict: "terminated", RuleID: rule.String(), SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp", DstPort: 5432},
		{OccurredAt: time.Now().UTC(), Verdict: "terminated", RuleID: rule.String(), SrcIP: "10.99.0.10", DstIP: "10.0.5.6", Protocol: "tcp", DstPort: 5433},
	}
	if err := ing.IngestBatch(ctx, org, uuid.New(), batch, 0); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	rows, err := q.ListAccessDenies(ctx, sqlc.ListAccessDeniesParams{OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// Two distinct terminations (never collapsed), each keyed on the revoked grant.
	if len(rows) != 2 {
		t.Fatalf("terminated events must NOT aggregate: got %d, want 2", len(rows))
	}
	for _, r := range rows {
		e := FromRow(r)
		if e.Decision != DecisionTerminated {
			t.Fatalf("want decision=terminated, got %q", e.Decision)
		}
		if e.RuleID == nil || *e.RuleID != rule {
			t.Fatalf("terminated must carry the revoked grant's rule_id: %+v", e)
		}
		if e.DstResourceID == nil || *e.DstResourceID != res {
			t.Fatalf("terminated must be grant-enriched (dst resource): %+v", e)
		}
	}
}
