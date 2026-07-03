// Package tenancy holds the multi-tenant core services (organizations,
// memberships). It is edition-aware: the org limit comes from the enterprise
// boundary, so the open build caps org creation while the enterprise build does
// not — without any conditional logic leaking into the HTTP layer.
package tenancy

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise"
)

// Service provides organization operations.
type Service struct {
	q *sqlc.Queries
}

// NewService builds a tenancy service over the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: sqlc.New(pool)}
}

// CreateOrganization creates an organization, enforcing the edition's org cap.
// Returns a typed *apierr.Error ("org_limit_reached") when the open build's cap
// is hit, or ("slug_taken") on a duplicate slug.
func (s *Service) CreateOrganization(ctx context.Context, name, slug string) (sqlc.Organization, error) {
	if !enterprise.Unlimited {
		count, err := s.q.CountOrganizations(ctx)
		if err != nil {
			return sqlc.Organization{}, err
		}
		if count >= int64(enterprise.MaxOrganizations) {
			return sqlc.Organization{}, apierr.Forbidden("org_limit_reached",
				"the open edition supports a single organization; upgrade to Tunnex Enterprise for multiple organizations")
		}
	}

	org, err := s.q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{Name: name, Slug: slug})
	if err != nil {
		return sqlc.Organization{}, mapDBError(err)
	}
	return org, nil
}

// ListOrganizations returns all live organizations.
func (s *Service) ListOrganizations(ctx context.Context) ([]sqlc.Organization, error) {
	return s.q.ListOrganizations(ctx)
}

// mapDBError converts known Postgres errors into typed API errors.
func mapDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return apierr.Conflict("slug_taken", "that organization slug is already in use")
	}
	return err
}
