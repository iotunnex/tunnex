package http

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// Machine-credential endpoints (S10.2 Slice 1) — the GitOps operator's org identity. machine:manage is
// OWNER-ONLY (minting a non-human actor that can rewrite access policy is org-delete-grade). The mint is a
// one-time-secret ceremony: the token is returned ONCE and never re-displayed (revoke + re-mint if lost);
// the list + audit carry the keyed fingerprint only.

func (s apiServer) ListMachineCredentials(ctx context.Context, req api.ListMachineCredentialsRequestObject) (api.ListMachineCredentialsResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMachineManage); err != nil {
		return nil, err
	}
	rows, err := s.machine.List(ctx, req.OrgId)
	if err != nil {
		return nil, err
	}
	out := make([]api.MachineCredential, len(rows))
	for i, c := range rows {
		out[i] = api.MachineCredential{Id: c.ID, Name: c.Name, Fingerprint: c.Fingerprint, CreatedAt: c.CreatedAt, LastUsedAt: timePtr(c.LastUsedAt)}
	}
	return api.ListMachineCredentials200JSONResponse{Body: out, Headers: api.ListMachineCredentials200ResponseHeaders{XRequestId: reqID(ctx)}}, nil
}

func (s apiServer) MintMachineCredential(ctx context.Context, req api.MintMachineCredentialRequestObject) (api.MintMachineCredentialResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMachineManage); err != nil {
		return nil, err
	}
	if req.Body == nil || strings.TrimSpace(req.Body.Name) == "" {
		return nil, apierr.BadRequest("invalid_request", "a name is required — it appears in the audit trail as operator:<name>")
	}
	p, _ := authctx.PrincipalFrom(ctx)
	cred, err := s.machine.Mint(ctx, req.OrgId, p.UserID, strings.TrimSpace(req.Body.Name))
	if err != nil {
		return nil, err
	}
	// The token rides the 201 body ONCE — the response is no-store (router), and no other endpoint re-serves
	// it (List returns the fingerprint only). Loss path: revoke + re-mint.
	return api.MintMachineCredential201JSONResponse{
		Body:    api.MintMachineCredentialResponse{Id: cred.ID, Name: cred.Name, Fingerprint: cred.Fingerprint, Token: cred.Token},
		Headers: api.MintMachineCredential201ResponseHeaders{XRequestId: reqID(ctx)},
	}, nil
}

func (s apiServer) RevokeMachineCredential(ctx context.Context, req api.RevokeMachineCredentialRequestObject) (api.RevokeMachineCredentialResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermMachineManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	// Idempotent + org-scoped in the service: an unknown/other-org/already-revoked id is a no-op (204), never
	// a leak. Revocation severs on the credential's very next request (the auth path re-reads the row).
	if _, err := s.machine.Revoke(ctx, req.OrgId, p.UserID, req.CredentialId); err != nil {
		return nil, err
	}
	return api.RevokeMachineCredential204Response{}, nil
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
