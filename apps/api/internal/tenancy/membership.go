package tenancy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
)

// MembershipService provides org-scoped membership reads. Every method scopes by
// org_id (see the query-lint), so it cannot return another tenant's rows.
type MembershipService struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewMembershipService builds a membership service over the given pool.
func NewMembershipService(pool *pgxpool.Pool) *MembershipService {
	return &MembershipService{pool: pool, q: sqlc.New(pool)}
}

// ListMembers returns the memberships of a single org.
func (s *MembershipService) ListMembers(ctx context.Context, orgID uuid.UUID) ([]sqlc.Membership, error) {
	return s.q.ListMembershipsByOrg(ctx, orgID)
}

// GetMember returns a membership scoped to (orgID, userID), or a typed not-found
// error. Because the lookup is org-scoped, a user in another org reads as
// not-found — no cross-tenant existence leak.
func (s *MembershipService) GetMember(ctx context.Context, orgID, userID uuid.UUID) (sqlc.Membership, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Membership{}, apierr.NotFound("member_not_found", "member not found")
	}
	return m, err
}
