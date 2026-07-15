//go:build !enterprise

package http

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/accesslog"
)

// NewAccessLogPort — open build returns nil, so the access-log query endpoints reply
// 403 edition_required (Zero Trust visibility is enterprise).
func NewAccessLogPort(_ *pgxpool.Pool, _ *accesslog.Health) accessLogPort { return nil }
