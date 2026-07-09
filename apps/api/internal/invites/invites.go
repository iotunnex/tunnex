// Package invites implements admin-driven user invitations: create, accept,
// resend, revoke. It reuses the S1.1 invitations table (token hashed at rest,
// expiring, single-use) and the S0.3 mailer.
package invites

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/mail"
	"github.com/tunnexio/tunnex/apps/api/internal/password"
	"github.com/tunnexio/tunnex/apps/api/internal/pgerr"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

const inviteTTL = 7 * 24 * time.Hour

// AcceptAction is the reconciliation decision when an invite is accepted.
type AcceptAction int

const (
	// AcceptCreate: no local account — provision one.
	AcceptCreate AcceptAction = iota
	// AcceptAttach: a local account exists — attach the membership.
	AcceptAttach
)

func (a AcceptAction) String() string {
	if a == AcceptCreate {
		return "create"
	}
	return "attach"
}

// DecideAccept is the invite-accept policy. Unlike SSO's DecideLink there is no
// reject branch: possession of the invite token proves control of the invited
// inbox, so linking is always safe — AND accepting verifies the email (same
// evidence as a verification link). Verification is applied by the caller
// regardless of the prior state.
func DecideAccept(localExists bool) AcceptAction {
	if localExists {
		return AcceptAttach
	}
	return AcceptCreate
}

// Service provides invitation operations.
type Service struct {
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	mailer  mail.Mailer
	baseURL string
	logger  *slog.Logger
}

// NewService builds the invites service.
func NewService(pool *pgxpool.Pool, mailer mail.Mailer, baseURL string, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), mailer: mailer, baseURL: strings.TrimRight(baseURL, "/"), logger: logger}
}

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
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

// Create issues an invitation and emails the link.
// Create mints an invitation and returns the RAW accept token. The caller surfaces
// it to the inviting admin (the dashboard builds a copyable accept link — the
// primary delivery for SMTP-less self-hosts); the email is best-effort on top.
func (s *Service) Create(ctx context.Context, actor, orgID uuid.UUID, email, role string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !rbac.ValidRole(role) {
		return "", apierr.BadRequest("invalid_role", "unknown role: "+role)
	}
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.CreateInvitation(ctx, sqlc.CreateInvitationParams{
			OrgID: orgID, Email: email, Role: role, TokenHash: hash,
			ExpiresAt: time.Now().Add(inviteTTL), InvitedByUserID: pgUUID(actor),
		}); e != nil {
			if pgerr.IsUnique(e) {
				return apierr.Conflict("invite_pending", "an invitation is already pending for this email; resend or revoke it")
			}
			return e
		}
		return writeAudit(ctx, q, orgID, &actor, "invite.created", email, map[string]any{"role": role})
	})
	if err != nil {
		return "", err
	}
	s.mail(ctx, email, "You're invited to Tunnex", "You've been invited to join an organization on Tunnex: "+s.baseURL+"/accept-invite?token="+raw)
	return raw, nil
}

// Accept consumes an invite token, provisions/links the user (verifying the
// email — the token proves inbox control), and adds the membership.
func (s *Service) Accept(ctx context.Context, token, name, pw string) (uuid.UUID, uuid.UUID, error) {
	var userID, orgID uuid.UUID
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		inv, e := q.GetInvitationByTokenHash(ctx, hashToken(token))
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.BadRequest("invalid_invite", "this invitation is invalid or has expired")
		}
		if e != nil {
			return e
		}
		// AcceptInvitation only transitions a pending, unexpired invite (single-use).
		accepted, e := q.AcceptInvitation(ctx, inv.ID)
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.BadRequest("invalid_invite", "this invitation is invalid or has expired")
		}
		if e != nil {
			return e
		}
		orgID = accepted.OrgID

		local, e := q.GetUserByEmail(ctx, accepted.Email)
		switch {
		case errors.Is(e, pgx.ErrNoRows): // AcceptCreate
			var ph *string
			if pw != "" {
				if len(pw) < password.MinPasswordLen {
					return apierr.BadRequest("weak_password", password.ErrPasswordShort.Error())
				}
				h, herr := password.Hash(pw)
				if herr != nil {
					return herr
				}
				ph = &h
			}
			created, ce := q.CreateUser(ctx, sqlc.CreateUserParams{Email: accepted.Email, Name: name, PasswordHash: ph})
			if ce != nil {
				return ce
			}
			userID = created.ID
		case e != nil:
			return e
		default: // AcceptAttach
			// Never resurrect a frozen account into a new membership.
			if local.Status != "active" {
				return apierr.New(403, "account_deactivated", "this account is deactivated; contact an administrator")
			}
			userID = local.ID
		}

		// Accepting an invite verifies the email (token proves inbox control).
		if e := q.MarkEmailVerified(ctx, userID); e != nil {
			return e
		}
		if _, e := q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{OrgID: orgID, UserID: userID, Role: accepted.Role}); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, &userID, "invite.accepted", accepted.Email, map[string]any{"role": accepted.Role})
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return userID, orgID, nil
}

// Resend invalidates any pending invite for the email and issues a fresh one —
// the old token stops working; the same live token is never re-mailed.
func (s *Service) Resend(ctx context.Context, actor, orgID uuid.UUID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	raw, hash, err := newToken()
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.RevokeInvitationByOrgEmail(ctx, sqlc.RevokeInvitationByOrgEmailParams{OrgID: orgID, Email: email}); e != nil {
			return e
		}
		if _, e := q.CreateInvitation(ctx, sqlc.CreateInvitationParams{
			OrgID: orgID, Email: email, Role: rbac.RoleMember, TokenHash: hash,
			ExpiresAt: time.Now().Add(inviteTTL), InvitedByUserID: pgUUID(actor),
		}); e != nil {
			return e
		}
		return writeAudit(ctx, q, orgID, &actor, "invite.resent", email, nil)
	})
	if err != nil {
		return err
	}
	s.mail(ctx, email, "Your Tunnex invitation", "Your invitation link: "+s.baseURL+"/accept-invite?token="+raw)
	return nil
}

// Revoke cancels a pending invite. Revoking an already-accepted (or absent)
// invite is a clean error — never a membership removal.
func (s *Service) Revoke(ctx context.Context, actor, orgID uuid.UUID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.RevokeInvitationByOrgEmail(ctx, sqlc.RevokeInvitationByOrgEmailParams{OrgID: orgID, Email: email})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.NotFound("invite_not_pending", "no pending invitation for this email")
		}
		return writeAudit(ctx, q, orgID, &actor, "invite.revoked", email, nil)
	})
}

func (s *Service) mail(ctx context.Context, to, subject, body string) {
	if err := s.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Text: body}); err != nil {
		s.logger.Warn("invite_email_failed", slog.String("to", to), slog.String("error", err.Error()))
	}
}

func newToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte(id), Valid: true} }

func writeAudit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, actor *uuid.UUID, action, targetID string, meta map[string]any) error {
	b := []byte("{}")
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	tt := "invitation"
	_, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID: pgUUID(orgID), ActorUserID: pgUUID(*actor), Action: action,
		TargetType: &tt, TargetID: &targetID, Metadata: b,
	})
	return err
}
