package http

import (
	"context"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// ListAgents GET /api/v1/organizations/{orgId}/agents — S15.3.
//
// ⛔ PERMISSION BEFORE EDITION, and the order is enforced by `TestEditionGateNeverPrecedesPermissionGate`
// (which harvests gate-helper names from source, so a new helper cannot slip past). Checking the edition
// first would tell an unauthorized caller which editions a feature belongs to — an edition oracle, answered
// to someone who was never entitled to ask.
//
// ⚠ AND `403 edition_required` IS A SUCCESSFUL REFUSAL, NOT A FAILURE. The open edition must render ABSENCE
// — no section, no styled-away control, no error. Folding this into the failed state would show "could not
// load" for a server that answered correctly, which is the load-failed/none confusion under a new name.
func (s apiServer) ListAgents(ctx context.Context, req api.ListAgentsRequestObject) (api.ListAgentsResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	if s.policy == nil {
		return nil, policyEditionRequired()
	}
	rows, err := s.nodes.ListAgents(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.Agent, 0, len(rows))
	for _, r := range rows {
		a := api.Agent{
			DeviceId:       r.DeviceID,
			Name:           r.Name,
			NodeId:         &r.NodeID,
			GatewayName:    r.GatewayName,
			Status:         r.Status,
			Unattributable: r.OwnerEmail == nil,
		}
		a.OwnerEmail = r.OwnerEmail
		a.Address = r.Address

		// ⛔ `config_issued` IS NOT LIVENESS AND NO LONGER PRETENDS TO BE. It was named `connected` and
		// computed exactly this way, so an agent that had never once handshaked reported connected: the
		// field described the row's shape and was read as a statement about the network.
		configIssued := r.PublicKey != ""
		a.ConfigIssued = &configIssued

		if r.LastHandshakeAt.Valid {
			hs := r.LastHandshakeAt.Time
			a.LastHandshakeAt = &hs
		}
		a.RxBytes = r.RxBytes
		a.TxBytes = r.TxBytes

		// ⛔ THE SAME HELPER AND THEREFORE THE SAME WINDOW AS A HUMAN DEVICE. An agent is a peer; if its
		// online-ness were derived here against a locally-chosen threshold, the two surfaces would disagree
		// about the same handshake and neither would be wrong on its own terms.
		online := deviceOnline(a.LastHandshakeAt)
		a.Online = &online

		// ⛔ AND WHETHER THE REPORTER ITSELF IS ALIVE, because a silent gateway and a dead agent produce an
		// IDENTICAL absence of handshakes. The agent has no control-plane channel of its own — it runs
		// wg-quick — so the gateway's status push is the only thing that can ever say an agent is up.
		// Without this field the UI would render a confident "offline" about an agent it has no information
		// on, which is the three-states-one-appearance failure the EPIC 15 walk was nearly ruined by.
		gwReporting := r.GatewayLastSeenAt.Valid && deviceOnline(&r.GatewayLastSeenAt.Time)
		a.GatewayReporting = &gwReporting

		out = append(out, a)
	}
	return api.ListAgents200JSONResponse{Body: out, Headers: api.ListAgents200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}
