// Package auth implements local authentication: signup, email verification,
// credential verification (login), and password reset.
//
// Adversarial-input properties (this is the first stranger-facing surface):
//   - Passwords: argon2id via internal/password, transparent rehash-on-login.
//   - Tokens: hashed at rest, purpose-bound, single-use, expiring.
//   - No account enumeration: signup and reset-request return an identical
//     generic result whether or not the email exists; login returns one generic
//     error regardless of which factor failed (and burns equal time on a
//     missing user via DummyVerify).
package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/mail"
	"github.com/tunnexio/tunnex/apps/api/internal/password"
)

const (
	purposeVerify = "email_verification"
	purposeReset  = "password_reset"

	verifyTTL = 24 * time.Hour
	resetTTL  = 1 * time.Hour
)

// SessionRevoker revokes all of a user's sessions (implemented by the session
// store). A password reset must invalidate every existing session.
type SessionRevoker interface {
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
}

// Service provides local-auth operations.
type Service struct {
	pool    *pgxpool.Pool
	q       *sqlc.Queries
	mailer  mail.Mailer
	baseURL string
	revoker SessionRevoker
	logger  *slog.Logger
}

// NewService builds the auth service.
func NewService(pool *pgxpool.Pool, mailer mail.Mailer, baseURL string, revoker SessionRevoker, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), mailer: mailer, baseURL: strings.TrimRight(baseURL, "/"), revoker: revoker, logger: logger}
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

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Signup creates an unverified account and emails a verification link. If the
// email already exists it emails an "account exists" notice instead — the caller
// gets the same generic result either way, so existence is never confirmed.
func (s *Service) Signup(ctx context.Context, email, name, pw string) error {
	if len(pw) < password.MinPasswordLen {
		return apierr.BadRequest("weak_password", password.ErrPasswordShort.Error())
	}
	email = normalizeEmail(email)

	existing, err := s.q.GetUserByEmail(ctx, email)
	if err == nil {
		s.send(ctx, existing.Email, "Your Tunnex account",
			"You already have a Tunnex account. If you didn't try to sign up, you can ignore this. "+
				"Forgot your password? Reset it at "+s.baseURL+"/reset-password")
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	raw, tokenHash, err := newToken()
	if err != nil {
		return err
	}

	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		user, e := q.CreateUser(ctx, sqlc.CreateUserParams{Email: email, Name: name, PasswordHash: &hash})
		if e != nil {
			return e
		}
		_, e = q.CreateAuthToken(ctx, sqlc.CreateAuthTokenParams{
			UserID: user.ID, Purpose: purposeVerify, TokenHash: tokenHash, ExpiresAt: time.Now().Add(verifyTTL),
		})
		return e
	})
	if err != nil {
		return err
	}
	s.send(ctx, email, "Verify your Tunnex email",
		"Welcome to Tunnex. Verify your email: "+s.baseURL+"/verify-email?token="+raw)
	return nil
}

// Authenticate verifies credentials and returns the user. It returns a single
// generic error for every failure mode, and spends equal time on a missing user.
// On success with a weak stored hash it transparently rehashes.
func (s *Service) Authenticate(ctx context.Context, email, pw string) (sqlc.User, error) {
	email = normalizeEmail(email)
	user, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		password.DummyVerify(pw) // equalize timing; no such user
		return sqlc.User{}, errInvalidCredentials()
	}
	if err != nil {
		return sqlc.User{}, err
	}
	if user.PasswordHash == nil {
		password.DummyVerify(pw) // SSO-only account: no password login
		return sqlc.User{}, errInvalidCredentials()
	}
	needsRehash, err := password.Verify(pw, *user.PasswordHash)
	if err != nil {
		return sqlc.User{}, errInvalidCredentials()
	}
	// Only after the password checks out (so we never leak account state to an
	// attacker) do we surface deactivation to the legitimate owner.
	if user.Status != "active" {
		return sqlc.User{}, apierr.New(403, "account_deactivated", "this account has been deactivated")
	}
	if needsRehash {
		if nh, herr := password.Hash(pw); herr == nil {
			if serr := s.q.SetUserPassword(ctx, sqlc.SetUserPasswordParams{ID: user.ID, PasswordHash: &nh}); serr != nil {
				s.logger.Warn("rehash_failed", slog.String("user_id", user.ID.String()), slog.String("error", serr.Error()))
			}
		}
	}
	return user, nil
}

// VerifyEmail consumes an email-verification token and marks the user verified.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		tok, err := q.ConsumeAuthToken(ctx, sqlc.ConsumeAuthTokenParams{TokenHash: hashToken(rawToken), Purpose: purposeVerify})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.BadRequest("invalid_token", "this verification link is invalid or has expired")
		}
		if err != nil {
			return err
		}
		return q.MarkEmailVerified(ctx, tok.UserID)
	})
}

// RequestPasswordReset emails a reset link if the account exists. It always
// returns nil (generic result) so existence is never confirmed.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	user, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no enumeration
	}
	if err != nil {
		return err
	}

	raw, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if e := q.InvalidateUserTokens(ctx, sqlc.InvalidateUserTokensParams{UserID: user.ID, Purpose: purposeReset}); e != nil {
			return e
		}
		_, e := q.CreateAuthToken(ctx, sqlc.CreateAuthTokenParams{
			UserID: user.ID, Purpose: purposeReset, TokenHash: tokenHash, ExpiresAt: time.Now().Add(resetTTL),
		})
		return e
	})
	if err != nil {
		return err
	}
	s.send(ctx, email, "Reset your Tunnex password",
		"Reset your Tunnex password: "+s.baseURL+"/reset-password?token="+raw)
	return nil
}

// ResetPassword consumes a reset token and sets a new password.
//
// S2.2 HANDOFF: on success, revoke all existing sessions for the user (a reset
// must log out other sessions). Sessions don't exist until S2.2 — wire it there.
func (s *Service) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	if len(newPassword) < password.MinPasswordLen {
		return apierr.BadRequest("weak_password", password.ErrPasswordShort.Error())
	}
	hash, err := password.Hash(newPassword)
	if err != nil {
		return err
	}
	var resetUserID uuid.UUID
	if err := s.withTx(ctx, func(q *sqlc.Queries) error {
		tok, err := q.ConsumeAuthToken(ctx, sqlc.ConsumeAuthTokenParams{TokenHash: hashToken(rawToken), Purpose: purposeReset})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.BadRequest("invalid_token", "this reset link is invalid or has expired")
		}
		if err != nil {
			return err
		}
		resetUserID = tok.UserID
		return q.SetUserPassword(ctx, sqlc.SetUserPasswordParams{ID: tok.UserID, PasswordHash: &hash})
	}); err != nil {
		return err
	}

	// A reset must invalidate every existing session for the user.
	if s.revoker != nil {
		if err := s.revoker.DeleteAllForUser(ctx, resetUserID); err != nil {
			s.logger.Warn("session_revoke_after_reset_failed",
				slog.String("user_id", resetUserID.String()), slog.String("error", err.Error()))
		}
	}
	return nil
}

func (s *Service) send(ctx context.Context, to, subject, body string) {
	if err := s.mailer.Send(ctx, mail.Message{To: to, Subject: subject, Text: body}); err != nil {
		s.logger.Warn("email_send_failed", slog.String("to", to), slog.String("error", err.Error()))
	}
}

func errInvalidCredentials() error {
	return apierr.New(401, "invalid_credentials", "invalid email or password")
}
