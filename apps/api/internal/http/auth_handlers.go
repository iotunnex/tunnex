package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
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

// loginResponse is a custom oapi response object: its Visit sets the session cookie
// ONLY when the login fully authenticated (setCookie). On an MFA challenge (D6) no
// cookie is set — the pending state is a challenge token, never a session.
type loginResponse struct {
	body      api.LoginResult
	sess      session.Session
	setCookie bool
	secure    bool
	requestID string
}

func (r loginResponse) VisitLoginResponse(w http.ResponseWriter) error {
	if r.setCookie {
		session.SetCookie(w, r.sess, r.secure)
	}
	w.Header().Set("X-Request-Id", r.requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.body)
}

// Login verifies credentials. If the user has an armed TOTP (self-enrolled — S7.5.5 D1), NO
// session is minted; a challenge token is returned and the client completes at /auth/mfa/verify.
// Otherwise a fresh session is established (fixation-safe).
func (s apiServer) Login(ctx context.Context, req api.LoginRequestObject) (api.LoginResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	user, err := s.auth.Authenticate(ctx, string(req.Body.Email), req.Body.Password)
	if err != nil {
		return nil, err
	}
	reqID := middleware.GetReqID(ctx)

	if s.mfa != nil {
		challenged, cerr := s.mfa.HasConfirmedTOTP(ctx, user.ID)
		if cerr != nil {
			return nil, cerr
		}
		if challenged {
			token, ttl, e := s.mfa.CreateChallenge(ctx, user.ID)
			if e != nil {
				return nil, e
			}
			return loginResponse{body: api.LoginResult{MfaRequired: true, Challenge: &token, ChallengeExpiresIn: &ttl}, requestID: reqID}, nil
		}
	}

	sess, err := s.sessions.Create(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	au := authUser(user)
	result := api.LoginResult{MfaRequired: false, User: &au}
	// D8 grandfather: unenrolled user in an enforcing org gets a GATED session (enterprise only) —
	// authenticated, but the middleware restricts it to enrollment until a confirmed TOTP exists.
	if s.mfaEnforceEnabled && s.mfa != nil {
		if gated, gerr := s.mfa.IsEnrollmentGated(ctx, user.ID); gerr == nil && gated {
			tr := true
			au.MfaEnrollmentRequired = &tr
			result.User = &au
			result.EnrollmentRequired = &tr
		}
	}
	return loginResponse{body: result, sess: sess, setCookie: true, secure: s.cookieSecure, requestID: reqID}, nil
}

func authUser(user sqlc.User) api.AuthUser {
	return api.AuthUser{Id: user.ID, Email: openapi_types.Email(user.Email), EmailVerified: user.EmailVerifiedAt.Valid}
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
	au := api.AuthUser{
		Id:            p.UserID,
		Email:         openapi_types.Email(p.Email),
		EmailVerified: p.EmailVerified,
	}
	// Carry the gate state so a gated client (session minted, enrollment-restricted) can route to
	// the enrollment ceremony rather than hit dead 403s. Enterprise only.
	if s.mfaEnforceEnabled && s.mfa != nil {
		if gated, _ := s.mfa.IsEnrollmentGated(ctx, p.UserID); gated {
			tr := true
			au.MfaEnrollmentRequired = &tr
		}
	}
	return api.CurrentUser200JSONResponse{
		Body:    au,
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
