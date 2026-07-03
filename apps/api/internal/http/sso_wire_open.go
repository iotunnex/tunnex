//go:build !enterprise

package http

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

// NewSSOPort returns nil in the open build: SSO is an enterprise feature, so the
// handlers respond with the edition_required envelope.
func NewSSOPort(_ *pgxpool.Pool, _ *crypto.Sealer, _ *redis.Client, _ string, _ *slog.Logger) ssoPort {
	return nil
}
