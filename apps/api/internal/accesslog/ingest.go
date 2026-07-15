package accesslog

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
// aggregate per-source denies, mark gaps, assign the per-org monotonic seq, and persist to the
// PG hot-window.
//
// NOTE (S7.5.1b deferral): the JSONL on-disk source-of-truth + byte-verbatim export were
// DEFERRED to S7.5.1b after the writer failed to converge over six review rounds (see
// docs/S7.5.1-decisions.md). v1 is PG-only; the per-org monotonic seq column REMAINS in PG as
// the follow-up's anchor + the gap detector.
//
// CONCURRENCY (review #1): the net/http agent channel calls IngestBatch from a per-request
// goroutine, so the SAME Ingester is hit concurrently. The per-org DB counter (BumpOrgFlowSeq)
// row-locks the org row, serializing same-org ingest so seq can never collide; a process-level
// `mu` additionally serializes the batch (defensive + keeps a single ordering point). seq is
// reserved from the per-org counter (organizations.flow_seq) INSIDE the per-batch tx, so a
// rolled-back batch releases its reserved range (no burn, no false gap), and the counter is
// never swept so seq is monotonic + sweep-proof.
type Ingester struct {
	mu     sync.Mutex // serializes IngestBatch (seq reservation + inserts) — defensive over the DB row lock
	pool   *pgxpool.Pool
	grants GrantResolver
	health *Health
	now    func() time.Time
}

// NewIngester wires the pool (for the per-batch tx), the grant resolver, and the health
// surface. now defaults to time.Now; health may be nil.
func NewIngester(pool *pgxpool.Pool, grants GrantResolver, health *Health, now func() time.Time) *Ingester {
	if now == nil {
		now = time.Now
	}
	return &Ingester{pool: pool, grants: grants, health: health, now: now}
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
	// One ingest clock for the whole batch — the PG keyset created_at.
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

	// One critical section (seq reservation + PG inserts): serializes concurrent same-org ingest
	// so seq never collides (review #1). The per-org DB counter row-lock is the primary guard;
	// this process mutex is defensive + a single ordering point.
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // org soft-deleted / absent — nothing to ingest; drop the batch (not an error)
	}
	if err != nil {
		return err
	}
	base := top - int64(len(events)) // the reserved range is base+1 .. top
	params := make([]sqlc.InsertAccessEventBatchParams, len(events))
	for idx := range events {
		events[idx].Seq = base + int64(idx) + 1
		params[idx] = sqlc.InsertAccessEventBatchParams(InsertParams(events[idx]))
	}
	// Pipeline the whole batch's inserts in ONE round trip (fold-2 #6) so the process-global
	// ingest mutex is held for far less time. A (org_id, seq) collision is IMPOSSIBLE under the
	// counter, so any error here fails LOUD (never a silent drop).
	var insErr error
	br := q.InsertAccessEventBatch(ctx, params)
	br.Exec(func(_ int, err error) {
		if err != nil && insErr == nil {
			insErr = err
		}
	})
	if err := br.Close(); err != nil {
		return err
	}
	if insErr != nil {
		return insErr
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// (S7.5.1b) the JSONL source-of-truth write happened HERE, after commit. Deferred — v1 is
	// PG-only. The per-line seq already committed above is the follow-up's anchor + gap detector.
	return nil
}

// grantCache memoizes rule_id → grant-dst resolution FOR ONE batch (fold-2 #5): a report
// where many flows share a grant resolved the same rule_id once per event (N+1). Keyed by
// rule_id; each distinct rule_id hits the DB at most once per batch.
type grantCache struct {
	r    GrantResolver
	seen map[uuid.UUID]grantHit
}

type grantHit struct {
	res, grp *uuid.UUID
	ok       bool
}

func (c *grantCache) resolve(ctx context.Context, orgID, ruleID uuid.UUID) (*uuid.UUID, *uuid.UUID, bool) {
	if h, ok := c.seen[ruleID]; ok {
		return h.res, h.grp, h.ok
	}
	res, grp, ok := c.r.ResolveGrant(ctx, orgID, ruleID)
	c.seen[ruleID] = grantHit{res, grp, ok}
	return res, grp, ok
}

// aggregate enriches each wire event (grant-only) and collapses per-source deny floods.
func (i *Ingester) aggregate(ctx context.Context, orgID, nodeID uuid.UUID, wire []WireEvent) []Event {
	out := make([]Event, 0, len(wire))
	gc := &grantCache{r: i.grants, seen: map[uuid.UUID]grantHit{}} // one grant lookup per distinct rule_id per batch
	denyBySrc := map[string][]WireEvent{}
	for _, w := range wire {
		switch w.Verdict {
		case wireDeny:
			denyBySrc[w.SrcIP] = append(denyBySrc[w.SrcIP], w)
		case wireTerminated:
			// A flow torn down by a rule-revoke — enriched on the REVOKED grant's rule_id (the
			// carried binding), NEVER aggregated (each termination is a distinct event).
			out = append(out, i.enrich(ctx, gc, orgID, nodeID, w, DecisionTerminated))
		default: // allow
			out = append(out, i.enrich(ctx, gc, orgID, nodeID, w, DecisionAllow))
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
			agg := i.enrich(ctx, gc, orgID, nodeID, last, DecisionDenyAggregate)
			agg.OccurredAt = ds[0].OccurredAt.UTC() // window START
			agg.DenyCount = len(ds)
			end := last.OccurredAt.UTC() // window END
			agg.WindowEnd = &end
			out = append(out, agg)
			continue
		}
		for _, w := range ds {
			out = append(out, i.enrich(ctx, gc, orgID, nodeID, w, DecisionDeny))
		}
	}
	return out
}

// enrich builds an Event from a wire event. Attribution is GRANT-ONLY: rule_id → the
// grant's destination (resource/group). NO src_ip→device/user lookup (device/user stay nil
// — a racy IP map is forbidden). src_ip is kept as the raw observed fact.
func (i *Ingester) enrich(ctx context.Context, gc *grantCache, orgID, nodeID uuid.UUID, w WireEvent, d Decision) Event {
	e := Event{
		OrgID: orgID, NodeID: &nodeID, OccurredAt: w.OccurredAt.UTC(), Decision: d,
		SrcIP: w.SrcIP, DstIP: w.DstIP, Protocol: w.Protocol, DstPort: w.DstPort,
	}
	if rid, err := uuid.Parse(w.RuleID); err == nil {
		e.RuleID = &rid
		if res, grp, ok := gc.resolve(ctx, orgID, rid); ok { // per-batch memoized (fold-2 #5)
			e.DstResourceID, e.DstGroupID = res, grp // grant dst captured at event time
		}
	}
	return e
}

