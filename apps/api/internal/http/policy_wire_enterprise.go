//go:build enterprise

package http

import (
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
