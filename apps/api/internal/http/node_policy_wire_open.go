//go:build !enterprise

package http

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// NewNodePolicyProvider returns nil in the open build: no Zero Trust policy is
// compiled, so the desired state omits the policy field and agents keep the mesh.
func NewNodePolicyProvider(_ *pgxpool.Pool) nodes.PolicyProvider { return nil }
