package tenancy

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// SessionRevoker revokes all of a user's sessions (implemented by the session
// store). Deactivation uses it to cut live access immediately.
type SessionRevoker interface {
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
}

// DevicePusher nudges the nodes carrying a user's peers to reconcile now, so an
// account state change (de/reactivation) removes/restores that user's peers from
// the data plane within seconds. Implemented by devices.Service.
type DevicePusher interface {
	PushUserNodes(ctx context.Context, userID uuid.UUID)
	// PushOrgNodes signals all org gateways — used for org-wide policy changes like
	// member removal (S7.2 F1 4th recompile+push trigger).
	PushOrgNodes(ctx context.Context, orgID uuid.UUID)
}

// MembershipService provides org-scoped membership reads. Every method scopes by
// org_id (see the query-lint), so it cannot return another tenant's rows.
type MembershipService struct {
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	revoker SessionRevoker
	pusher  DevicePusher
}

// NewMembershipService builds a membership service over the given pool.
func NewMembershipService(pool *pgxpool.Pool, revoker SessionRevoker) *MembershipService {
	return &MembershipService{pool: pool, q: sqlc.New(pool), revoker: revoker}
}

// WithDevicePusher wires the offboarding peer cascade (optional).
func (s *MembershipService) WithDevicePusher(p DevicePusher) *MembershipService {
	s.pusher = p
	return s
}

// DeactivateMember freezes a user's account (status only — memberships and role
// history are preserved for a clean reactivation) and revokes every live
// session. It refuses to deactivate the sole owner of any org (last-owner
// invariant) so an org can never be orphaned.
func (s *MembershipService) DeactivateMember(ctx context.Context, actor, orgID, targetUserID uuid.UUID) error {
	// target must belong to the acting org (authorization scope).
	if _, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: targetUserID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("member_not_found", "member not found")
		}
		return err
	}
	sole, err := s.q.CountOrgsWhereSoleOwner(ctx, targetUserID)
	if err != nil {
		return err
	}
	if sole > 0 {
		return apierr.Conflict("last_owner", "this user is the only owner of an organization and cannot be deactivated")
	}
	if err := s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: targetUserID, Status: "deactivated"}); e != nil {
			return e
		}
		// Deactivation sweeps CLI credentials too (S5.1, session parity) — in the
		// SAME tx as the status flip, so the sweep can't be lost between the two.
		if e := cliauth.SweepUser(ctx, q, targetUserID); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, &actor, "user.deactivated", "user", targetUserID.String(), map[string]any{})
	}); err != nil {
		return err
	}
	// Cut live access immediately (belt-and-suspenders with the SessionAuth
	// status check that also 401s any in-flight session).
	if s.revoker != nil {
		if err := s.revoker.DeleteAllForUser(ctx, targetUserID); err != nil {
			return err
		}
	}
	// Offboarding cascade: the user's peers now fall out of every node's desired
	// state (the peer query requires an active owner); push so they are removed
	// from the data plane within seconds, not at the next interval.
	if s.pusher != nil {
		s.pusher.PushUserNodes(ctx, targetUserID)
	}
	return nil
}

// ReactivateMember restores a frozen user; memberships/roles are intact.
func (s *MembershipService) ReactivateMember(ctx context.Context, actor, orgID, targetUserID uuid.UUID) error {
	if _, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: targetUserID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("member_not_found", "member not found")
		}
		return err
	}
	if err := s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: targetUserID, Status: "active"}); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, &actor, "user.reactivated", "user", targetUserID.String(), map[string]any{})
	}); err != nil {
		return err
	}
	// Restore the user's peers to the data plane promptly (they re-enter desired
	// state now that the owner is active again).
	if s.pusher != nil {
		s.pusher.PushUserNodes(ctx, targetUserID)
	}
	return nil
}

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

// ListMembers returns the BARE membership rows of a single org (no user join,
// no soft-delete filter). The Users UI uses ListMembersWithUser instead (roster
// with name/email/status, soft-deleted excluded); this variant is retained for
// the cross-tenant isolation test. Prefer ListMembersWithUser for anything
// user-facing so soft-deleted users can't leak.
func (s *MembershipService) ListMembers(ctx context.Context, orgID uuid.UUID) ([]sqlc.Membership, error) {
	return s.q.ListMembershipsByOrg(ctx, orgID)
}

// ListMembersWithUser returns the org roster enriched with user fields
// (name/email/status/verified) for the Users page. Org-scoped; excludes
// soft-deleted users, keeps deactivated members (status carries that).
func (s *MembershipService) ListMembersWithUser(ctx context.Context, orgID uuid.UUID) ([]sqlc.ListOrgMembersWithUserRow, error) {
	return s.q.ListOrgMembersWithUser(ctx, orgID)
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
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
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
	if err == nil && s.pusher != nil {
		// F1 4th trigger: member removal is an org-wide policy change (group_members
		// cascade-dropped in the tx). Push ALL org gateways so the ex-member's /32
		// leaves every compiled ruleset within the <5s spec, not just their own nodes.
		s.pusher.PushOrgNodes(ctx, orgID)
	}
	return err
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
