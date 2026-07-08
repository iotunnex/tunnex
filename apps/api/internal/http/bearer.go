package http

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
)

// BearerAuthFunc resolves an Authorization: Bearer CLI credential.
// Returns (principal, nil) for a valid credential; (nil, *apierr.Error) when
// the request MUST be refused distinctly (an EXPIRED credential — the CLI's
// "run 'tunnex login'" UX depends on the credential_expired code); (nil, nil)
// when the request is simply unauthenticated (no/unknown/revoked bearer —
// deliberately indistinguishable, failing closed at the handlers' generic 401).
type BearerAuthFunc func(r *http.Request) (*authctx.Principal, error)

// BearerAuth builds the bearer resolver. bearer ≡ cookie (S5.1 decision 2): the
// principal is constructed EXACTLY like SessionAuth's (user status check,
// memberships, verified state) so every downstream authorization rule applies
// identically to both credential types.
func BearerAuth(q *sqlc.Queries) BearerAuthFunc {
	return func(r *http.Request) (*authctx.Principal, error) {
		raw, ok := bearerToken(r)
		if !ok {
			return nil, nil
		}
		h := sha256.Sum256([]byte(raw))
		cred, err := q.GetCliCredentialByHash(r.Context(), h[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // unknown token — generic 401 downstream
		}
		if err != nil {
			return nil, nil // fail closed, no oracle
		}
		if cred.RevokedAt.Valid {
			return nil, nil // revoked ≡ unknown (no revocation oracle)
		}
		if !cred.ExpiresAt.After(time.Now()) {
			// Distinct code: the CLI renders "credential expired — run 'tunnex login'".
			return nil, apierr.New(http.StatusUnauthorized, "credential_expired",
				"this CLI credential has expired — run 'tunnex login' to mint a new one")
		}
		user, err := q.GetUserByID(r.Context(), cred.UserID)
		if err != nil {
			return nil, nil
		}
		// A deactivated user's credential dies on its very next request
		// (SessionAuth parity), independent of the deactivation sweep.
		if user.Status != "active" {
			return nil, nil
		}
		memberships, err := q.ListMembershipsByUser(r.Context(), cred.UserID)
		if err != nil {
			return nil, nil
		}
		roles := make(map[uuid.UUID]string, len(memberships))
		for _, m := range memberships {
			roles[m.OrgID] = m.Role
		}
		_ = q.TouchCliCredentialUsed(r.Context(), cred.ID) // best-effort telemetry
		return &authctx.Principal{
			UserID:        user.ID,
			Email:         user.Email,
			EmailVerified: user.EmailVerifiedAt.Valid,
			Roles:         roles,
		}, nil
	}
}

// bearerToken extracts a tnx_-prefixed bearer token, if present.
func bearerToken(r *http.Request) (string, bool) {
	const scheme = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, scheme) {
		return "", false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, scheme))
	if !strings.HasPrefix(tok, cliauth.TokenPrefix) {
		return "", false
	}
	return tok, true
}
