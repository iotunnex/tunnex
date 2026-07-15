//go:build enterprise

package http

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/idpsync"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// syncDeprovisioner adapts tenancy's full deactivate sweep to the reconciler's Deprovisioner seam,
// stamping the CAUSE. It lives here (not in idpsync) so the enterprise idpsync package stays free of
// a tenancy import — the Deprovisioner interface is the whole decoupling point (S7.5.2 slice 3).
type syncDeprovisioner struct{ members *tenancy.MembershipService }

func (d syncDeprovisioner) DeactivateForSync(ctx context.Context, orgID, userID uuid.UUID, _ string) error {
	return d.members.DeactivateMemberBySync(ctx, orgID, userID, "disabled_in_directory")
}

// NewIdpSyncPort builds the enterprise IdP-sync service: sqlc + AES-GCM sealer + the device pusher
// (the same org-wide recompile the tenancy sweep uses) + the deactivate sweep behind Deprovisioner.
func NewIdpSyncPort(pool *pgxpool.Pool, sealer *crypto.Sealer, members *tenancy.MembershipService, pusher *devices.Service, logger *slog.Logger) idpSyncPort {
	return idpsync.NewService(pool, sealer, pusher, syncDeprovisioner{members: members}, logger)
}

// StartIdpSyncPoller runs the background directory poll every 10 minutes (D2), jittered so many
// orgs don't stampede Graph on the same tick. First run is one interval out (boot isn't a sync
// trigger). Stops when ctx is cancelled.
func StartIdpSyncPoller(ctx context.Context, port idpSyncPort, logger *slog.Logger) {
	if port == nil {
		return
	}
	const base = 10 * time.Minute
	go func() {
		// A fixed per-process phase offset (0..119s) spreads load without per-tick randomness.
		jitter := time.Duration(uuid.New().ID()%120) * time.Second
		t := time.NewTimer(base + jitter)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				port.PollAll(ctx)
				t.Reset(base + jitter)
			}
		}
	}()
	logger.Info("idp_sync_poller_started", slog.Duration("interval", base))
}
