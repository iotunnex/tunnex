//go:build enterprise

package http

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
)

// NewPolicyPort builds the enterprise Zero Trust policy port. Present only in the
// enterprise build; the open build's stub returns nil (see policy_wire_open.go).
// policy.Service returns sqlc rows, matching the policyPort interface directly.
func NewPolicyPort(pool *pgxpool.Pool) policyPort {
	return policy.NewService(pool)
}
