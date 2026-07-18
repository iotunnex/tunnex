package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// EnrollAgent POST /api/v1/agent/enroll (public — the join token is the credential).
func (s apiServer) EnrollAgent(ctx context.Context, req api.EnrollAgentRequestObject) (api.EnrollAgentResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.BadRequest("invalid_request", "request body is required")
	}
	if req.Body.ProtocolVersion > nodes.ProtocolVersion {
		return nil, apierr.BadRequest("unsupported_protocol", "the control plane does not support this agent protocol version")
	}
	res, err := s.nodes.Enroll(ctx, req.Body.JoinToken, req.Body.Csr, req.Body.NodeName, req.Body.AgentVersion)
	if err != nil {
		return nil, err
	}
	id, _ := uuid.Parse(res.NodeID)
	return api.EnrollAgent200JSONResponse{
		Body: api.EnrollResponse{
			NodeId:        id,
			Certificate:   res.CertPEM,
			CaCertificate: res.CAPEM,
		},
		Headers: api.EnrollAgent200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// ListNodes GET /api/v1/organizations/{orgId}/nodes.
func (s apiServer) ListNodes(ctx context.Context, req api.ListNodesRequestObject) (api.ListNodesResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	ns, err := s.nodes.ListNodes(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	// Zero Trust policy health: the authoritative bool + the advisory kind from ONE org
	// compile (S7.4b fold [0] — a single snapshot so bool and kind can't disagree).
	health := s.nodes.PolicyHealthForNodes(ctx, req.OrgId, ns)
	// S8.3: the hub designation (projection of the ONE election) + the reported max policy version (CW).
	extras := s.nodes.NodeDisplayExtrasForNodes(ctx, req.OrgId, ns)
	out := make([]api.Node, 0, len(ns))
	for _, n := range ns {
		an := toAPINode(n)
		h := health[n.ID]
		an.PolicyDegraded = &h.Degraded
		k := api.NodePolicyDegradedKind(h.Kind)
		an.PolicyDegradedKind = &k
		e := extras[n.ID]
		an.IsSiteHub = &e.IsSiteHub
		if e.MaxPolicyVersion > 0 { // nullable: 0 = never reported → leave nil (the UI reads absence as below-ceiling)
			mv := e.MaxPolicyVersion
			an.MaxPolicyVersion = &mv
		}
		out = append(out, an)
	}
	return api.ListNodes200JSONResponse{
		Body:    out,
		Headers: api.ListNodes200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// IssueJoinToken POST /api/v1/organizations/{orgId}/nodes/join-token.
func (s apiServer) IssueJoinToken(ctx context.Context, req api.IssueJoinTokenRequestObject) (api.IssueJoinTokenResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	name := ""
	if req.Body != nil && req.Body.NodeName != nil {
		name = *req.Body.NodeName
	}
	tok, err := s.nodes.IssueJoinToken(ctx, p.UserID, req.OrgId, name)
	if err != nil {
		return nil, err
	}
	return api.IssueJoinToken201JSONResponse{
		Body:    api.JoinTokenResponse{JoinToken: tok},
		Headers: api.IssueJoinToken201ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// RevokeNode POST /api/v1/organizations/{orgId}/nodes/{nodeId}/revoke.
func (s apiServer) RevokeNode(ctx context.Context, req api.RevokeNodeRequestObject) (api.RevokeNodeResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgUpdate); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if err := s.nodes.Revoke(ctx, p.UserID, req.OrgId, req.NodeId); err != nil {
		return nil, err
	}
	return api.RevokeNode204Response{
		Headers: api.RevokeNode204ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

func toAPINode(n sqlc.Node) api.Node {
	out := api.Node{
		Id:           n.ID,
		Name:         n.Name,
		Status:       api.NodeStatus(n.Status),
		AgentVersion: n.AgentVersion,
		EnrolledAt:   n.EnrolledAt,
	}
	if n.LastSeenAt.Valid {
		t := n.LastSeenAt.Time
		out.LastSeenAt = &t
	}
	if n.SiteID.Valid { // S8.3 D2/CH: the site binding the topology view joins on
		sid := uuid.UUID(n.SiteID.Bytes)
		out.SiteId = &sid
	}
	return out
}
