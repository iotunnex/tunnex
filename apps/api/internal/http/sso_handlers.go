package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
)

// ssoPort is the enterprise SSO capability. It is nil in the open build, so the
// handlers return a clean edition_required envelope — same shape as the org cap.
type ssoPort interface {
	StartLogin(ctx context.Context, orgSlug, provider string) (redirectURL string, err error)
	HandleCallback(ctx context.Context, provider, code, state string) (userID uuid.UUID, err error)
	SetConfig(ctx context.Context, orgID uuid.UUID, provider, clientID, clientSecret string, enabled bool) error
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

// ssoCallbackResponse issues the post-login redirect and sets the session cookie.
type ssoCallbackResponse struct {
	sess     session.Session
	secure   bool
	location string
}

func (r ssoCallbackResponse) VisitSsoCallbackResponse(w http.ResponseWriter) error {
	session.SetCookie(w, r.sess, r.secure)
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
		return nil, err
	}
	sess, err := s.sessions.Create(ctx, userID) // SSO mints a fresh session (fixation rule)
	if err != nil {
		return nil, err
	}
	return ssoCallbackResponse{sess: sess, secure: s.cookieSecure, location: s.appBaseURL + "/"}, nil
}

// SetSsoConfig implements PUT /api/v1/organizations/{orgId}/sso/{provider}.
// authorize runs first so a sessionless request is 401 (keeps the spec 401-walk
// honest); the edition gate applies to authenticated callers on the open build.
func (s apiServer) SetSsoConfig(ctx context.Context, req api.SetSsoConfigRequestObject) (api.SetSsoConfigResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	if s.sso == nil {
		return nil, editionRequired()
	}
	if err := s.sso.SetConfig(ctx, req.OrgId, req.Provider, req.Body.ClientId, req.Body.ClientSecret, req.Body.Enabled); err != nil {
		return nil, err
	}
	return api.SetSsoConfig204Response{
		Headers: api.SetSsoConfig204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
