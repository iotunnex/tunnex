//go:build enterprise

package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
)

// publicDomains is an embedded blocklist of consumer email providers that must
// never be capturable. It is deliberately small and paired with the ownership
// inversion guard (the real defense); a maintained public-suffix-style list can
// replace it later without changing the guard.
var publicDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true,
	"live.com": true, "msn.com": true, "yahoo.com": true, "ymail.com": true,
	"icloud.com": true, "me.com": true, "aol.com": true, "proton.me": true,
	"protonmail.com": true, "gmx.com": true, "mail.com": true, "zoho.com": true,
	"pm.me": true, "fastmail.com": true, "yandex.com": true, "qq.com": true,
}

// IsPublicDomain reports whether a domain is a known consumer email provider.
func IsPublicDomain(domain string) bool { return publicDomains[strings.ToLower(strings.TrimSpace(domain))] }

// DNSResolver looks up TXT records (injectable for tests).
type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type netResolver struct{}

func (netResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// DomainService manages DNS-verified domain capture.
type DomainService struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	dns  DNSResolver
}

// NewDomainService builds a domain service with the real DNS resolver.
func NewDomainService(pool *pgxpool.Pool) *DomainService {
	return &DomainService{pool: pool, q: sqlc.New(pool), dns: netResolver{}}
}

// DomainFromEmail returns the lowercased domain part of an email.
func DomainFromEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

func (s *DomainService) withTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
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

// CreateClaim starts a domain claim. It refuses public domains and enforces the
// ownership inversion guard: the acting admin must already have a VERIFIED
// account at the domain being claimed (kills most mischief regardless of list
// freshness). Returns the TXT token the admin must publish.
func (s *DomainService) CreateClaim(ctx context.Context, actor uuid.UUID, actorEmail string, actorVerified bool, orgID uuid.UUID, domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || !strings.Contains(domain, ".") {
		return "", apierr.BadRequest("invalid_domain", "provide a valid domain")
	}
	if IsPublicDomain(domain) {
		return "", apierr.BadRequest("public_domain", "public email domains cannot be captured")
	}
	if !actorVerified || DomainFromEmail(actorEmail) != domain {
		return "", apierr.New(403, "domain_ownership_required",
			"you must have a verified account at this domain to claim it")
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.CreateDomainClaim(ctx, sqlc.CreateDomainClaimParams{OrgID: orgID, Domain: domain, VerificationToken: token}); e != nil {
			return mapClaimErr(e)
		}
		return audit(ctx, q, orgID, &actor, "domain.claim_created", "domain", domain, map[string]any{"domain": domain})
	})
	if err != nil {
		return "", err
	}
	return "tunnex-verify=" + token, nil
}

// Verify checks the domain's TXT records for the claim token and, on success,
// marks it verified (subject to the global-uniqueness index). Records the outcome.
func (s *DomainService) Verify(ctx context.Context, actor uuid.UUID, orgID uuid.UUID, domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	claim, err := s.q.GetDomainClaim(ctx, sqlc.GetDomainClaimParams{OrgID: orgID, Domain: domain})
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound("claim_not_found", "no claim for this domain")
	}
	if err != nil {
		return err
	}

	if !s.txtHasToken(ctx, domain, claim.VerificationToken) {
		_ = s.withTx(ctx, func(q *sqlc.Queries) error {
			_ = q.TouchDomainCheckedAt(ctx, sqlc.TouchDomainCheckedAtParams{OrgID: orgID, Domain: domain})
			return audit(ctx, q, orgID, &actor, "domain.verification_failed", "domain", domain, nil)
		})
		return apierr.BadRequest("verification_failed", "the tunnex-verify TXT record was not found")
	}

	return s.withTx(ctx, func(q *sqlc.Queries) error {
		if _, e := q.MarkDomainVerified(ctx, sqlc.MarkDomainVerifiedParams{OrgID: orgID, Domain: domain}); e != nil {
			return mapClaimErr(e) // another org already verified this domain -> conflict
		}
		return audit(ctx, q, orgID, &actor, "domain.verified", "domain", domain, nil)
	})
}

// CapturingOrgForEmail returns the org that has verified-captured the email's
// domain, re-checking the TXT record (verification loss suspends capture rather
// than joining stale members). Returns ok=false when there is no live capture.
func (s *DomainService) CapturingOrgForEmail(ctx context.Context, email string) (uuid.UUID, bool) {
	return s.capturingOrgTx(ctx, s.q, email)
}

// capturingOrgTx is CapturingOrgForEmail scoped to a caller-supplied querier so
// the lookup + any suspend participate in the JIT transaction.
func (s *DomainService) capturingOrgTx(ctx context.Context, q *sqlc.Queries, email string) (uuid.UUID, bool) {
	domain := DomainFromEmail(email)
	if domain == "" || IsPublicDomain(domain) {
		return uuid.Nil, false
	}
	claim, err := q.GetVerifiedClaimForDomain(ctx, domain)
	if err != nil {
		return uuid.Nil, false
	}
	if !s.txtHasToken(ctx, domain, claim.VerificationToken) {
		// Verification lost (record removed / domain changed hands): suspend.
		_ = q.SuspendDomainClaim(ctx, sqlc.SuspendDomainClaimParams{OrgID: claim.OrgID, Domain: domain})
		return uuid.Nil, false
	}
	return claim.OrgID, true
}

func (s *DomainService) txtHasToken(ctx context.Context, domain, token string) bool {
	records, err := s.dns.LookupTXT(ctx, domain)
	if err != nil {
		return false
	}
	want := "tunnex-verify=" + token
	for _, r := range records {
		if strings.TrimSpace(r) == want {
			return true
		}
	}
	return false
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func mapClaimErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apierr.Conflict("domain_taken", "this domain is already captured by another organization")
	}
	return err
}

// audit writes an audit row in the caller's transaction.
func audit(ctx context.Context, q *sqlc.Queries, orgID uuid.UUID, actor *uuid.UUID, action, targetType, targetID string, meta map[string]any) error {
	b := []byte("{}") // metadata is NOT NULL
	if meta != nil {
		b, _ = json.Marshal(meta)
	}
	actorID := pgtype.UUID{}
	if actor != nil {
		actorID = pgtype.UUID{Bytes: [16]byte(*actor), Valid: true}
	}
	_, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       pgtype.UUID{Bytes: [16]byte(orgID), Valid: true},
		ActorUserID: actorID,
		Action:      action,
		TargetType:  &targetType,
		TargetID:    &targetID,
		Metadata:    b,
	})
	return err
}
