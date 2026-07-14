package accesslog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// InsertParams maps an Event to the sqlc insert params (pointer identity fields → nullable
// pgtype). deny_count defaults to 1 (the DB default) for non-aggregate events.
func InsertParams(e Event) sqlc.InsertAccessEventParams {
	dc := int32(e.DenyCount)
	if dc < 1 {
		dc = 1
	}
	return sqlc.InsertAccessEventParams{
		ID: e.ID, OrgID: e.OrgID, Seq: e.Seq,
		NodeID: pgUUID(e.NodeID), OccurredAt: e.OccurredAt, Decision: string(e.Decision),
		RuleID: pgUUID(e.RuleID), SrcDeviceID: pgUUID(e.SrcDeviceID), SrcUserID: pgUUID(e.SrcUserID),
		SrcIp: e.SrcIP, DstIp: e.DstIP, DstResourceID: pgUUID(e.DstResourceID), DstGroupID: pgUUID(e.DstGroupID),
		Protocol: e.Protocol, DstPort: i32Ptr(e.DstPort), DenyCount: dc, WindowEnd: pgTS(e.WindowEnd),
	}
}

// FromRow rebuilds an Event from a persisted row (for the query API + tests).
func FromRow(r sqlc.AccessEvent) Event {
	e := Event{
		ID: r.ID, Seq: r.Seq, OrgID: r.OrgID, NodeID: uuidPtr(r.NodeID), OccurredAt: r.OccurredAt,
		Decision: Decision(r.Decision), RuleID: uuidPtr(r.RuleID), SrcDeviceID: uuidPtr(r.SrcDeviceID),
		SrcUserID: uuidPtr(r.SrcUserID), SrcIP: r.SrcIp, DstIP: r.DstIp, DstResourceID: uuidPtr(r.DstResourceID),
		DstGroupID: uuidPtr(r.DstGroupID), Protocol: r.Protocol, DenyCount: int(r.DenyCount),
	}
	if r.DstPort != nil {
		e.DstPort = int(*r.DstPort)
	}
	if r.WindowEnd.Valid {
		t := r.WindowEnd.Time
		e.WindowEnd = &t
	}
	return e
}

// Retain runs the PG hot-window sweep (D3): delete by INGEST age, then trim each org to
// the row cap. The JSONL stream keeps the full record, so this is LOSSLESS for export.
// Returns the total rows deleted. `orgs` is the set to cap (callers pass orgs with events).
func Retain(ctx context.Context, q *sqlc.Queries, now time.Time, retention time.Duration, rowCap int32, orgs []uuid.UUID) (int64, error) {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if rowCap <= 0 {
		rowCap = DefaultPGRowCap
	}
	var deleted int64
	n, err := q.SweepAccessEventsByAge(ctx, now.Add(-retention))
	if err != nil {
		return deleted, err
	}
	deleted += n
	for _, org := range orgs {
		n, err := q.SweepAccessEventsOverCap(ctx, sqlc.SweepAccessEventsOverCapParams{OrgID: org, KeepNewest: rowCap})
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}

func pgUUID(p *uuid.UUID) pgtype.UUID {
	if p == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *p, Valid: true}
}

func uuidPtr(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	u := uuid.UUID(v.Bytes)
	return &u
}

func pgTS(p *time.Time) pgtype.Timestamptz {
	if p == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *p, Valid: true}
}

func i32Ptr(v int) *int32 {
	if v == 0 {
		return nil
	}
	x := int32(v)
	return &x
}
