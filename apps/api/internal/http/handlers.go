package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// apiServer implements the generated api.StrictServerInterface. Handlers return
// typed responses on success and plain errors on failure; the strict handler's
// ResponseErrorHandlerFunc renders those errors as the standard envelope.
type apiServer struct {
	orgs *tenancy.Service
}

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

// ListOrganizations implements GET /api/v1/organizations.
func (s apiServer) ListOrganizations(ctx context.Context, _ api.ListOrganizationsRequestObject) (api.ListOrganizationsResponseObject, error) {
	orgs, err := s.orgs.ListOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Organization, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, toAPIOrg(o))
	}
	return api.ListOrganizations200JSONResponse{
		Body:    out,
		Headers: api.ListOrganizations200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// CreateOrganization implements POST /api/v1/organizations.
func (s apiServer) CreateOrganization(ctx context.Context, req api.CreateOrganizationRequestObject) (api.CreateOrganizationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	org, err := s.orgs.CreateOrganization(ctx, req.Body.Name, req.Body.Slug)
	if err != nil {
		return nil, err // rendered as the envelope by the strict error handler
	}
	return api.CreateOrganization201JSONResponse{
		Body:    toAPIOrg(org),
		Headers: api.CreateOrganization201ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

func toAPIOrg(o sqlc.Organization) api.Organization {
	return api.Organization{
		Id:        o.ID,
		Name:      o.Name,
		Slug:      o.Slug,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}
