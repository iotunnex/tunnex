// Package http wires the API's HTTP routes and middleware.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	applog "github.com/tunnexio/tunnex/apps/api/internal/log"
)

// NewRouter builds the API router with the standard middleware chain.
//
// Middleware order matters: RequestID must run before the structured request
// logger so the correlation ID is available when the access log is written.
func NewRouter(logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(applog.Requests(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Liveness — no external dependencies (see health.go).
	r.Get("/healthz", handleHealth)

	// Versioned API surface; endpoints land here from EPIC 1 onward.
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", handleHealth)
	})

	return r
}
