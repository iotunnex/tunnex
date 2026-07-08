package http

import (
	"context"
	"net/http"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
)

// S5.1 CLI credential flow — SPEC-FIRST STUBS. Every handler enforces its auth
// contract (the part the 401-walk and the verified-gate rely on) and then
// returns not_implemented until the implementation commit lands. The auth
// ordering here is the real one: authenticate → verify-gate → (work).

var errNotImplemented = apierr.New(http.StatusNotImplemented, "not_implemented",
	"this endpoint is specified but not implemented yet (S5.1 in progress)")

// CliAuthorize POST /api/v1/auth/cli/authorize — cookie-session only (argued in
// the spec: minting must not be reachable from a bearer credential) + verified.
func (s apiServer) CliAuthorize(ctx context.Context, _ api.CliAuthorizeRequestObject) (api.CliAuthorizeResponseObject, error) {
	if _, err := requireVerifiedUser(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

// CliToken POST /api/v1/auth/cli/token — public (the CLI holds nothing yet).
func (s apiServer) CliToken(ctx context.Context, _ api.CliTokenRequestObject) (api.CliTokenResponseObject, error) {
	return nil, errNotImplemented
}

// ListCliCredentials GET /api/v1/auth/cli/credentials — any authenticated
// principal (self-scoped read; verification not required, logout-class).
func (s apiServer) ListCliCredentials(ctx context.Context, _ api.ListCliCredentialsRequestObject) (api.ListCliCredentialsResponseObject, error) {
	if _, ok := authctx.PrincipalFrom(ctx); !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return nil, errNotImplemented
}

// RevokeCliCredential DELETE /api/v1/auth/cli/credentials/{credentialId} —
// self-scoped revoke; allowed unverified (logout-class, not org-mutating).
func (s apiServer) RevokeCliCredential(ctx context.Context, _ api.RevokeCliCredentialRequestObject) (api.RevokeCliCredentialResponseObject, error) {
	if _, ok := authctx.PrincipalFrom(ctx); !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return nil, errNotImplemented
}

// CliDeviceStart POST /api/v1/auth/cli/device — public.
func (s apiServer) CliDeviceStart(ctx context.Context, _ api.CliDeviceStartRequestObject) (api.CliDeviceStartResponseObject, error) {
	return nil, errNotImplemented
}

// CliDeviceApprove POST /api/v1/auth/cli/device/approve — cookie-session only
// (same argued exception as CliAuthorize) + verified: the human checkpoint.
func (s apiServer) CliDeviceApprove(ctx context.Context, _ api.CliDeviceApproveRequestObject) (api.CliDeviceApproveResponseObject, error) {
	if _, err := requireVerifiedUser(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

// CliDeviceToken POST /api/v1/auth/cli/device/token — public polling endpoint.
func (s apiServer) CliDeviceToken(ctx context.Context, _ api.CliDeviceTokenRequestObject) (api.CliDeviceTokenResponseObject, error) {
	return nil, errNotImplemented
}
