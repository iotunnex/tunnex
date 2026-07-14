package accesslog

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// Wire verdict values (the agent's flowlog.Verdict, as a string on the ingest wire).
const (
	wireAllow = "allow"
	wireDeny  = "deny"
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
// stores (PG hot-window + JSONL source-of-truth). One CP instance owns the seq counter
// (self-hosted single control plane); the (org_id, seq) unique index is the backstop.
type Ingester struct {
	q      *sqlc.Queries
	jsonl  *JSONLWriter
	grants GrantResolver
	now    func() time.Time

	mu       sync.Mutex
	seqByOrg map[uuid.UUID]int64
}

// NewIngester wires the querier, the JSONL stream, and the grant resolver. now defaults to
// time.Now.
func NewIngester(q *sqlc.Queries, jsonl *JSONLWriter, grants GrantResolver, now func() time.Time) *Ingester {
	if now == nil {
		now = time.Now
	}
	return &Ingester{q: q, jsonl: jsonl, grants: grants, now: now, seqByOrg: map[uuid.UUID]int64{}}
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
	for idx := range events {
		e := &events[idx]
		e.ID = uuid.New()
		seq, err := i.nextSeq(ctx, orgID)
		if err != nil {
			return err
		}
		e.Seq = seq
		if _, err := i.q.InsertAccessEvent(ctx, InsertParams(*e)); err != nil {
			return err
		}
		if i.jsonl != nil {
			if err := i.jsonl.Append(*e); err != nil {
				return err // JSONL is the source-of-truth; a write failure is real
			}
		}
	}
	return nil
}

// aggregate enriches each wire event (grant-only) and collapses per-source deny floods.
func (i *Ingester) aggregate(ctx context.Context, orgID, nodeID uuid.UUID, wire []WireEvent) []Event {
	out := make([]Event, 0, len(wire))
	denyBySrc := map[string][]WireEvent{}
	for _, w := range wire {
		if w.Verdict == wireDeny {
			denyBySrc[w.SrcIP] = append(denyBySrc[w.SrcIP], w)
			continue
		}
		out = append(out, i.enrich(ctx, orgID, nodeID, w, DecisionAllow))
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
			// Collapse: one deny_aggregate carrying the count + the window end (last seen).
			last := ds[len(ds)-1]
			agg := i.enrich(ctx, orgID, nodeID, last, DecisionDenyAggregate)
			agg.DenyCount = len(ds)
			end := last.OccurredAt
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

// nextSeq returns the next per-org monotonic sequence, resuming from the DB high-water on
// first use so the sequence never rewinds across a CP restart (gap-detection integrity).
func (i *Ingester) nextSeq(ctx context.Context, orgID uuid.UUID) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, seen := i.seqByOrg[orgID]; !seen {
		hi, err := i.q.MaxAccessEventSeqForOrg(ctx, orgID)
		if err != nil {
			return 0, err
		}
		i.seqByOrg[orgID] = hi
	}
	i.seqByOrg[orgID]++
	return i.seqByOrg[orgID], nil
}
