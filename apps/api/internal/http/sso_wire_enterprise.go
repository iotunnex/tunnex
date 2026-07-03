//go:build enterprise

package http

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/sso"
)

// ssoAdapter bridges the enterprise sso.Service to the http ssoPort interface,
// resolving org slugs to ids (start) and org ids straight through (config).
type ssoAdapter struct {
	pool *pgxpool.Pool
	svc  *sso.Service
}

// NewSSOPort builds the enterprise SSO port. Present only in the enterprise
// build; the open build's stub returns nil (see sso_wire_open.go).
func NewSSOPort(pool *pgxpool.Pool, sealer *crypto.Sealer, rdb *redis.Client, baseURL string, logger *slog.Logger) ssoPort {
	configs := sso.NewConfigService(pool, sealer)
	flows := sso.NewFlowStore(rdb, 10*time.Minute)
	svc := sso.NewService(pool, configs, flows, sso.DefaultProviderFactory, baseURL, logger)
	return &ssoAdapter{pool: pool, svc: svc}
}

func (a *ssoAdapter) StartLogin(ctx context.Context, orgSlug, provider string) (string, error) {
	org, err := sqlc.New(a.pool).GetOrganizationBySlug(ctx, orgSlug)
	if err != nil {
		return "", apierr.NotFound("org_not_found", "organization not found")
	}
	return a.svc.StartLogin(ctx, org.ID, provider)
}

func (a *ssoAdapter) HandleCallback(ctx context.Context, provider, code, state string) (uuid.UUID, error) {
	return a.svc.HandleCallback(ctx, provider, code, state)
}

func (a *ssoAdapter) SetConfig(ctx context.Context, orgID uuid.UUID, provider, clientID, clientSecret, tenantID string, enabled bool) error {
	return a.svc.Configs().Set(ctx, orgID, provider, clientID, clientSecret, tenantID, enabled)
}
