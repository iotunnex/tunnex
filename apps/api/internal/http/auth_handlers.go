package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
)

// Signup implements POST /api/v1/auth/signup. The response is deliberately
// generic (same for new and existing emails) to avoid account enumeration.
func (s apiServer) Signup(ctx context.Context, req api.SignupRequestObject) (api.SignupResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	if err := s.auth.Signup(ctx, string(req.Body.Email), name, req.Body.Password); err != nil {
		return nil, err
	}
	return api.Signup202JSONResponse{
		Body:    api.GenericMessage{Message: "If that email can be registered, a verification link has been sent."},
		Headers: api.Signup202ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// loginResponse is a custom oapi response object: its Visit sets the session
// cookie (the strict interface hands Visit the ResponseWriter).
type loginResponse struct {
	body      api.AuthUser
	sess      session.Session
	secure    bool
	requestID string
}

func (r loginResponse) VisitLoginResponse(w http.ResponseWriter) error {
	session.SetCookie(w, r.sess, r.secure)
	w.Header().Set("X-Request-Id", r.requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.body)
}

// Login verifies credentials, then establishes a fresh session (fixation-safe).
func (s apiServer) Login(ctx context.Context, req api.LoginRequestObject) (api.LoginResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	user, err := s.auth.Authenticate(ctx, string(req.Body.Email), req.Body.Password)
	if err != nil {
		return nil, err
	}
	sess, err := s.sessions.Create(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return loginResponse{
		body: api.AuthUser{
			Id:            user.ID,
			Email:         openapi_types.Email(user.Email),
			EmailVerified: user.EmailVerifiedAt.Valid,
		},
		sess:      sess,
		secure:    s.cookieSecure,
		requestID: middleware.GetReqID(ctx),
	}, nil
}

// logoutResponse clears the session cookie in its Visit.
type logoutResponse struct {
	secure    bool
	requestID string
}

func (r logoutResponse) VisitLogoutResponse(w http.ResponseWriter) error {
	session.ClearCookie(w, r.secure)
	w.Header().Set("X-Request-Id", r.requestID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ResendVerification re-sends the current user's email-verification link. Session
// gated; idempotent (202 even if already verified — reveals nothing).
func (s apiServer) ResendVerification(ctx context.Context, _ api.ResendVerificationRequestObject) (api.ResendVerificationResponseObject, error) {
	p, ok := authctx.PrincipalFrom(ctx)
	if !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	if err := s.auth.ResendVerification(ctx, p.UserID); err != nil {
		return nil, err
	}
	return api.ResendVerification202JSONResponse{
		Body:    api.GenericMessage{Message: "If your email is unverified, a verification link has been sent."},
		Headers: api.ResendVerification202ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// CurrentUser returns the session's user (for SPA auth rehydration on load), or
// 401 if there is no valid session. The principal is attached by SessionAuth.
func (s apiServer) CurrentUser(ctx context.Context, _ api.CurrentUserRequestObject) (api.CurrentUserResponseObject, error) {
	p, ok := authctx.PrincipalFrom(ctx)
	if !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return api.CurrentUser200JSONResponse{
		Body: api.AuthUser{
			Id:            p.UserID,
			Email:         openapi_types.Email(p.Email),
			EmailVerified: p.EmailVerified,
		},
		Headers: api.CurrentUser200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// Logout revokes the current session and clears the cookie.
func (s apiServer) Logout(ctx context.Context, _ api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	if p, ok := authctx.PrincipalFrom(ctx); ok && p.SessionID != "" {
		_ = s.sessions.Delete(ctx, p.SessionID)
	}
	return logoutResponse{secure: s.cookieSecure, requestID: middleware.GetReqID(ctx)}, nil
}

// VerifyEmail implements POST /api/v1/auth/verify-email.
func (s apiServer) VerifyEmail(ctx context.Context, req api.VerifyEmailRequestObject) (api.VerifyEmailResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if err := s.auth.VerifyEmail(ctx, req.Body.Token); err != nil {
		return nil, err
	}
	return api.VerifyEmail200JSONResponse{
		Body:    api.GenericMessage{Message: "Email verified."},
		Headers: api.VerifyEmail200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// RequestPasswordReset implements POST /api/v1/auth/password-reset. Always
// returns the same generic result to avoid enumeration.
func (s apiServer) RequestPasswordReset(ctx context.Context, req api.RequestPasswordResetRequestObject) (api.RequestPasswordResetResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if err := s.auth.RequestPasswordReset(ctx, string(req.Body.Email)); err != nil {
		return nil, err
	}
	return api.RequestPasswordReset202JSONResponse{
		Body:    api.GenericMessage{Message: "If that email is registered, a reset link has been sent."},
		Headers: api.RequestPasswordReset202ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// ConfirmPasswordReset implements POST /api/v1/auth/password-reset/confirm.
func (s apiServer) ConfirmPasswordReset(ctx context.Context, req api.ConfirmPasswordResetRequestObject) (api.ConfirmPasswordResetResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if err := s.auth.ResetPassword(ctx, req.Body.Token, req.Body.Password); err != nil {
		return nil, err
	}
	return api.ConfirmPasswordReset200JSONResponse{
		Body:    api.GenericMessage{Message: "Password updated."},
		Headers: api.ConfirmPasswordReset200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
