package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// TransferNodeDevices POST /api/v1/organizations/{orgId}/nodes/{nodeId}/transfer-devices (S12.12 D1).
//
// THE STEP THE REVOKE REFUSAL SENDS AN OPERATOR TO. Revoking a gateway cascade-revokes every device homed on
// it, permanently — so the revoke now refuses while any remain (409 devices_still_homed) and this is the only
// way past it. The refusal without this endpoint would be a dead end; this endpoint without the refusal would
// be an optional step nobody takes before the destructive one. Neither half is useful alone, which is why
// they ship together.
//
// device:transfer rather than device:restore or org:update: a new capability never rides in on an existing
// permission, and this one moves LIVE users between gateways — which, across sites, changes which policy
// rules apply to them (D5). Granting the power to retire a gateway must not silently grant that.
func (s apiServer) TransferNodeDevices(ctx context.Context, req api.TransferNodeDevicesRequestObject) (api.TransferNodeDevicesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermDeviceTransfer); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	p, _ := authctx.PrincipalFrom(ctx)
	res, err := s.devices.TransferDevicesToNode(ctx, p.UserID, req.OrgId, req.NodeId, req.Body.TargetNodeId)
	if err != nil {
		// ErrTransferSourceUnknown and ErrTransferTargetUnusable are already *apierr.Error carrying their own
		// codes, so they travel as themselves rather than being flattened into one generic 400 here. The UI
		// needs to tell "you named a gateway that isn't yours" from "you named one that cannot host them",
		// because only the second is a mistake the operator fixes by picking again.
		return nil, err
	}

	// api.TransferDevicesResult — the schema is TransferNodeDevicesResponse but carries x-go-name, because the
	// bare name collides with the wrapper oapi-codegen derives for operationId `transferNodeDevices`.
	body := api.TransferDevicesResult{Moved: len(res)}
	for _, r := range res {
		if r.NeedsReissue {
			body.NeedsReissue++
		}
		entry := struct {
			Id           openapi_types.UUID                                 `json:"id"`
			Name         string                                             `json:"name"`
			NeedsReissue bool                                               `json:"needs_reissue"`
			ReissueCause *api.TransferNodeDevicesResponseDevicesReissueCause `json:"reissue_cause,omitempty"`
		}{Id: r.DeviceID, Name: r.Name, NeedsReissue: r.NeedsReissue}
		if r.ReissueCause != "" {
			cause := api.TransferNodeDevicesResponseDevicesReissueCause(r.ReissueCause)
			entry.ReissueCause = &cause
		}
		body.Devices = append(body.Devices, entry)
	}
	return api.TransferNodeDevices200JSONResponse{
		Body:    body,
		Headers: api.TransferNodeDevices200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
