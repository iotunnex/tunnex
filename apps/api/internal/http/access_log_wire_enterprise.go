//go:build enterprise

package http

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/accesslog"
)

// accessLogAdapter is the enterprise access-log query port: keyset reads over the PG
// hot-window, the JSONL source-of-truth export, and the shared Health snapshot. The query
// is org-scoped by construction (every query takes org_id — querylint-enforced).
type accessLogAdapter struct {
	q      *sqlc.Queries
	health *accesslog.Health
	dir    string // JSONL source-of-truth dir (for Export)
}

// NewAccessLogPort builds the enterprise port. The Health is SHARED with the flow-event
// Ingester (constructed in main) so the retention + JSONL-degraded signals the ingest side
// records are the same ones this endpoint surfaces. dir is the JSONL stream directory.
func NewAccessLogPort(pool *pgxpool.Pool, health *accesslog.Health, dir string) accessLogPort {
	return &accessLogAdapter{q: sqlc.New(pool), health: health, dir: dir}
}

// Export streams the org's JSONL lines VERBATIM (buffered; bounded org exports). ExportOrg
// copies raw bytes — never a re-serialize — so per-line seq is preserved byte-for-byte.
func (a *accessLogAdapter) Export(_ context.Context, orgID uuid.UUID) (io.Reader, int64, error) {
	var buf bytes.Buffer
	if err := accesslog.ExportOrg(a.dir, orgID, &buf); err != nil {
		return nil, 0, err
	}
	return &buf, int64(buf.Len()), nil
}

func (a *accessLogAdapter) List(ctx context.Context, orgID uuid.UUID, deniesOnly bool, cursorTS time.Time, cursorID uuid.UUID, limit int32) ([]accesslog.Event, error) {
	if deniesOnly {
		rows, err := a.q.ListAccessDenies(ctx, sqlc.ListAccessDeniesParams{OrgID: orgID, BeforeCreatedAt: cursorTS, BeforeID: cursorID, PageLimit: limit})
		if err != nil {
			return nil, err
		}
		out := make([]accesslog.Event, len(rows))
		for i, r := range rows {
			out[i] = accesslog.FromRow(r)
		}
		return out, nil
	}
	rows, err := a.q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{OrgID: orgID, BeforeCreatedAt: cursorTS, BeforeID: cursorID, PageLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]accesslog.Event, len(rows))
	for i, r := range rows {
		out[i] = accesslog.FromRow(r)
	}
	return out, nil
}

func (a *accessLogAdapter) Health() accesslog.Snapshot { return a.health.Snapshot() }
