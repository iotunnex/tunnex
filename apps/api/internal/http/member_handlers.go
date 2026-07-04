package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

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
	if _, _, err := s.invites.Accept(ctx, req.Body.Token, name, pw); err != nil {
		return nil, err
	}
	return api.AcceptInvitation200JSONResponse{
		Body:    api.GenericMessage{Message: "Invitation accepted — you can now sign in."},
		Headers: api.AcceptInvitation200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
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
