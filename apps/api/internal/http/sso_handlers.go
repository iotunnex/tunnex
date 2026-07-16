package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
)

// ssoPort is the enterprise SSO capability. It is nil in the open build, so the
// handlers return a clean edition_required envelope — same shape as the org cap.
type ssoPort interface {
	StartLogin(ctx context.Context, orgSlug, provider string) (redirectURL string, err error)
	HandleCallback(ctx context.Context, provider, code, state string) (userID uuid.UUID, err error)
	SetConfig(ctx context.Context, actor, orgID uuid.UUID, provider, clientID, clientSecret, tenantID string, enabled bool) error
	ViewConfig(ctx context.Context, orgID uuid.UUID, provider string) (SSOConfigView, error)
	CreateDomainClaim(ctx context.Context, actor uuid.UUID, actorEmail string, actorVerified bool, orgID uuid.UUID, domain string) (txtRecord string, err error)
	VerifyDomain(ctx context.Context, actor, orgID uuid.UUID, domain string) error
}

// SSOConfigView is the non-secret projection returned by the read endpoint. It
// carries the keyed fingerprint but NEVER the client secret (sealed or plain).
type SSOConfigView struct {
	Provider          string
	ClientID          string
	TenantID          string
	SecretFingerprint string
	Enabled           bool
	UpdatedAt         time.Time
}

func editionRequired() error {
	return apierr.New(http.StatusForbidden, "edition_required", "SSO is a Tunnex Enterprise feature")
}

// StartSsoLogin implements GET /api/v1/auth/sso/{provider}/start.
func (s apiServer) StartSsoLogin(ctx context.Context, req api.StartSsoLoginRequestObject) (api.StartSsoLoginResponseObject, error) {
	if s.sso == nil {
		return nil, editionRequired()
	}
	url, err := s.sso.StartLogin(ctx, req.Params.Org, string(req.Provider))
	if err != nil {
		return nil, err
	}
	return api.StartSsoLogin200JSONResponse{
		Body:    api.SsoRedirect{RedirectUrl: url},
		Headers: api.StartSsoLogin200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// ssoCallbackResponse issues a browser redirect after the IdP round-trip. On
// success it sets the session cookie and lands in the app; on failure it carries
// no cookie and lands on the login page with an error code the SPA renders as
// human-readable guidance (never a raw JSON error envelope — the browser followed
// the IdP redirect here, so a person is looking at this response).
type ssoCallbackResponse struct {
	sess      session.Session
	setCookie bool
	secure    bool
	location  string
}

func (r ssoCallbackResponse) VisitSsoCallbackResponse(w http.ResponseWriter) error {
	if r.setCookie {
		session.SetCookie(w, r.sess, r.secure)
	}
	w.Header().Set("Location", r.location)
	w.WriteHeader(http.StatusFound)
	return nil
}

// SsoCallback implements GET /api/v1/auth/sso/{provider}/callback.
func (s apiServer) SsoCallback(ctx context.Context, req api.SsoCallbackRequestObject) (api.SsoCallbackResponseObject, error) {
	if s.sso == nil {
		return nil, editionRequired()
	}
	userID, err := s.sso.HandleCallback(ctx, string(req.Provider), req.Params.Code, req.Params.State)
	if err != nil {
		// Redirect to a human-readable login landing carrying the reject reason,
		// not a raw error body. Reflect only KNOWN reject codes into the URL (never
		// arbitrary error text), falling back to a generic code otherwise.
		return ssoCallbackResponse{location: s.appBaseURL + "/login?sso_error=" + ssoErrorCode(err)}, nil
	}
	sess, err := s.sessions.Create(ctx, userID, authctx.AuthSSO) // SSO mints a fresh session (fixation rule)
	if err != nil {
		return nil, err
	}
	return ssoCallbackResponse{sess: sess, setCookie: true, secure: s.cookieSecure, location: s.appBaseURL + "/"}, nil
}

// ssoRejectCodes is the allowlist of SSO callback reject reasons the SPA renders
// as guidance. Anything else collapses to "sso_failed" so no arbitrary error
// text is ever reflected into the login URL.
var ssoRejectCodes = map[string]bool{
	"unverified_local_exists": true,
	"idp_email_unverified":    true,
	"edition_required":        true,
}

func ssoErrorCode(err error) string {
	var ae *apierr.Error
	if errors.As(err, &ae) && ssoRejectCodes[ae.Code] {
		return ae.Code
	}
	return "sso_failed"
}

// SetSsoConfig implements PUT /api/v1/organizations/{orgId}/sso/{provider}.
// authorize runs FIRST so a sessionless request is 401 before any body check
// (keeps the spec 401-walk honest and doesn't leak route existence); the edition
// gate applies to authenticated callers on the open build.
func (s apiServer) SetSsoConfig(ctx context.Context, req api.SetSsoConfigRequestObject) (api.SetSsoConfigResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if s.sso == nil {
		return nil, editionRequired()
	}
	p, _ := authctx.PrincipalFrom(ctx)
	tenantID := ""
	if req.Body.TenantId != nil {
		tenantID = *req.Body.TenantId
	}
	if err := s.sso.SetConfig(ctx, p.UserID, req.OrgId, req.Provider, req.Body.ClientId, req.Body.ClientSecret, tenantID, req.Body.Enabled); err != nil {
		return nil, err
	}
	return api.SetSsoConfig204Response{
		Headers: api.SetSsoConfig204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// GetSsoConfig implements GET /api/v1/organizations/{orgId}/sso/{provider}. It
// returns the NON-SECRET view (keyed fingerprint, never the secret — the field
// doesn't exist in the response type). 401 first (keeps the spec 401-walk
// honest), then the edition gate on the open build (403 edition_required).
func (s apiServer) GetSsoConfig(ctx context.Context, req api.GetSsoConfigRequestObject) (api.GetSsoConfigResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	if s.sso == nil {
		return nil, editionRequired()
	}
	v, err := s.sso.ViewConfig(ctx, req.OrgId, string(req.Provider))
	if err != nil {
		return nil, err
	}
	body := api.SsoConfigView{
		Provider:          api.SsoConfigViewProvider(v.Provider),
		ClientId:          v.ClientID,
		Enabled:           v.Enabled,
		SecretFingerprint: v.SecretFingerprint,
		UpdatedAt:         v.UpdatedAt,
	}
	if v.TenantID != "" {
		body.TenantId = &v.TenantID
	}
	return api.GetSsoConfig200JSONResponse{
		Body:    body,
		Headers: api.GetSsoConfig200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// CreateDomainClaim implements POST /api/v1/organizations/{orgId}/domains.
func (s apiServer) CreateDomainClaim(ctx context.Context, req api.CreateDomainClaimRequestObject) (api.CreateDomainClaimResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	if s.sso == nil {
		return nil, editionRequired()
	}
	p, _ := authctx.PrincipalFrom(ctx) // non-nil after authorize
	txt, err := s.sso.CreateDomainClaim(ctx, p.UserID, p.Email, p.EmailVerified, req.OrgId, req.Body.Domain)
	if err != nil {
		return nil, err
	}
	return api.CreateDomainClaim201JSONResponse{
		Body:    api.DomainClaimResponse{TxtRecord: txt},
		Headers: api.CreateDomainClaim201ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// VerifyDomainClaim implements POST /api/v1/organizations/{orgId}/domains/verify.
func (s apiServer) VerifyDomainClaim(ctx context.Context, req api.VerifyDomainClaimRequestObject) (api.VerifyDomainClaimResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	if s.sso == nil {
		return nil, editionRequired()
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.sso.VerifyDomain(ctx, p.UserID, req.OrgId, req.Body.Domain); err != nil {
		return nil, err
	}
	return api.VerifyDomainClaim200JSONResponse{
		Body:    api.GenericMessage{Message: "Domain verified."},
		Headers: api.VerifyDomainClaim200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
