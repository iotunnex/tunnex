//go:build !enterprise

package http

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
)

// NewPolicyPort returns nil in the open build: Zero Trust policy is an enterprise
// feature, so the handlers respond with the edition_required envelope.
func NewPolicyPort(_ *pgxpool.Pool, _ *nodepush.Hub) policyPort {
	return nil
}
