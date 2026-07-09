package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
)

// ListMembers GET /api/v1/organizations/{orgId}/members — the org roster
// (incl. deactivated members). Any member may list (PermMemberList).
func (s apiServer) ListMembers(ctx context.Context, req api.ListMembersRequestObject) (api.ListMembersResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberList); err != nil {
		return nil, err
	}
	rows, err := s.members.ListMembersWithUser(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPIMember(r))
	}
	return api.ListMembers200JSONResponse{
		Body:    out,
		Headers: api.ListMembers200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

func toAPIMember(r sqlc.ListOrgMembersWithUserRow) api.Member {
	return api.Member{
		UserId:        r.UserID,
		Email:         openapi_types.Email(r.Email),
		Name:          r.Name,
		Role:          api.MemberRole(r.Role),
		Status:        api.MemberStatus(r.Status),
		EmailVerified: r.EmailVerified,
		JoinedAt:      r.JoinedAt,
	}
}

// ChangeMemberRole PUT /api/v1/organizations/{orgId}/members/{userId}/role.
// Gated on PermMemberManage; the service applies the RBAC relational rules
// (only an owner manages/creates owners) and the last-owner invariant.
func (s apiServer) ChangeMemberRole(ctx context.Context, req api.ChangeMemberRoleRequestObject) (api.ChangeMemberRoleResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	actorRole, _ := p.RoleIn(req.OrgId)
	if _, err := s.members.ChangeMemberRole(ctx, &p.UserID, actorRole, req.OrgId, req.UserId, string(req.Body.Role)); err != nil {
		return nil, err
	}
	return api.ChangeMemberRole204Response{
		Headers: api.ChangeMemberRole204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// CreateInvitation POST /api/v1/organizations/{orgId}/invitations.
func (s apiServer) CreateInvitation(ctx context.Context, req api.CreateInvitationRequestObject) (api.CreateInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberInvite); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.invites.Create(ctx, p.UserID, req.OrgId, string(req.Body.Email), string(req.Body.Role)); err != nil {
		return nil, err
	}
	return api.CreateInvitation202JSONResponse{
		Body:    api.GenericMessage{Message: "Invitation sent."},
		Headers: api.CreateInvitation202ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// AcceptInvitation POST /api/v1/auth/invitations/accept (public).
func (s apiServer) AcceptInvitation(ctx context.Context, req api.AcceptInvitationRequestObject) (api.AcceptInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	name, pw := "", ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	if req.Body.Password != nil {
		pw = *req.Body.Password
	}
	userID, _, err := s.invites.Accept(ctx, req.Body.Token, name, pw)
	if err != nil {
		return nil, err
	}
	// Auto-login: accepting the invite proves inbox control (and, for a new user,
	// sets a password), so establish a fresh session here — the user lands directly
	// in their new org instead of being bounced to a second sign-in (which, with no
	// org yet, would misroute them into create-org onboarding). Same session-mint +
	// cookie path as Login; csrfGuard is a no-op here (no prior session cookie).
	sess, err := s.sessions.Create(ctx, userID)
	if err != nil {
		return nil, err
	}
	return acceptInviteResponse{
		body:      api.GenericMessage{Message: "Invitation accepted."},
		sess:      sess,
		secure:    s.cookieSecure,
		requestID: middleware.GetReqID(ctx),
	}, nil
}

// acceptInviteResponse sets the session cookie on a successful invite accept
// (auto-login), mirroring loginResponse.
type acceptInviteResponse struct {
	body      api.GenericMessage
	sess      session.Session
	secure    bool
	requestID string
}

func (r acceptInviteResponse) VisitAcceptInvitationResponse(w http.ResponseWriter) error {
	session.SetCookie(w, r.sess, r.secure)
	w.Header().Set("X-Request-Id", r.requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.body)
}

// ResendInvitation POST /api/v1/organizations/{orgId}/invitations/resend.
func (s apiServer) ResendInvitation(ctx context.Context, req api.ResendInvitationRequestObject) (api.ResendInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberInvite); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.invites.Resend(ctx, p.UserID, req.OrgId, string(req.Body.Email)); err != nil {
		return nil, err
	}
	return api.ResendInvitation202JSONResponse{
		Body:    api.GenericMessage{Message: "Invitation re-sent."},
		Headers: api.ResendInvitation202ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// RevokeInvitation POST /api/v1/organizations/{orgId}/invitations/revoke.
func (s apiServer) RevokeInvitation(ctx context.Context, req api.RevokeInvitationRequestObject) (api.RevokeInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberInvite); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.invites.Revoke(ctx, p.UserID, req.OrgId, string(req.Body.Email)); err != nil {
		return nil, err
	}
	return api.RevokeInvitation204Response{
		Headers: api.RevokeInvitation204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// DeactivateMember POST /api/v1/organizations/{orgId}/members/{userId}/deactivate.
func (s apiServer) DeactivateMember(ctx context.Context, req api.DeactivateMemberRequestObject) (api.DeactivateMemberResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.members.DeactivateMember(ctx, p.UserID, req.OrgId, req.UserId); err != nil {
		return nil, err
	}
	return api.DeactivateMember204Response{
		Headers: api.DeactivateMember204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// ReactivateMember POST /api/v1/organizations/{orgId}/members/{userId}/reactivate.
func (s apiServer) ReactivateMember(ctx context.Context, req api.ReactivateMemberRequestObject) (api.ReactivateMemberResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMemberManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.members.ReactivateMember(ctx, p.UserID, req.OrgId, req.UserId); err != nil {
		return nil, err
	}
	return api.ReactivateMember204Response{
		Headers: api.ReactivateMember204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
