package mfa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

const (
	challengeTTL = 5 * time.Minute
	maxAttempts  = 5 // D7 terminal per-challenge cap (TOTP + recovery share it)
)

// Service orchestrates TOTP enrollment + the login-time challenge. Enrollment is OPEN;
// org enforce (slice 2) layers on top. `now` is injectable for tests.
type Service struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	sealer *crypto.Sealer
	logger *slog.Logger
	now    func() time.Time
}

func NewService(pool *pgxpool.Pool, sealer *crypto.Sealer, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, q: sqlc.New(pool), sealer: sealer, logger: logger, now: time.Now}
}

func (s *Service) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// StartEnrollment (OPEN) generates a fresh secret, stores it SEALED + unconfirmed (replacing any
// prior unconfirmed attempt), and returns the otpauth URI (QR) + the base32 manual key ONCE. The
// account label on the URI is the user's email (resolved here so the handler needn't thread it).
func (s *Service) StartEnrollment(ctx context.Context, userID uuid.UUID) (uri, manualKey string, err error) {
	user, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	account := user.Email
	secret, err := GenerateSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := s.sealer.Seal([]byte(secret))
	if err != nil {
		return "", "", err
	}
	if err := s.q.UpsertUnconfirmedTOTP(ctx, sqlc.UpsertUnconfirmedTOTPParams{
		UserID: userID, SecretEnc: []byte(sealed),
	}); err != nil {
		return "", "", err
	}
	return OtpauthURI(secret, account), secret, nil
}

// ConfirmEnrollment (OPEN) arms MFA: a VALID code flips confirmed=true (verify-before-arm),
// stamps the replay clock, issues + stores single-use recovery codes (hashed), and audits
// mfa.enrolled. Returns the plaintext recovery codes ONCE.
func (s *Service) ConfirmEnrollment(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	row, err := s.q.GetTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.BadRequest("no_pending_enrollment", "start an enrollment first")
	}
	if err != nil {
		return nil, err
	}
	if row.Confirmed {
		return nil, apierr.Conflict("already_enrolled", "MFA is already enrolled; disenroll to re-enroll")
	}
	secret, err := s.sealer.Open(string(row.SecretEnc))
	if err != nil {
		return nil, err
	}
	ts, ok := Validate(string(secret), code, s.now().Unix(), -1)
	if !ok {
		return nil, apierr.BadRequest("invalid_code", "that code is not valid")
	}

	codes, err := GenerateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := s.withTx(ctx, func(q *sqlc.Queries) error {
		n, e := q.ConfirmTOTP(ctx, sqlc.ConfirmTOTPParams{UserID: userID, LastUsedTimestep: &ts})
		if e != nil {
			return e
		}
		if n == 0 {
			return apierr.Conflict("already_enrolled", "MFA is already enrolled")
		}
		if e := q.DeleteRecoveryCodesForUser(ctx, userID); e != nil { // clear any stale set
			return e
		}
		for _, c := range codes {
			if e := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{UserID: userID, CodeHash: HashCode(c)}); e != nil {
				return e
			}
		}
		return s.audit(ctx, q, userID, userID, "mfa.enrolled", nil)
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

// HasConfirmedTOTP reports whether the user has an armed TOTP (self-enrolled users are always
// challenged at login — D1). Unconfirmed enrollments do NOT count.
func (s *Service) HasConfirmedTOTP(ctx context.Context, userID uuid.UUID) (bool, error) {
	row, err := s.q.GetTOTP(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Confirmed, nil
}

// CreateChallenge mints the login second-step token (NOT a session — D6): a short-lived,
// hashed, attempt-capped challenge. Returns the RAW token + its TTL in seconds.
func (s *Service) CreateChallenge(ctx context.Context, userID uuid.UUID) (string, int, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", 0, err
	}
	if err := s.q.CreateMfaChallenge(ctx, sqlc.CreateMfaChallengeParams{
		UserID: userID, TokenHash: hash, ExpiresAt: s.now().Add(challengeTTL),
	}); err != nil {
		return "", 0, err
	}
	return raw, int(challengeTTL.Seconds()), nil
}

// VerifyChallenge resolves the login second step. It accepts a TOTP code (replay-guarded) OR a
// single-use recovery code, burns the challenge on SUCCESS or on cap exhaustion, and returns the
// authenticated user id (the handler then mints the full session). viaRecovery flags a
// recovery-code login (a security signal). The challenge + TOTP rows are locked so the
// attempt-count, replay clock, and burn all serialize.
func (s *Service) VerifyChallenge(ctx context.Context, rawToken, code string) (sqlc.User, bool, error) {
	var userID uuid.UUID
	var viaRecovery bool
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		ch, e := q.GetMfaChallengeForUpdate(ctx, hashToken(rawToken))
		if errors.Is(e, pgx.ErrNoRows) {
			return apierr.New(401, "mfa_challenge_invalid", "this login challenge is invalid or has expired — sign in again")
		}
		if e != nil {
			return e
		}
		userID = ch.UserID

		// TOTP first (replay-guarded), then recovery.
		if totp, e := q.GetConfirmedTOTPForUpdate(ctx, ch.UserID); e == nil {
			last := int64(-1)
			if totp.LastUsedTimestep != nil {
				last = *totp.LastUsedTimestep
			}
			secret, oe := s.sealer.Open(string(totp.SecretEnc))
			if oe != nil {
				return oe
			}
			if ts, ok := Validate(string(secret), code, s.now().Unix(), last); ok {
				if e := q.SetTOTPLastTimestep(ctx, sqlc.SetTOTPLastTimestepParams{UserID: ch.UserID, LastUsedTimestep: &ts}); e != nil {
					return e
				}
				return q.DeleteMfaChallenge(ctx, ch.ID) // burn on success
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		// Recovery code (atomic single-use).
		if _, e := q.ConsumeRecoveryCode(ctx, sqlc.ConsumeRecoveryCodeParams{UserID: ch.UserID, CodeHash: HashCode(code)}); e == nil {
			viaRecovery = true
			if e := q.DeleteMfaChallenge(ctx, ch.ID); e != nil { // burn on success
				return e
			}
			return s.audit(ctx, q, ch.UserID, ch.UserID, "mfa.recovery_code_used", map[string]any{"fingerprint": s.sealer.Fingerprint([]byte(normalizeCode(code)))})
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		// Wrong. Count against the terminal cap; burn on exhaustion.
		n, e := q.IncrementMfaChallengeAttempts(ctx, ch.ID)
		if e != nil {
			return e
		}
		if int(n) >= maxAttempts {
			if e := q.DeleteMfaChallenge(ctx, ch.ID); e != nil {
				return e
			}
			return apierr.New(401, "mfa_challenge_exhausted", "too many attempts — sign in again")
		}
		return apierr.New(401, "invalid_code", "that code is not valid")
	})
	if err != nil {
		return sqlc.User{}, false, err
	}
	user, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return sqlc.User{}, false, err
	}
	return user, viaRecovery, nil
}

// Disenroll removes the user's TOTP + recovery codes (self re-enroll / admin reset), audited.
func (s *Service) Disenroll(ctx context.Context, actor, target uuid.UUID, action string) error {
	return s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.DeleteTOTP(ctx, target); e != nil {
			return e
		}
		if e := q.DeleteRecoveryCodesForUser(ctx, target); e != nil {
			return e
		}
		return s.audit(ctx, q, target, actor, action, nil)
	})
}

// CountRecoveryRemaining returns the user's unused recovery-code count (low-remaining warnings).
func (s *Service) CountRecoveryRemaining(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.q.CountUnusedRecoveryCodes(ctx, userID)
	return int(n), err
}

// audit scopes a user-global MFA event to the user's PRIMARY (earliest) membership org — a
// single-org user (the common case) is exact; a multi-org user gets a deterministic primary.
func (s *Service) audit(ctx context.Context, q *sqlc.Queries, subjectUser, actor uuid.UUID, action string, meta map[string]any) error {
	orgID, err := s.primaryOrg(ctx, q, subjectUser)
	if err != nil {
		s.logger.Warn("mfa_audit_org_unresolved", slog.String("user_id", subjectUser.String()), slog.String("action", action))
		return nil // never fail the security action on an audit-scope miss; slog carries it
	}
	b := []byte("{}")
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	tt, tid := "user", subjectUser.String()
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: [16]byte(orgID), Valid: true},
		ActorUserID: pgtype.UUID{Bytes: [16]byte(actor), Valid: true},
		Action:      action, TargetType: &tt, TargetID: &tid, Metadata: b,
	})
	return err
}

func (s *Service) primaryOrg(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (uuid.UUID, error) {
	ms, err := q.ListMembershipsByUser(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	if len(ms) == 0 {
		return uuid.Nil, errors.New("no membership")
	}
	return ms[0].OrgID, nil
}

// newToken returns a random raw challenge token + its sha256 hash (join-token hygiene).
func newToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, h[:], nil
}

func hashToken(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }
