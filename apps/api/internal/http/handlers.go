package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
)

// apiServer implements the generated api.StrictServerInterface. Each method is
// typed to the OpenAPI contract, so responses cannot drift from the spec.
type apiServer struct{}

// GetHealth implements GET /healthz.
func (apiServer) GetHealth(ctx context.Context, _ api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	reqID := middleware.GetReqID(ctx)
	return api.GetHealth200JSONResponse{
		Body: api.HealthResponse{
			Status:    api.Ok,
			Service:   "tunnex-api",
			RequestId: &reqID,
		},
		Headers: api.GetHealth200ResponseHeaders{XRequestId: reqID},
	}, nil
}
