//go:build enterprise

package http

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/enterprise/policy"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// NewNodePolicyProvider wires the enterprise policy engine as the node desired-state
// policy source (S7.2). policy.Service.CompiledForNode satisfies nodes.PolicyProvider.
func NewNodePolicyProvider(pool *pgxpool.Pool) nodes.PolicyProvider {
	return policy.NewService(pool)
}
