//go:build !enterprise

package http

import "github.com/jackc/pgx/v5/pgxpool"

// NewPolicyPort returns nil in the open build: Zero Trust policy is an enterprise
// feature, so the handlers respond with the edition_required envelope.
func NewPolicyPort(_ *pgxpool.Pool) policyPort {
	return nil
}
