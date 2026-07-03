//go:build enterprise

package sso

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// ProviderFactory builds a Provider for a given config (injectable for tests).
type ProviderFactory func(ctx context.Context, name, clientID, clientSecret, redirectURL string) (Provider, error)

// DefaultProviderFactory maps provider names to their OIDC issuers.
func DefaultProviderFactory(ctx context.Context, name, clientID, clientSecret, redirectURL string) (Provider, error) {
	switch name {
	case "google":
		return NewGoogle(ctx, clientID, clientSecret, redirectURL)
	default:
		return nil, apierr.BadRequest("unknown_provider", "unknown SSO provider: "+name)
	}
}

// Service orchestrates the SSO login flow.
type Service struct {
	pool     *pgxpool.Pool
	q        *sqlc.Queries
	configs  *ConfigService
	flows    *FlowStore
	factory  ProviderFactory
	baseURL  string
	logger   *slog.Logger
}

// NewService builds the SSO service.
func NewService(pool *pgxpool.Pool, configs *ConfigService, flows *FlowStore, factory ProviderFactory, baseURL string, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), configs: configs, flows: flows, factory: factory, baseURL: baseURL, logger: logger}
}

// Configs exposes the config service (for the admin set-config endpoint).
func (s *Service) Configs() *ConfigService { return s.configs }

func (s *Service) redirectURL(provider string) string {
	return s.baseURL + "/api/v1/auth/sso/" + provider + "/callback"
}

// StartLogin builds the IdP redirect URL for org/provider and stores flow state.
func (s *Service) StartLogin(ctx context.Context, orgID uuid.UUID, provider string) (string, error) {
	cfg, err := s.configs.Get(ctx, orgID, provider)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", apierr.NotFound("sso_not_configured", "SSO is not enabled for this provider")
	}
	prov, err := s.factory(ctx, provider, cfg.ClientID, cfg.ClientSecret, s.redirectURL(provider))
	if err != nil {
		return "", err
	}
	state, err := RandomToken()
	if err != nil {
		return "", err
	}
	nonce, err := RandomToken()
	if err != nil {
		return "", err
	}
	verifier, challenge, err := PKCE()
	if err != nil {
		return "", err
	}
	if err := s.flows.Save(ctx, state, flowState{Nonce: nonce, Verifier: verifier, OrgID: orgID, Provider: provider}); err != nil {
		return "", err
	}
	return prov.AuthCodeURL(state, nonce, challenge), nil
}

// HandleCallback validates state, exchanges+verifies the code, applies the
// linking policy, provisions/links the user, ensures org membership, and returns
// the resolved user id for the caller to mint a session.
func (s *Service) HandleCallback(ctx context.Context, provider, code, state string) (uuid.UUID, error) {
	fs, err := s.flows.Take(ctx, state)
	if err != nil {
		return uuid.Nil, apierr.BadRequest("invalid_state", "the SSO login could not be verified; please try again")
	}
	if fs.Provider != provider {
		return uuid.Nil, apierr.BadRequest("invalid_state", "provider mismatch")
	}
	cfg, err := s.configs.Get(ctx, fs.OrgID, provider)
	if err != nil {
		return uuid.Nil, err
	}
	prov, err := s.factory(ctx, provider, cfg.ClientID, cfg.ClientSecret, s.redirectURL(provider))
	if err != nil {
		return uuid.Nil, err
	}
	identity, err := prov.Exchange(ctx, code, fs.Verifier, fs.Nonce)
	if err != nil {
		return uuid.Nil, apierr.New(401, "sso_verification_failed", "could not verify the SSO login")
	}
	return s.resolveUser(ctx, identity, fs.OrgID)
}

// resolveUser applies DecideLink and provisions/links the user + org membership.
func (s *Service) resolveUser(ctx context.Context, id Identity, orgID uuid.UUID) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.withTx(ctx, func(q *sqlc.Queries) error {
		local, err := q.GetUserByEmail(ctx, id.Email)
		localExists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		localVerified := localExists && local.EmailVerifiedAt.Valid

		switch DecideLink(localExists, localVerified, id.EmailVerified) {
		case LinkReject:
			return apierr.New(409, "sso_link_required",
				"an account with this email exists; sign in and link SSO from settings")
		case LinkCreate:
			created, e := q.CreateUser(ctx, sqlc.CreateUserParams{Email: id.Email, Name: id.Name, PasswordHash: nil})
			if e != nil {
				return e
			}
			if e := q.MarkEmailVerified(ctx, created.ID); e != nil { // IdP asserted verified
				return e
			}
			userID = created.ID
		case LinkAttach:
			userID = local.ID
		}

		// JIT membership: an SSO login into an org makes the user a member.
		_, e := q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{OrgID: orgID, UserID: userID, Role: rbac.RoleMember})
		return e
	})
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
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
