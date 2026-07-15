//go:build enterprise

package http

import (
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

// Export STREAMS the org's JSONL lines VERBATIM straight to the HTTP response via an io.Pipe
// — never materializing the whole export in memory (review #4: a high-volume org's unswept
// JSONL is many GiB → OOM-kills the CP for all tenants). ExportOrg copies raw bytes, never a
// re-serialize, so per-line seq is preserved byte-for-byte; a mid-stream read error is
// propagated to the HTTP reader via CloseWithError. contentLength is 0 (unknown → chunked;
// the generated response omits Content-Length when 0). The returned PipeReader is an
// io.ReadCloser, so the handler's Close unblocks the writer goroutine on client disconnect.
func (a *accessLogAdapter) Export(_ context.Context, orgID uuid.UUID) (io.Reader, int64, error) {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(accesslog.ExportOrg(a.dir, orgID, pw))
	}()
	return pr, 0, nil
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
