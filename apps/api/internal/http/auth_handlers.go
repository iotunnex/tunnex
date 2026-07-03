package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
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

// Login implements POST /api/v1/auth/login. S2.1 verifies credentials; S2.2
// will establish the session cookie here.
func (s apiServer) Login(ctx context.Context, req api.LoginRequestObject) (api.LoginResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	user, err := s.auth.Authenticate(ctx, string(req.Body.Email), req.Body.Password)
	if err != nil {
		return nil, err
	}
	return api.Login200JSONResponse{
		Body: api.AuthUser{
			Id:            user.ID,
			Email:         openapi_types.Email(user.Email),
			EmailVerified: user.EmailVerifiedAt.Valid,
		},
		Headers: api.Login200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
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
