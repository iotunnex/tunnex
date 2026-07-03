package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// authorize fails closed and permission-gates a request against orgID:
//   - no principal            -> 401 unauthenticated
//   - principal not a member  -> 404 (not 403): no cross-tenant existence leak
//   - member lacking the perm -> 403 forbidden
//
// On success it returns the org-scoped context. Call sites pass a Permission,
// never a role, so the policy stays in package rbac.
func authorize(ctx context.Context, orgID uuid.UUID, perm rbac.Permission) (context.Context, error) {
	p, ok := authctx.PrincipalFrom(ctx)
	if !ok {
		return ctx, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	role, member := p.RoleIn(orgID)
	if !member {
		return ctx, apierr.NotFound("org_not_found", "organization not found")
	}
	if !rbac.Can(role, perm) {
		return ctx, apierr.New(http.StatusForbidden, "forbidden", "you do not have permission to perform this action")
	}
	return authctx.WithOrg(ctx, orgID), nil
}

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

// GetOrganization implements GET /api/v1/organizations/{orgId}.
func (s apiServer) GetOrganization(ctx context.Context, req api.GetOrganizationRequestObject) (api.GetOrganizationResponseObject, error) {
	ctx, err := authorize(ctx, req.OrgId, rbac.PermOrgView)
	if err != nil {
		return nil, err
	}
	org, err := s.orgs.GetOrganization(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	return api.GetOrganization200JSONResponse{
		Body:    toAPIOrg(org),
		Headers: api.GetOrganization200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// UpdateOrganization implements PATCH /api/v1/organizations/{orgId}.
func (s apiServer) UpdateOrganization(ctx context.Context, req api.UpdateOrganizationRequestObject) (api.UpdateOrganizationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	ctx, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate)
	if err != nil {
		return nil, err
	}
	org, err := s.orgs.UpdateOrganization(ctx, req.OrgId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return api.UpdateOrganization200JSONResponse{
		Body:    toAPIOrg(org),
		Headers: api.UpdateOrganization200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// DeleteOrganization implements DELETE /api/v1/organizations/{orgId}.
func (s apiServer) DeleteOrganization(ctx context.Context, req api.DeleteOrganizationRequestObject) (api.DeleteOrganizationResponseObject, error) {
	ctx, err := authorize(ctx, req.OrgId, rbac.PermOrgDelete)
	if err != nil {
		return nil, err
	}
	if err := s.orgs.SoftDeleteOrganization(ctx, req.OrgId); err != nil {
		return nil, err
	}
	return api.DeleteOrganization204Response{
		Headers: api.DeleteOrganization204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
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
