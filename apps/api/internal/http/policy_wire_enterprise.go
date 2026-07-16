//go:build enterprise

package http

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
)

// NewPolicyPort builds the enterprise Zero Trust policy port. The push hub is wired so
// every policy mutation signals the org's gateways to re-fetch + recompile within the
// <5s spec (S7.2). policy.Service returns sqlc rows, matching policyPort directly.
func NewPolicyPort(pool *pgxpool.Pool, hub *nodepush.Hub) policyPort {
	svc := policy.NewService(pool)
	svc.SetNotifier(hub)
	return svc
}

// StartPolicyGrantSweeper runs the S7.5.4 temporary-grant expiry sweep (enterprise
// only): a lapsed grant's /32 is pushed off every org gateway promptly (the compiler
// filter is the correctness backstop; this is promptness). No-op in the open build.
func StartPolicyGrantSweeper(ctx context.Context, pool *pgxpool.Pool, hub *nodepush.Hub) {
	svc := policy.NewService(pool)
	svc.SetNotifier(hub)
	go svc.StartGrantExpirySweeper(ctx)
}
