package accesslog

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// Wire verdict values (the agent's flowlog.Verdict, as a string on the ingest wire).
const (
	wireAllow      = "allow"
	wireDeny       = "deny"
	wireTerminated = "terminated" // 6/n seam: a flow torn down by a policy-rule revoke
)

// DenyAggregateThreshold bounds the port-scan log-DoS (D1): within ONE report batch (the
// window = the agent's drain/report interval), if a single src_ip produces MORE than this
// many denies, they collapse into ONE deny_aggregate event carrying the count — the signal
// (who scanned, how much) survives, the volume does not. Allows are NEVER aggregated
// (legitimate volume); a src at or under the threshold keeps its individual denies.
const DenyAggregateThreshold = 5

// WireEvent is the agent→CP flow-event shape (mirrors node flowlog.Event). Defined here so
// the api module never imports the node module. Verdict is "allow" | "deny".
type WireEvent struct {
	OccurredAt time.Time `json:"occurred_at"`
	Verdict    string    `json:"verdict"`
	RuleID     string    `json:"rule_id,omitempty"`
	PolicyHash string    `json:"policy_hash"`
	SrcIP      string    `json:"src_ip"`
	DstIP      string    `json:"dst_ip"`
	Protocol   string    `json:"protocol"`
	DstPort    int       `json:"dst_port,omitempty"`
}

// GrantResolver maps a kernel-stamped rule_id to the grant's destination, captured AT EVENT
// TIME so it survives a later rule delete. This is the ONLY attribution enrichment — there
// is NO src_ip→device lookup anywhere (that would be a racy IP-map reconstruction; the
// agent-stamped rule_id → grant is authoritative). ok=false = the rule was already deleted.
type GrantResolver interface {
	ResolveGrant(ctx context.Context, orgID, ruleID uuid.UUID) (dstResource, dstGroup *uuid.UUID, ok bool)
}

// SQLGrantResolver is the production GrantResolver: rule_id → the grant's destination via
// the DB, org-scoped. A deleted/unknown rule resolves to (nil,nil,false) — the event keeps
// its raw rule_id, the dst is simply not captured. This is the ONLY enrichment lookup; it
// takes a rule_id, never a src_ip.
type SQLGrantResolver struct{ Q *sqlc.Queries }

// ResolveGrant implements GrantResolver.
func (r SQLGrantResolver) ResolveGrant(ctx context.Context, orgID, ruleID uuid.UUID) (*uuid.UUID, *uuid.UUID, bool) {
	row, err := r.Q.GetPolicyRuleForOrg(ctx, sqlc.GetPolicyRuleForOrgParams{ID: ruleID, OrgID: orgID})
	if err != nil {
		return nil, nil, false
	}
	return uuidPtr(row.DstResourceID), uuidPtr(row.DstGroupID), true
}

// Ingester turns an agent report batch into persisted access events: enrich (grant-only),
// aggregate per-source denies, mark gaps, assign the per-org monotonic seq, and write BOTH
// stores (PG hot-window + JSONL source-of-truth).
//
// CONCURRENCY (review #1/#2, composed): the net/http agent channel calls IngestBatch from a
// PER-REQUEST goroutine, so the SAME Ingester + shared JSONLWriter are hit concurrently. A
// single process-level `mu` encloses the WHOLE batch — seq reservation, the PG inserts, AND
// the JSONL append+flush are ONE critical section. So seq order == PG commit order == JSONL
// append order == manifest order; there is no window where the not-concurrency-safe bufio
// writer is raced or where JSONL reorders under a correct seq. Nested INSIDE mu, the per-org
// DB counter (BumpOrgFlowSeq) still row-locks the org row — redundant under mu on a single CP
// but the correct primitive if the control plane ever runs multi-instance. Tradeoff: ingest
// is serialized process-wide; acceptable — batches are drain-interval driven, and a whole
// source-of-truth's integrity outranks ingest parallelism.
//
// seq integrity: the seq is reserved from the per-org counter (organizations.flow_seq) INSIDE
// the per-batch tx, so a rolled-back batch releases its reserved range (no burn, no false
// gap), and the counter is never swept so seq is monotonic + sweep-proof. JSONL is BEST-EFFORT
// (a write failure does NOT fail the batch — PG stays the queryable store; the failure
// surfaces on Health and the per-line seq leaves a durable, detectable hole in the stream).
// streamWriter is the JSONL source-of-truth sink (JSONLWriter in production; a fake in
// tests, to exercise the best-effort write-failure path). Flush makes the batch's appended
// lines DURABLE (bufio -> OS -> fsync); the ingest calls it once per batch so a graceful
// shutdown or a reader never sees committed-in-PG-but-buffered-in-memory events.
type streamWriter interface {
	Append(Event) error
	Flush() error
}

type Ingester struct {
	// mu serializes the whole IngestBatch — the seq/PG/JSONL critical section (see the type
	// doc). Guards both the shared JSONLWriter (not concurrency-safe) and the seq order.
	mu     sync.Mutex
	pool   *pgxpool.Pool
	grants GrantResolver
	jsonl  streamWriter
	health *Health
	now    func() time.Time
}

// NewIngester wires the pool (for the per-batch tx), the JSONL stream, the grant resolver,
// and the health surface. now defaults to time.Now; jsonl/health may be nil.
func NewIngester(pool *pgxpool.Pool, jsonl streamWriter, grants GrantResolver, health *Health, now func() time.Time) *Ingester {
	if now == nil {
		now = time.Now
	}
	return &Ingester{pool: pool, jsonl: jsonl, grants: grants, health: health, now: now}
}

// IngestBatch persists one agent report: the observed events (enriched + aggregated) plus,
// if the agent dropped any, a single legible gap marker carrying the dropped count.
func (i *Ingester) IngestBatch(ctx context.Context, orgID, nodeID uuid.UUID, wire []WireEvent, dropped int64) error {
	events := i.aggregate(ctx, orgID, nodeID, wire)
	if dropped > 0 {
		// The gap marker sits in-stream where the loss occurred (an auditor sees it).
		events = append(events, Event{
			OrgID: orgID, NodeID: &nodeID, OccurredAt: i.now().UTC(),
			Decision: DecisionGap, DenyCount: int(dropped),
		})
	}
	if len(events) == 0 {
		return nil
	}
	// One ingest clock for the whole batch — the keyset created_at (PG + JSONL agree).
	ingestAt := i.now().UTC()
	for idx := range events {
		events[idx].CreatedAt = ingestAt
		// uuid v7 (time-ordered): within a batch all events share created_at, so the
		// (created_at DESC, id DESC) keyset falls to id — v7 keeps that in occurrence order
		// (review #5). uuid.NewV7 only errors on a crypto/rand failure; surface it.
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		events[idx].ID = id
	}

	// The whole batch is ONE critical section (seq reservation + PG inserts + JSONL): serializes
	// concurrent same-org AND cross-org ingest so seq never collides and the shared JSONLWriter
	// is never raced (review #1/#2). See the Ingester type doc for how the process mutex and the
	// per-org DB row lock compose.
	i.mu.Lock()
	defer i.mu.Unlock()

	// PG: all inserts in ONE tx. seq is reserved from the per-org sweep-proof counter, whose
	// UPDATE row-locks the org row (serializes same-org even across instances). On failure
	// nothing commits and the counter bump rolls back → no burn → the agent's next report gaps.
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	q := sqlc.New(tx)
	top, err := q.BumpOrgFlowSeq(ctx, sqlc.BumpOrgFlowSeqParams{N: int64(len(events)), OrgID: orgID})
	if err != nil {
		return err
	}
	base := top - int64(len(events)) // the reserved range is base+1 .. top
	for idx := range events {
		events[idx].Seq = base + int64(idx) + 1
		if err := q.InsertAccessEvent(ctx, InsertParams(events[idx])); err != nil {
			return err // a (org_id, seq) collision here is IMPOSSIBLE under the counter -> fail LOUD, never silent
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// JSONL (source-of-truth stream) — best-effort, AFTER commit so it only ever reflects
	// committed events. A failure marks Health (legible divergence) + leaves a seq hole;
	// it does NOT fail the batch (else a retry would duplicate committed PG rows).
	if i.jsonl != nil {
		wrote := true
		for idx := range events {
			if err := i.jsonl.Append(events[idx]); err != nil {
				wrote = false
				i.health.jsonlFailed(i.now())
			}
		}
		// Flush the batch to disk (bufio -> OS -> fsync). Append only fills a buffer; without
		// this the lines are not durable and a reader/export sees nothing until a far-off
		// rotation (box-walk finding). A flush failure is a real durability failure, so it too
		// marks Health (not just per-line write errors) — Health must not read green while the
		// source-of-truth is diverging from PG.
		if err := i.jsonl.Flush(); err != nil {
			wrote = false
			i.health.jsonlFailed(i.now())
		}
		if wrote {
			i.health.jsonlRecovered()
		}
	}
	return nil
}

// aggregate enriches each wire event (grant-only) and collapses per-source deny floods.
func (i *Ingester) aggregate(ctx context.Context, orgID, nodeID uuid.UUID, wire []WireEvent) []Event {
	out := make([]Event, 0, len(wire))
	denyBySrc := map[string][]WireEvent{}
	for _, w := range wire {
		switch w.Verdict {
		case wireDeny:
			denyBySrc[w.SrcIP] = append(denyBySrc[w.SrcIP], w)
		case wireTerminated:
			// A flow torn down by a rule-revoke — enriched on the REVOKED grant's rule_id (the
			// carried binding), NEVER aggregated (each termination is a distinct event).
			out = append(out, i.enrich(ctx, orgID, nodeID, w, DecisionTerminated))
		default: // allow
			out = append(out, i.enrich(ctx, orgID, nodeID, w, DecisionAllow))
		}
	}
	// Deterministic order: aggregate/emit denies by src.
	srcs := make([]string, 0, len(denyBySrc))
	for s := range denyBySrc {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	for _, s := range srcs {
		ds := denyBySrc[s]
		if len(ds) > DenyAggregateThreshold {
			// Collapse: one deny_aggregate carrying the src (via enrich), the count, and the
			// full window BOUNDS — OccurredAt = first deny seen, WindowEnd = last — so a SIEM
			// has src + count + [start,end] without the individual lines.
			last := ds[len(ds)-1]
			agg := i.enrich(ctx, orgID, nodeID, last, DecisionDenyAggregate)
			agg.OccurredAt = ds[0].OccurredAt.UTC() // window START
			agg.DenyCount = len(ds)
			end := last.OccurredAt.UTC() // window END
			agg.WindowEnd = &end
			out = append(out, agg)
			continue
		}
		for _, w := range ds {
			out = append(out, i.enrich(ctx, orgID, nodeID, w, DecisionDeny))
		}
	}
	return out
}

// enrich builds an Event from a wire event. Attribution is GRANT-ONLY: rule_id → the
// grant's destination (resource/group). NO src_ip→device/user lookup (device/user stay nil
// — a racy IP map is forbidden). src_ip is kept as the raw observed fact.
func (i *Ingester) enrich(ctx context.Context, orgID, nodeID uuid.UUID, w WireEvent, d Decision) Event {
	e := Event{
		OrgID: orgID, NodeID: &nodeID, OccurredAt: w.OccurredAt.UTC(), Decision: d,
		SrcIP: w.SrcIP, DstIP: w.DstIP, Protocol: w.Protocol, DstPort: w.DstPort,
	}
	if rid, err := uuid.Parse(w.RuleID); err == nil {
		e.RuleID = &rid
		if res, grp, ok := i.grants.ResolveGrant(ctx, orgID, rid); ok {
			e.DstResourceID, e.DstGroupID = res, grp // grant dst captured at event time
		}
	}
	return e
}

