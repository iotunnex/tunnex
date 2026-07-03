package tenancy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
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

// withTx mirrors Service.withTx: mutation + audit are atomic; a nil pool (tests
// injecting a tx) runs on the pre-set querier.
func (s *MembershipService) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangeMemberRole changes a member's role, enforcing the RBAC relational rules
// (actorRole vs target vs new) and the last-owner invariant, and records a
// member.role_changed audit event atomically. actor is the acting user (nil only
// for system callers; user-initiated changes must pass it once auth lands).
func (s *MembershipService) ChangeMemberRole(ctx context.Context, actor *uuid.UUID, actorRole string, orgID, targetUserID uuid.UUID, newRole string) (sqlc.Membership, error) {
	if !rbac.ValidRole(newRole) {
		return sqlc.Membership{}, apierr.BadRequest("invalid_role", "unknown role: "+newRole)
	}
	var result sqlc.Membership
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		target, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: targetUserID})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("member_not_found", "member not found")
		}
		if e != nil {
			return e
		}
		if !rbac.CanManageMembership(actorRole, target.Role, newRole) {
			return apierr.New(403, "forbidden", "you may not change this member's role")
		}
		if e := s.guardLastOwner(ctx, q, orgID, target.Role, newRole); e != nil {
			return e
		}
		result, e = q.ChangeMemberRole(ctx, sqlc.ChangeMemberRoleParams{OrgID: orgID, UserID: targetUserID, Role: newRole})
		if e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, actor, "member.role_changed", "membership", targetUserID.String(),
			map[string]any{"role": map[string]string{"from": target.Role, "to": newRole}})
	})
	return result, err
}

// RemoveMember removes a member, enforcing the RBAC relational rules and the
// last-owner invariant, and records member.removed atomically.
func (s *MembershipService) RemoveMember(ctx context.Context, actor *uuid.UUID, actorRole string, orgID, targetUserID uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		target, e := q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: targetUserID})
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.NotFound("member_not_found", "member not found")
		}
		if e != nil {
			return e
		}
		if !rbac.CanManageMembership(actorRole, target.Role, "") {
			return apierr.New(403, "forbidden", "you may not remove this member")
		}
		if e := s.guardLastOwner(ctx, q, orgID, target.Role, ""); e != nil {
			return e
		}
		if _, e := q.RemoveMember(ctx, sqlc.RemoveMemberParams{OrgID: orgID, UserID: targetUserID}); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, actor, "member.removed", "membership", targetUserID.String(),
			map[string]any{"role": target.Role})
	})
}

// guardLastOwner rejects demoting or removing the final owner of an org.
func (s *MembershipService) guardLastOwner(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, targetRole, newRole string) error {
	losingAnOwner := targetRole == rbac.RoleOwner && newRole != rbac.RoleOwner // demotion or removal
	if !losingAnOwner {
		return nil
	}
	owners, err := q.CountOwners(ctx, orgID)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return apierr.Conflict("last_owner", "an organization must always have at least one owner")
	}
	return nil
}
