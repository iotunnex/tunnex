package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/auth"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	"github.com/tunnexio/tunnex/apps/api/internal/invites"
	"github.com/tunnexio/tunnex/apps/api/internal/mfa"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
	"github.com/tunnexio/tunnex/apps/api/internal/sites"
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
	// Mutating actions require a verified email (S2.1 decision, enforced here).
	if rbac.IsMutating(perm) && !p.EmailVerified {
		return ctx, apierr.New(http.StatusForbidden, "email_not_verified", "verify your email to perform this action")
	}
	return authctx.WithOrg(ctx, orgID), nil
}

// requireVerifiedUser requires an authenticated, verified principal (for actions
// not scoped to an existing org, e.g. creating one). Returns the principal.
func requireVerifiedUser(ctx context.Context) (*authctx.Principal, error) {
	p, ok := authctx.PrincipalFrom(ctx)
	if !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	if !p.EmailVerified {
		return nil, apierr.New(http.StatusForbidden, "email_not_verified", "verify your email to perform this action")
	}
	return p, nil
}

// requireVerifiedSessionUser is requireVerifiedUser PLUS a proof that the
// principal is backed by a browser SESSION, not a CLI bearer credential. It
// ENFORCES the cookie-only exception argued in the spec for CLI credential
// minting (cliAuthorize / cliDeviceApprove): a bearer-built principal has no
// SessionID (only SessionAuth sets it), so minting a NEW credential from an
// existing bearer credential — self-replication that would let a stolen token
// outlive its expiry and survive revocation of the original — is refused. The
// browser session is the human checkpoint; without this the spec's
// "cookie-session only" would be documentation, not behavior.
func requireVerifiedSessionUser(ctx context.Context) (*authctx.Principal, error) {
	p, err := requireVerifiedUser(ctx)
	if err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, apierr.New(http.StatusForbidden, "session_required",
			"a browser session is required to authorize a CLI credential; a CLI credential cannot mint another")
	}
	return p, nil
}

// apiServer implements the generated api.StrictServerInterface. Handlers return
// typed responses on success and plain errors on failure; the strict handler's
// ResponseErrorHandlerFunc renders those errors as the standard envelope.
type apiServer struct {
	orgs      *tenancy.Service
	cliAuth   *cliauth.Service
	auth      *auth.Service
	members   *tenancy.MembershipService
	invites   *invites.Service
	nodes     *nodes.Service
	devices   *devices.Service
	sites     *sites.Service
	sessions  *session.Store
	mfa       *mfa.Service  // OPEN (all editions): TOTP enrollment + login challenge (S7.5.5)
	sso       ssoPort       // nil in the open build
	policy    policyPort    // nil in the open build (Zero Trust, S7.1)
	accessLog accessLogPort // nil in the open build (Zero Trust visibility, S7.5.1)
	idpSync   idpSyncPort   // nil in the open build (IdP-group sync, S7.5.2)
	// deviceApprovalEnabled gates device posture (S7.3). NAMED per-feature (its own
	// wire files), not a proxy behind s.policy — device posture and Zero Trust policy
	// are distinct enterprise features (F2 / ledgered S12.1 refactor).
	deviceApprovalEnabled bool
	// deviceHealthEnabled gates device health / posture checks (S7.5.3) — its own
	// named per-feature edition bool (approval ≠ health: orthogonal capabilities).
	deviceHealthEnabled bool
	// mfaEnforceEnabled gates org-level MFA ENFORCE (S7.5.5) — enterprise only. Enrollment is
	// OPEN (s.mfa, all editions); only the enforce toggle + admin-reset + the enrollment gate
	// are enterprise. In the open build this is false → enforcement releases (D2 downgrade).
	mfaEnforceEnabled bool
	cookieSecure      bool
	appBaseURL        string
	nodeAgentImage    string
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

// ListOrganizations implements GET /api/v1/organizations — scoped to the
// caller's memberships (never all orgs).
func (s apiServer) ListOrganizations(ctx context.Context, _ api.ListOrganizationsRequestObject) (api.ListOrganizationsResponseObject, error) {
	p, ok := authctx.PrincipalFrom(ctx)
	if !ok {
		return nil, apierr.New(http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	orgs, err := s.orgs.ListOrganizationsForUser(ctx, p.UserID)
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
	p, err := requireVerifiedUser(ctx)
	if err != nil {
		return nil, err
	}
	org, err := s.orgs.CreateOrganization(ctx, p.UserID, req.Body.Name, req.Body.Slug)
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
		PoolCidr:  o.PoolCidr,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}
