// Package http wires the API's HTTP routes and middleware. Routes are mounted
// from the generated OpenAPI server (internal/api) so the wire contract is the
// spec, not hand-written handlers.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	oapimw "github.com/oapi-codegen/nethttp-middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
)

// NewRouter builds the API router with the standard middleware chain and mounts
// the generated OpenAPI handlers.
//
// Middleware order matters: RequestID runs before the structured logger so the
// correlation ID is available when the access log is written; the OpenAPI
// validator runs before handlers so malformed requests never reach them.
func NewRouter(logger *slog.Logger) (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(applog.Requests(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Validate every request against the spec (precedent for all S1.2+ endpoints).
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	swagger.Servers = nil // don't enforce a server URL (we run behind nginx)
	r.Use(oapimw.OapiRequestValidator(swagger))

	strict := api.NewStrictHandler(apiServer{}, nil)
	api.HandlerFromMux(strict, r)

	return r, nil
}
