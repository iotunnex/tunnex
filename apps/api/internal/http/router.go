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
	"github.com/tunnexio/tunnex/apps/api/internal/invites"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// AuthFunc resolves the authenticated principal for a request, or nil if the
// request is unauthenticated. SessionAuth is the session-backed implementation.
type AuthFunc func(r *http.Request) *authctx.Principal

// Deps are the router's dependencies.
type Deps struct {
	Orgs         *tenancy.Service
	Auth         *auth.Service
	Members      *tenancy.MembershipService
	Invites      *invites.Service
	Sessions     *session.Store
	SSO          ssoPort // nil => open build (SSO endpoints return edition_required)
	CookieSecure bool
	AppBaseURL   string
	AuthFn       AuthFunc
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
	r.Use(applog.Requests(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Attach the authenticated principal (if any) so downstream authorization can
	// fail closed. The org used for scoping is derived from this principal's
	// memberships, never from client input.
	if d.AuthFn != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if p := d.AuthFn(req); p != nil {
					req = req.WithContext(authctx.WithPrincipal(req.Context(), p))
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

	srv := apiServer{orgs: d.Orgs, auth: d.Auth, members: d.Members, invites: d.Invites, sessions: d.Sessions, sso: d.SSO, cookieSecure: d.CookieSecure, appBaseURL: d.AppBaseURL}
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
