// Package http wires the API's HTTP routes and middleware. Routes are mounted
// from the generated OpenAPI server (internal/api) so the wire contract is the
// spec, not hand-written handlers.
package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	oapimw "github.com/oapi-codegen/nethttp-middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/auth"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
	"github.com/tunnexio/tunnex/apps/api/internal/mfa"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	"github.com/tunnexio/tunnex/apps/api/internal/invites"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// AuthFunc resolves the authenticated principal for a request, or nil if the
// request is unauthenticated. SessionAuth is the session-backed implementation.
type AuthFunc func(r *http.Request) *authctx.Principal

// Deps are the router's dependencies.
type Deps struct {
	Orgs      *tenancy.Service
	CliAuth   *cliauth.Service
	Auth      *auth.Service
	Members   *tenancy.MembershipService
	Invites   *invites.Service
	Nodes     *nodes.Service
	Devices   *devices.Service
	Sessions  *session.Store
	Mfa       *mfa.Service  // OPEN (all editions): TOTP enrollment + login challenge (S7.5.5)
	SSO       ssoPort       // nil => open build (SSO endpoints return edition_required)
	Policy    policyPort    // nil => open build (policy endpoints return edition_required)
	AccessLog accessLogPort // nil => open build (access-log endpoints return edition_required)
	IdpSync   idpSyncPort   // nil => open build (idp-sync endpoints return edition_required)
	// DeviceApprovalEnabled => false in the open build (S7.3 device posture endpoints
	// return edition_required). Named per-feature (NewDeviceApprovalEdition).
	DeviceApprovalEnabled bool
	// DeviceHealthEnabled => false in the open build (S7.5.3 device health/posture-check
	// endpoints return edition_required). Named per-feature (NewDeviceHealthEdition).
	DeviceHealthEnabled bool
	CookieSecure        bool
	AppBaseURL          string
	// CORSAllowedOrigins are exact origins allowed cross-origin bearer access
	// (S6.2 desktop; app://tunnex). Empty = no CORS (pure same-origin).
	CORSAllowedOrigins []string
	AuthFn             AuthFunc
	// BearerFn resolves a CLI bearer credential (S5.1). Tried BEFORE the cookie
	// session; any invalid bearer (unknown/revoked/expired) is one generic 401
	// (no oracle) — the CLI recognizes expiry from its local expires_at.
	BearerFn BearerAuthFunc
}

// NewRouter builds the API router with the standard middleware chain and mounts
// the generated OpenAPI handlers.
//
// Middleware order matters: RequestID runs before the structured logger so the
// correlation ID is available when the access log is written; the OpenAPI
// validator runs before handlers so malformed requests never reach them.
func NewRouter(logger *slog.Logger, d Deps) (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// CORS runs early: it answers cross-origin preflights (OPTIONS) for the
	// allowlisted desktop origin before auth/validation, and never sends
	// Allow-Credentials (bearer only, cookies never cross) so the same-origin
	// cookie/CSRF posture is untouched. No-op when the allowlist is empty.
	if len(d.CORSAllowedOrigins) > 0 {
		r.Use(corsBearer(d.CORSAllowedOrigins))
	}
	r.Use(applog.Requests(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	// API responses are never cacheable: some carry one-time secrets (a device's
	// server-generated private key / .conf), and none should be stored by an
	// intermediary proxy or the browser.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, req)
		})
	})

	// Attach the authenticated principal (if any) so downstream authorization can
	// fail closed. The org used for scoping is derived from this principal's
	// memberships, never from client input. A CLI bearer credential (S5.1) is
	// tried FIRST: bearer ≡ cookie for authorization. Any invalid bearer
	// (unknown/revoked/expired) is one generic 401 (no oracle); the CLI knows
	// its own expiry from the locally-stored expires_at.
	//
	// PRECEDENCE (intended): a request carries EITHER a bearer OR a cookie in
	// practice — the CLI sends no cookie and a browser never attaches an
	// Authorization header cross-site. An invalid bearer resolves to (nil,nil)
	// and falls through to the cookie path; a VALID bearer wins outright. A stale
	// bearer is therefore never a way to assume a cookie identity. The error
	// return of BearerFn is retained for a future path that needs a distinct
	// refusal; today it is always nil.
	if d.AuthFn != nil || d.BearerFn != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if d.BearerFn != nil {
					p, err := d.BearerFn(req)
					if err != nil {
						apierr.Write(w, req, err)
						return
					}
					if p != nil {
						next.ServeHTTP(w, req.WithContext(authctx.WithPrincipal(req.Context(), p)))
						return
					}
				}
				if d.AuthFn != nil {
					if p := d.AuthFn(req); p != nil {
						req = req.WithContext(authctx.WithPrincipal(req.Context(), p))
					}
				}
				next.ServeHTTP(w, req)
			})
		})
	}

	// CSRF protection for cookie-authenticated state changes.
	r.Use(csrfGuard)

	// Validate every request against the spec; render failures as the envelope.
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	swagger.Servers = nil // don't enforce a server URL (we run behind nginx)
	r.Use(oapimw.OapiRequestValidatorWithOptions(swagger, &oapimw.Options{
		ErrorHandler: validationErrorHandler,
		Options: openapi3filter.Options{
			// The validator must NOT enforce security itself — authentication and
			// authorization are done in our handlers (authorize/requireVerifiedUser),
			// which produce the typed envelope. A noop here means "auth handled
			// elsewhere"; without it the validator would 401 even valid sessions.
			AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil },
		},
	}))

	srv := apiServer{orgs: d.Orgs, cliAuth: d.CliAuth, auth: d.Auth, members: d.Members, invites: d.Invites, nodes: d.Nodes, devices: d.Devices, sessions: d.Sessions, mfa: d.Mfa, sso: d.SSO, policy: d.Policy, accessLog: d.AccessLog, idpSync: d.IdpSync, deviceApprovalEnabled: d.DeviceApprovalEnabled, deviceHealthEnabled: d.DeviceHealthEnabled, cookieSecure: d.CookieSecure, appBaseURL: d.AppBaseURL}
	strict := api.NewStrictHandlerWithOptions(srv, nil, api.StrictHTTPServerOptions{
		// Both hooks render typed *apierr.Error (and anything else) as the envelope.
		RequestErrorHandlerFunc:  apierr.Write,
		ResponseErrorHandlerFunc: apierr.Write,
	})
	api.HandlerFromMux(strict, r)

	return r, nil
}

// validationErrorHandler renders spec-validation failures as the error envelope.
// The middleware callback lacks the request, so request_id is omitted here.
func validationErrorHandler(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "validation_failed",
			"message": message,
		},
	})
}
