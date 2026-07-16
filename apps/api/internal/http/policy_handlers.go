package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// policyPort is the enterprise Zero Trust policy capability. It is nil in the
// open build, so every handler returns the edition_required envelope (same shape
// as the org cap / SSO). It returns sqlc rows; the handlers map them to API types.
type policyPort interface {
	ListGroups(ctx context.Context, orgID uuid.UUID) ([]sqlc.UserGroup, error)
	CreateGroup(ctx context.Context, orgID uuid.UUID, name, description string) (sqlc.UserGroup, error)
	UpdateGroup(ctx context.Context, orgID, groupID uuid.UUID, name, description string) (sqlc.UserGroup, error)
	DeleteGroup(ctx context.Context, orgID, groupID uuid.UUID) error
	ListGroupMembers(ctx context.Context, orgID, groupID uuid.UUID) ([]sqlc.ListGroupMembersRow, error)
	AddGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	RemoveGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	ListResources(ctx context.Context, orgID uuid.UUID) ([]sqlc.Resource, error)
	CreateResource(ctx context.Context, orgID uuid.UUID, in policyspec.ResourceInput) (sqlc.Resource, error)
	UpdateResource(ctx context.Context, orgID, resourceID uuid.UUID, in policyspec.ResourceInput) (sqlc.Resource, error)
	DeleteResource(ctx context.Context, orgID, resourceID uuid.UUID) error
	ListPolicyRules(ctx context.Context, orgID uuid.UUID) ([]sqlc.PolicyRule, error)
	CreatePolicyRule(ctx context.Context, orgID uuid.UUID, in policyspec.RuleInput) (sqlc.PolicyRule, error)
	DeletePolicyRule(ctx context.Context, orgID, ruleID uuid.UUID) error
	ExtendGrant(ctx context.Context, orgID, ruleID uuid.UUID, newExpiresAt time.Time) (sqlc.PolicyRule, error)
	GetMode(ctx context.Context, orgID uuid.UUID) (string, error)
	SetMode(ctx context.Context, orgID uuid.UUID, mode string) (mode_ string, affected []policyspec.AffectedDevice, err error)
}

// EgressPolicyDenied is the NAMED state for a device whose internet egress is denied
// by Zero Trust POLICY on a gateway that IS egress-capable (S7.2 decision 2-coherence)
// -- deliberately distinct from gateway_no_egress (the gateway cannot egress at all).
// The two refusal paths must never be conflated in status/error surfaces.
const EgressPolicyDenied = "egress_policy_denied"

func policyEditionRequired() error {
	return apierr.New(http.StatusForbidden, "edition_required", "Zero Trust policy is a Tunnex Enterprise feature")
}

// reqID is a short alias for the request-id header value.
func reqID(ctx context.Context) string { return middleware.GetReqID(ctx) }

// ── groups ──────────────────────────────────────────────────────────────────────

func (s apiServer) ListGroups(ctx context.Context, req api.ListGroupsRequestObject) (api.ListGroupsResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	gs, err := s.policy.ListGroups(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.UserGroup, 0, len(gs))
	for _, g := range gs {
		out = append(out, toAPIGroup(g))
	}
	return api.ListGroups200JSONResponse{Body: out, Headers: api.ListGroups200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) CreateGroup(ctx context.Context, req api.CreateGroupRequestObject) (api.CreateGroupResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	g, err := s.policy.CreateGroup(ctx, req.OrgId, req.Body.Name, deref(req.Body.Description))
	if err != nil {
		return nil, err
	}
	return api.CreateGroup201JSONResponse{Body: toAPIGroup(g), Headers: api.CreateGroup201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) UpdateGroup(ctx context.Context, req api.UpdateGroupRequestObject) (api.UpdateGroupResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	g, err := s.policy.UpdateGroup(ctx, req.OrgId, req.GroupId, req.Body.Name, deref(req.Body.Description))
	if err != nil {
		return nil, err
	}
	return api.UpdateGroup200JSONResponse{Body: toAPIGroup(g), Headers: api.UpdateGroup200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) DeleteGroup(ctx context.Context, req api.DeleteGroupRequestObject) (api.DeleteGroupResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if err := s.policy.DeleteGroup(ctx, req.OrgId, req.GroupId); err != nil {
		return nil, err
	}
	return api.DeleteGroup204Response{Headers: api.DeleteGroup204ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ── group members ───────────────────────────────────────────────────────────────

func (s apiServer) ListGroupMembers(ctx context.Context, req api.ListGroupMembersRequestObject) (api.ListGroupMembersResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	ms, err := s.policy.ListGroupMembers(ctx, req.OrgId, req.GroupId)
	if err != nil {
		return nil, err
	}
	out := make([]api.GroupMember, 0, len(ms))
	for _, m := range ms {
		out = append(out, api.GroupMember{UserId: m.ID, Email: m.Email, Name: m.Name, AddedAt: m.CreatedAt})
	}
	return api.ListGroupMembers200JSONResponse{Body: out, Headers: api.ListGroupMembers200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) AddGroupMember(ctx context.Context, req api.AddGroupMemberRequestObject) (api.AddGroupMemberResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if err := s.policy.AddGroupMember(ctx, req.OrgId, req.GroupId, req.Body.UserId); err != nil {
		return nil, err
	}
	return api.AddGroupMember204Response{Headers: api.AddGroupMember204ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) RemoveGroupMember(ctx context.Context, req api.RemoveGroupMemberRequestObject) (api.RemoveGroupMemberResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if err := s.policy.RemoveGroupMember(ctx, req.OrgId, req.GroupId, req.UserId); err != nil {
		return nil, err
	}
	return api.RemoveGroupMember204Response{Headers: api.RemoveGroupMember204ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ── resources ───────────────────────────────────────────────────────────────────

func (s apiServer) ListResources(ctx context.Context, req api.ListResourcesRequestObject) (api.ListResourcesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	rs, err := s.policy.ListResources(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.Resource, 0, len(rs))
	for _, r := range rs {
		out = append(out, toAPIResource(r))
	}
	return api.ListResources200JSONResponse{Body: out, Headers: api.ListResources200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) CreateResource(ctx context.Context, req api.CreateResourceRequestObject) (api.CreateResourceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	r, err := s.policy.CreateResource(ctx, req.OrgId, resourceInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return api.CreateResource201JSONResponse{Body: toAPIResource(r), Headers: api.CreateResource201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) UpdateResource(ctx context.Context, req api.UpdateResourceRequestObject) (api.UpdateResourceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	r, err := s.policy.UpdateResource(ctx, req.OrgId, req.ResourceId, resourceInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return api.UpdateResource200JSONResponse{Body: toAPIResource(r), Headers: api.UpdateResource200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) DeleteResource(ctx context.Context, req api.DeleteResourceRequestObject) (api.DeleteResourceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if err := s.policy.DeleteResource(ctx, req.OrgId, req.ResourceId); err != nil {
		return nil, err
	}
	return api.DeleteResource204Response{Headers: api.DeleteResource204ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ── rules ───────────────────────────────────────────────────────────────────────

func (s apiServer) ListPolicyRules(ctx context.Context, req api.ListPolicyRulesRequestObject) (api.ListPolicyRulesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	rs, err := s.policy.ListPolicyRules(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.PolicyRule, 0, len(rs))
	for _, r := range rs {
		out = append(out, toAPIRule(r))
	}
	return api.ListPolicyRules200JSONResponse{Body: out, Headers: api.ListPolicyRules200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) CreatePolicyRule(ctx context.Context, req api.CreatePolicyRuleRequestObject) (api.CreatePolicyRuleResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	in := policyspec.RuleInput{
		SrcUserID:     req.Body.SrcUserId,
		DstKind:       string(req.Body.DstKind),
		DstResourceID: req.Body.DstResourceId,
		DstGroupID:    req.Body.DstGroupId,
		ExpiresAt:     req.Body.ExpiresAt,
	}
	if req.Body.SrcKind != nil {
		in.SrcKind = string(*req.Body.SrcKind)
	}
	if req.Body.SrcGroupId != nil {
		in.SrcGroupID = *req.Body.SrcGroupId
	}
	r, err := s.policy.CreatePolicyRule(ctx, req.OrgId, in)
	if err != nil {
		return nil, err
	}
	return api.CreatePolicyRule201JSONResponse{Body: toAPIRule(r), Headers: api.CreatePolicyRule201ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) DeletePolicyRule(ctx context.Context, req api.DeletePolicyRuleRequestObject) (api.DeletePolicyRuleResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if err := s.policy.DeletePolicyRule(ctx, req.OrgId, req.RuleId); err != nil {
		return nil, err
	}
	return api.DeletePolicyRule204Response{Headers: api.DeletePolicyRule204ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ExtendGrant PUT .../policies/{ruleId} — extend a temporary grant's window (S7.5.4).
func (s apiServer) ExtendGrant(ctx context.Context, req api.ExtendGrantRequestObject) (api.ExtendGrantResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	r, err := s.policy.ExtendGrant(ctx, req.OrgId, req.RuleId, req.Body.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return api.ExtendGrant200JSONResponse{Body: toAPIRule(r), Headers: api.ExtendGrant200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

// ── enforcement mode ──────────────────────────────────────────────────────────

func (s apiServer) GetZeroTrustMode(ctx context.Context, req api.GetZeroTrustModeRequestObject) (api.GetZeroTrustModeResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	mode, err := s.policy.GetMode(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	return api.GetZeroTrustMode200JSONResponse{
		Body:    api.ZeroTrustMode{Mode: api.ZeroTrustModeMode(mode)},
		Headers: api.GetZeroTrustMode200ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

// SetZeroTrustMode gates on PermPolicyManage — DISABLING re-opens the mesh, so it
// is the same (owner/admin) capability, deliberately not a members-level read.
func (s apiServer) SetZeroTrustMode(ctx context.Context, req api.SetZeroTrustModeRequestObject) (api.SetZeroTrustModeResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermPolicyManage); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	mode, affected, err := s.policy.SetMode(ctx, req.OrgId, string(req.Body.Mode))
	if err != nil {
		return nil, err
	}
	body := api.ZeroTrustMode{Mode: api.ZeroTrustModeMode(mode)}
	if len(affected) > 0 {
		out := make([]api.AffectedDevice, 0, len(affected))
		for _, d := range affected {
			out = append(out, api.AffectedDevice{Id: d.ID, Name: d.Name})
		}
		body.AffectedFullTunnelDevices = &out
	}
	return api.SetZeroTrustMode200JSONResponse{
		Body:    body,
		Headers: api.SetZeroTrustMode200ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

// ── mappers ─────────────────────────────────────────────────────────────────────

func toAPIGroup(g sqlc.UserGroup) api.UserGroup {
	out := api.UserGroup{Id: g.ID, OrgId: g.OrgID, Name: g.Name, Description: g.Description, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
	if g.Origin != "" { // S7.5.2: distinguish a directory-synced group from a manual one
		o := api.UserGroupOrigin(g.Origin)
		out.Origin = &o
	}
	if g.IdpProvider != nil {
		p := api.UserGroupIdpProvider(*g.IdpProvider)
		out.IdpProvider = &p
	}
	out.IdpGroupId = g.IdpGroupID
	return out
}

func toAPIResource(r sqlc.Resource) api.Resource {
	out := api.Resource{
		Id: r.ID, OrgId: r.OrgID, Name: r.Name, Cidr: r.Cidr,
		Protocol: api.ResourceProtocol(r.Protocol), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		PortLow: i32toIntPtr(r.PortLow), PortHigh: i32toIntPtr(r.PortHigh),
	}
	return out
}

func toAPIRule(r sqlc.PolicyRule) api.PolicyRule {
	out := api.PolicyRule{
		Id: r.ID, OrgId: r.OrgID, SrcKind: api.PolicyRuleSrcKind(r.SrcKind),
		DstKind: api.PolicyRuleDstKind(r.DstKind), CreatedAt: r.CreatedAt,
	}
	if r.SrcGroupID.Valid {
		u := uuid.UUID(r.SrcGroupID.Bytes)
		out.SrcGroupId = &u
	}
	if r.SrcUserID.Valid {
		u := uuid.UUID(r.SrcUserID.Bytes)
		out.SrcUserId = &u
	}
	if r.DstResourceID.Valid {
		u := uuid.UUID(r.DstResourceID.Bytes)
		out.DstResourceId = &u
	}
	if r.DstGroupID.Valid {
		u := uuid.UUID(r.DstGroupID.Bytes)
		out.DstGroupId = &u
	}
	if r.ExpiresAt.Valid {
		t := r.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	return out
}

func resourceInput(b api.ResourceRequest) policyspec.ResourceInput {
	return policyspec.ResourceInput{Name: b.Name, CIDR: b.Cidr, Protocol: string(b.Protocol), PortLow: b.PortLow, PortHigh: b.PortHigh}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func i32toIntPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
