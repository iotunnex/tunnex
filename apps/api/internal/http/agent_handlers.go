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
		// ⛔ THE KIND IS THREE-VALUED. A boolean here would force UNDETERMINED into one of the other two,
		// which is the failure the ruling exists to prevent: 'gateway' asserts a fact nobody has, 'agent'
		// repeats the defect the marker was built to fix.
		a := api.Agent{
			NodeId:        r.NodeID,
			Name:          r.Name,
			Status:        r.Status,
			EnrolmentKind: api.AgentEnrolmentKind(r.EnrolmentKind),
			// ⚠ Unattributable is derived from the OWNER, never from the email lookup — an owner who exists
			// but cannot be resolved is still attributable, and blaming the join for the fact would be a
			// second source of truth for one question.
			Unattributable: r.OwnerEmail == nil,
		}
		a.OwnerEmail = r.OwnerEmail
		a.Address = r.Address
		out = append(out, a)
	}
	return api.ListAgents200JSONResponse{Body: out, Headers: api.ListAgents200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}
