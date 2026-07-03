// Package http wires the API's HTTP routes and middleware. Routes are mounted
// from the generated OpenAPI server (internal/api) so the wire contract is the
// spec, not hand-written handlers.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	oapimw "github.com/oapi-codegen/nethttp-middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// NewRouter builds the API router with the standard middleware chain and mounts
// the generated OpenAPI handlers.
//
// Middleware order matters: RequestID runs before the structured logger so the
// correlation ID is available when the access log is written; the OpenAPI
// validator runs before handlers so malformed requests never reach them.
func NewRouter(logger *slog.Logger, orgs *tenancy.Service) (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(applog.Requests(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Validate every request against the spec; render failures as the envelope.
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	swagger.Servers = nil // don't enforce a server URL (we run behind nginx)
	r.Use(oapimw.OapiRequestValidatorWithOptions(swagger, &oapimw.Options{
		ErrorHandler: validationErrorHandler,
	}))

	srv := apiServer{orgs: orgs}
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
