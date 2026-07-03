//go:build enterprise

package sso

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

// Config is a decrypted per-org SSO provider configuration.
type Config struct {
	OrgID        uuid.UUID
	Provider     string
	ClientID     string
	ClientSecret string // decrypted; never persisted in the clear
	TenantID     string // microsoft only (pinned Entra tenant); empty otherwise
	Enabled      bool
}

// ConfigService reads/writes per-org SSO config, sealing the client secret at
// rest with the bootstrap master key (S0.3) — its first real payload.
type ConfigService struct {
	q      *sqlc.Queries
	sealer *crypto.Sealer
}

// NewConfigService builds a config service.
func NewConfigService(pool *pgxpool.Pool, sealer *crypto.Sealer) *ConfigService {
	return &ConfigService{q: sqlc.New(pool), sealer: sealer}
}

// forQueries lets tests inject a tx-scoped querier.
func newConfigService(q *sqlc.Queries, sealer *crypto.Sealer) *ConfigService {
	return &ConfigService{q: q, sealer: sealer}
}

// Set upserts a provider config, sealing the client secret before storage.
func (c *ConfigService) Set(ctx context.Context, orgID uuid.UUID, provider, clientID, clientSecret, tenantID string, enabled bool) error {
	sealed, err := c.sealer.Seal([]byte(clientSecret))
	if err != nil {
		return err
	}
	var tid *string
	if tenantID != "" {
		tid = &tenantID
	}
	_, err = c.q.UpsertSSOConfig(ctx, sqlc.UpsertSSOConfigParams{
		OrgID:              orgID,
		Provider:           provider,
		ClientID:           clientID,
		ClientSecretSealed: []byte(sealed),
		TenantID:           tid,
		Enabled:            enabled,
	})
	return err
}

// Get returns the decrypted config for (org, provider).
func (c *ConfigService) Get(ctx context.Context, orgID uuid.UUID, provider string) (Config, error) {
	row, err := c.q.GetSSOConfig(ctx, sqlc.GetSSOConfigParams{OrgID: orgID, Provider: provider})
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, apierr.NotFound("sso_not_configured", "SSO is not configured for this provider")
	}
	if err != nil {
		return Config{}, err
	}
	secret, err := c.sealer.Open(string(row.ClientSecretSealed))
	if err != nil {
		return Config{}, err
	}
	tenantID := ""
	if row.TenantID != nil {
		tenantID = *row.TenantID
	}
	return Config{
		OrgID:        row.OrgID,
		Provider:     row.Provider,
		ClientID:     row.ClientID,
		ClientSecret: string(secret),
		TenantID:     tenantID,
		Enabled:      row.Enabled,
	}, nil
}
