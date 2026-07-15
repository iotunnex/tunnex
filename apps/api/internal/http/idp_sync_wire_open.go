//go:build !enterprise

package http

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// NewIdpSyncPort returns nil in the open build: IdP-group sync is an enterprise feature, so the
// endpoints respond with the edition_required envelope. The signature matches the enterprise wire.
func NewIdpSyncPort(_ *pgxpool.Pool, _ *crypto.Sealer, _ *tenancy.MembershipService, _ *devices.Service, _ *slog.Logger) idpSyncPort {
	return nil
}

// StartIdpSyncPoller is a no-op in the open build (nothing to poll).
func StartIdpSyncPoller(_ context.Context, _ idpSyncPort, _ *slog.Logger) {}
