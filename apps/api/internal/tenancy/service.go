// Package tenancy holds the multi-tenant core services (organizations,
// memberships). It is edition-aware: the org limit comes from the enterprise
// boundary, so the open build caps org creation while the enterprise build does
// not — without any conditional logic leaking into the HTTP layer.
//
// Every mutation writes an audit_logs row in the SAME transaction as the change,
// so an org can never be created/updated/deleted without a corresponding audit
// record. The actor is currently null (endpoints are unauthenticated until S2).
package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// actorFromCtx returns the acting user id from the authenticated principal, or
// nil for system callers (seed/migration).
func actorFromCtx(ctx context.Context) *uuid.UUID {
	if p, ok := authctx.PrincipalFrom(ctx); ok {
		id := p.UserID
		return &id
	}
	return nil
}

// Service provides organization operations.
type Service struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewService builds a tenancy service over the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: sqlc.New(pool)}
}

// withTx runs fn inside a transaction (mutation + audit are atomic). When the
// service was constructed without a pool (tests injecting a tx), fn runs on the
// pre-set querier directly so the caller controls the transaction.
func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if s.pool == nil {
		return fn(s.q)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateOrganization creates an organization (enforcing the edition cap), makes
// the creator its first owner, and records an org.created audit event — all
// atomically.
func (s *Service) CreateOrganization(ctx context.Context, creator uuid.UUID, name, slug string) (sqlc.Organization, error) {
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

	var org sqlc.Organization
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		var e error
		org, e = q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{Name: name, Slug: slug})
		if e != nil {
			return mapDBError(e)
		}
		if _, e = q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{OrgID: org.ID, UserID: creator, Role: rbac.RoleOwner}); e != nil {
			return e
		}
		return writeAudit(ctx, q, org.ID, &creator, "org.created", "organization", org.ID.String(),
			map[string]any{"name": name, "slug": slug})
	})
	if err != nil {
		return sqlc.Organization{}, err
	}
	return org, nil
}

// ListOrganizationsForUser returns the live organizations the user belongs to.
func (s *Service) ListOrganizationsForUser(ctx context.Context, userID uuid.UUID) ([]sqlc.Organization, error) {
	return s.q.ListOrganizationsForUser(ctx, userID)
}

// GetOrganization returns a live organization or a typed not-found error.
func (s *Service) GetOrganization(ctx context.Context, id uuid.UUID) (sqlc.Organization, error) {
	org, err := s.q.GetOrganizationByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, orgNotFound()
	}
	return org, err
}

// ListOrganizations returns all live organizations.
func (s *Service) ListOrganizations(ctx context.Context) ([]sqlc.Organization, error) {
	return s.q.ListOrganizations(ctx)
}

// UpdateOrganization updates the mutable settings (name only — slug is
// immutable) and records an org.updated audit event atomically.
func (s *Service) UpdateOrganization(ctx context.Context, id uuid.UUID, name string) (sqlc.Organization, error) {
	var org sqlc.Organization
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		before, e := q.GetOrganizationByID(ctx, id)
		if errors.Is(e, pgx.ErrNoRows) {
			return orgNotFound()
		}
		if e != nil {
			return e
		}
		org, e = q.UpdateOrganizationName(ctx, sqlc.UpdateOrganizationNameParams{ID: id, Name: name})
		if e != nil {
			return e
		}
		return writeAudit(ctx, q, id, actorFromCtx(ctx), "org.updated", "organization", id.String(),
			map[string]any{"name": map[string]string{"from": before.Name, "to": name}})
	})
	if err != nil {
		return sqlc.Organization{}, err
	}
	return org, nil
}

// SoftDeleteOrganization soft-deletes an org and records org.deleted atomically.
func (s *Service) SoftDeleteOrganization(ctx context.Context, id uuid.UUID) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.SoftDeleteOrganization(ctx, id)
		if e != nil {
			return e
		}
		if n == 0 {
			return orgNotFound()
		}
		return writeAudit(ctx, q, id, actorFromCtx(ctx), "org.deleted", "organization", id.String(), map[string]any{})
	})
}

// writeAudit records an audit event in the caller's transaction. actor may be
// nil, which means a SYSTEM action (seed/migration/automation) — never an
// unattributed user action. Once auth lands (S2), user-initiated mutations
// (including every role change) MUST pass a non-nil actor.
func writeAudit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, actor *uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	actorID := pgtype.UUID{}
	if actor != nil {
		actorID = pgtype.UUID{Bytes: [16]byte(*actor), Valid: true}
	}
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: [16]byte(orgID), Valid: true},
		ActorUserID: actorID,
		Action:      action,
		TargetType:  &targetType,
		TargetID:    &targetID,
		Metadata:    b,
	})
	return err
}

func orgNotFound() error { return apierr.NotFound("org_not_found", "organization not found") }

// mapDBError converts known Postgres errors into typed API errors.
func mapDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return apierr.Conflict("slug_taken", "that organization slug is already in use")
	}
	return err
}

// OnlineWindow is the SINGLE SOURCE OF TRUTH for S3.6's online approximation: a
// device is "seen recently" if its last handshake is within this window
// (WireGuard has no live state). The HTTP device-list threshold aliases this
// (see http.onlineThreshold) so the dashboard tile and the per-device dot can
// never drift apart.
//
// Read predicates only need the LOWER bound (handshake >= now-OnlineWindow). The
// upper bound (handshake must not be in the future — which would pin a device
// "online" forever) is a DATA INVARIANT enforced once at ingestion in
// nodes.Service.ReportStatus (future handshakes past a small skew are dropped),
// so last_handshake_at is never future-dated at rest. Do not re-implement that
// clamp per read site — it would diverge from deviceOnline and duplicate the fix.
const OnlineWindow = 3 * time.Minute

// Overview is the dashboard aggregate for an org.
type Overview struct {
	Members        int64
	Devices        int64
	Nodes          int64
	Online         int64
	RecentActivity []sqlc.AuditLog
}

// Overview returns the org's counts + a recent audit slice for the dashboard
// home in a single service call (one API round-trip). Every read is org-scoped.
func (s *Service) Overview(ctx context.Context, orgID uuid.UUID) (Overview, error) {
	var o Overview
	var err error
	if o.Members, err = s.q.CountMembersByOrg(ctx, orgID); err != nil {
		return Overview{}, err
	}
	if o.Devices, err = s.q.CountActiveDevicesByOrg(ctx, orgID); err != nil {
		return Overview{}, err
	}
	if o.Nodes, err = s.q.CountActiveNodesByOrg(ctx, orgID); err != nil {
		return Overview{}, err
	}
	since := pgtype.Timestamptz{Time: time.Now().Add(-OnlineWindow), Valid: true}
	if o.Online, err = s.q.CountOnlineDevicesByOrg(ctx, sqlc.CountOnlineDevicesByOrgParams{OrgID: orgID, LastHandshakeAt: since}); err != nil {
		return Overview{}, err
	}
	// Latest 10, no filters/cursor — the same extended query the audit viewer uses
	// (all narg filters left nil/NULL = the unfiltered head of the feed).
	o.RecentActivity, err = s.q.ListAuditLogsByOrg(ctx, sqlc.ListAuditLogsByOrgParams{
		OrgID: pgtype.UUID{Bytes: orgID, Valid: true}, Lim: 10,
	})
	if err != nil {
		return Overview{}, err
	}
	return o, nil
}

// AuditFilter is the optional filter/cursor set for the audit-log viewer. A nil
// field means "unfiltered"; CursorTS+CursorID together fetch the page after that
// keyset position ((created_at,id) DESC).
type AuditFilter struct {
	Actor    *uuid.UUID
	Action   *string
	From, To *time.Time
	CursorTS *time.Time
	CursorID *uuid.UUID
	Limit    int32
}

// ListAuditLogs returns a keyset page of the org's audit feed, newest first,
// through the SAME extended query the dashboard's latest-N slice uses (no forked
// activity source). Org-scoped by the query-lint; every read stays within orgID.
func (s *Service) ListAuditLogs(ctx context.Context, orgID uuid.UUID, f AuditFilter) ([]sqlc.AuditLog, error) {
	p := sqlc.ListAuditLogsByOrgParams{OrgID: pgtype.UUID{Bytes: orgID, Valid: true}, Lim: f.Limit}
	if f.Actor != nil {
		p.Actor = pgtype.UUID{Bytes: *f.Actor, Valid: true}
	}
	p.Action = f.Action
	if f.From != nil {
		p.FromTs = pgtype.Timestamptz{Time: *f.From, Valid: true}
	}
	if f.To != nil {
		p.ToTs = pgtype.Timestamptz{Time: *f.To, Valid: true}
	}
	// Both cursor halves or neither — a half-cursor would silently disable paging.
	if f.CursorTS != nil && f.CursorID != nil {
		p.CursorTs = pgtype.Timestamptz{Time: *f.CursorTS, Valid: true}
		p.CursorID = pgtype.UUID{Bytes: *f.CursorID, Valid: true}
	}
	return s.q.ListAuditLogsByOrg(ctx, p)
}
