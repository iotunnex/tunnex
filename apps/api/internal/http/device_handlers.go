package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/devices"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// ListDevices GET /api/v1/organizations/{orgId}/devices. Members see their own
// devices; admins (member:manage) see all of the org's devices.
func (s apiServer) ListDevices(ctx context.Context, req api.ListDevicesRequestObject) (api.ListDevicesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	role, _ := p.RoleIn(req.OrgId)

	var devs []sqlc.Device
	var err error
	if rbac.Can(role, rbac.PermMemberManage) {
		devs, err = s.devices.ListForOrg(ctx, req.OrgId)
	} else {
		devs, err = s.devices.ListForUser(ctx, req.OrgId, p.UserID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]api.Device, 0, len(devs))
	for _, d := range devs {
		out = append(out, toAPIDevice(d))
	}
	return api.ListDevices200JSONResponse{
		Body:    out,
		Headers: api.ListDevices200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// CreateDevice POST /api/v1/organizations/{orgId}/devices. The owner is the
// session user; an admin may create on behalf of a named member. Minting a
// credential is a mutating action, so it requires a verified email.
func (s apiServer) CreateDevice(ctx context.Context, req api.CreateDeviceRequestObject) (api.CreateDeviceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if !p.EmailVerified {
		return nil, apierr.New(403, "email_not_verified", "verify your email to create a device")
	}

	owner := p.UserID
	if req.Body.UserId != nil && *req.Body.UserId != p.UserID {
		// Creating a device for someone else is an admin action.
		role, _ := p.RoleIn(req.OrgId)
		if !rbac.Can(role, rbac.PermMemberManage) {
			return nil, apierr.New(403, "forbidden", "you may not create a device for another user")
		}
		owner = *req.Body.UserId
	}

	in := devices.CreateInput{OrgID: req.OrgId, ActorID: p.UserID, OwnerID: owner, NodeID: req.Body.NodeId, Name: req.Body.Name}
	if req.Body.Platform != nil {
		in.Platform = *req.Body.Platform
	}
	if req.Body.PublicKey != nil {
		in.PublicKey = *req.Body.PublicKey
	}
	res, err := s.devices.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	body := api.CreateDeviceResponse{Device: toAPIDevice(res.Device)}
	if res.PrivateKeyOneTime != "" {
		body.PrivateKey = &res.PrivateKeyOneTime
	}
	return api.CreateDevice201JSONResponse{
		Body:    body,
		Headers: api.CreateDevice201ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// RevokeDevice POST /api/v1/organizations/{orgId}/devices/{deviceId}/revoke. A
// user may revoke their own device; an admin may revoke any.
func (s apiServer) RevokeDevice(ctx context.Context, req api.RevokeDeviceRequestObject) (api.RevokeDeviceResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	role, _ := p.RoleIn(req.OrgId)

	dev, err := s.devices.Get(ctx, req.OrgId, req.DeviceId)
	if err != nil {
		return nil, err
	}
	if dev.UserID != p.UserID && !rbac.Can(role, rbac.PermMemberManage) {
		return nil, apierr.New(403, "forbidden", "you may not revoke this device")
	}
	if err := s.devices.Revoke(ctx, req.OrgId, p.UserID, req.DeviceId); err != nil {
		return nil, err
	}
	return api.RevokeDevice204Response{
		Headers: api.RevokeDevice204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

func toAPIDevice(d sqlc.Device) api.Device {
	out := api.Device{
		Id:        d.ID,
		UserId:    d.UserID,
		NodeId:    d.NodeID,
		Name:      d.Name,
		PublicKey: d.PublicKey,
		Status:    api.DeviceStatus(d.Status),
		CreatedAt: d.CreatedAt,
	}
	if d.Platform != "" {
		out.Platform = &d.Platform
	}
	if d.AssignedIp != nil {
		out.AssignedIp = d.AssignedIp
	}
	if d.LastHandshakeAt.Valid {
		t := d.LastHandshakeAt.Time
		out.LastHandshakeAt = &t
	}
	return out
}
