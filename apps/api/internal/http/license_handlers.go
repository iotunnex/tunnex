package http

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
	"github.com/tunnexio/tunnex/apps/api/internal/licence"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// Licence install and read (S12.1 slice 6).
//
// ⛔ NO REBUILD, NO RESTART — that is the entire point of a runtime gate. The manager holds the parse in
// memory and every entitlement question re-reads it, so an installed key takes effect on the next request.
//
// ⚠ READING IS NOT OWNER-GATED. Any member may see which tier the deployment is on, because a user who
// hits a ceiling needs to understand why without having to ask an owner. INSTALLING is owner-only:
// `license:manage`, named per capability and deliberately not a reuse of `org:update` — an admin who can
// rename an org must not thereby change the commercial entitlement of the whole box.

func licenceStatusBody(st licence.Status) api.LicenseStatus {
	feats := []string{}
	for _, f := range licence.AllFeatures() {
		if licence.Has(st.Tier, f) {
			feats = append(feats, string(f))
		}
	}
	body := api.LicenseStatus{
		State:              api.LicenseStatusState(stateName(st.State)),
		Tier:               api.LicenseStatusTier(st.Tier),
		Features:           feats,
		ClockWentBackwards: &st.ClockWentBackwards,
	}
	if c, _ := licence.GatewayCeilingFor(st.Tier); c != nil {
		body.GatewayCeiling = c
	}
	if c, _ := licence.OrgCeilingFor(st.Tier); c != nil {
		body.OrgCeiling = c
	}
	if !st.ExpiresAt.IsZero() {
		e := st.ExpiresAt
		body.ExpiresAt = &e
	}
	if !st.GraceEndsAt.IsZero() {
		g := st.GraceEndsAt
		body.GraceEndsAt = &g
	}
	return body
}

func stateName(s licence.State) string {
	switch s {
	case licence.StateValid:
		return "valid"
	case licence.StateExpired:
		return "expired"
	case licence.StateLapsed:
		return "lapsed"
	default:
		return "unlicensed"
	}
}

// GetLicense reports the current entitlement.
//
// ⛔ IT ALWAYS ANSWERS. An absent licence is COMMUNITY, not an error — a deployment with no key is a
// supported, complete deployment, and a 404 here would say otherwise.
func (s apiServer) GetLicense(ctx context.Context, req api.GetLicenseRequestObject) (api.GetLicenseResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermOrgView); err != nil {
		return nil, err
	}
	st := s.licence.Evaluate(time.Now())
	return api.GetLicense200JSONResponse{
		Body:    licenceStatusBody(st),
		Headers: api.GetLicense200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

// InstallLicense verifies and installs a key.
//
// ⛔ A KEY THAT DOES NOT VERIFY IS REFUSED AND THE EXISTING ENTITLEMENT IS LEFT UNTOUCHED. A fat-fingered
// paste must never downgrade a working deployment — the manager enforces that, and the refusal says which
// half was wrong so an operator can act.
func (s apiServer) InstallLicense(ctx context.Context, req api.InstallLicenseRequestObject) (api.InstallLicenseResponseObject, error) {
	if _, err := authorize(ctx, req.OrgId, rbac.PermLicenseManage); err != nil {
		return nil, err
	}
	p, _ := authctx.PrincipalFrom(ctx)
	if req.Body == nil || req.Body.Key == "" {
		return nil, apierr.BadRequest("invalid_request", "a licence key is required")
	}

	res, err := s.licence.Install(licence.TrustedKeys, req.Body.Key)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		// ⚠ THE REASON IS NAMED AND THE REMEDY DIFFERS PER REASON. "invalid" alone sends an operator
		// looking in the wrong place — a key for another deployment and a corrupted paste need opposite
		// actions.
		return nil, apierr.BadRequest("license_rejected", licenceRefusal(res.Reason))
	}

	st := s.licence.Evaluate(time.Now())
	// ⛔ AUDITED. Installing a licence changes what the whole deployment may do; it belongs in the record
	// beside org deletion. The KEY ITSELF IS NEVER LOGGED — the licence id and tier identify it without
	// putting a credential in an audit row.
	if e := s.orgs.RecordLicenseInstall(ctx, req.OrgId, p.UserID, map[string]any{
		"license_id": res.Claims.ID,
		"tier":       string(st.Tier),
		"band":       res.Claims.Band,
		"expires_at": res.Claims.ExpiresAt,
		"kid":        res.Claims.Kid,
	}); e != nil {
		return nil, e
	}

	return api.InstallLicense200JSONResponse{
		Body:    licenceStatusBody(st),
		Headers: api.InstallLicense200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}

func licenceRefusal(r licence.Reason) string {
	switch r {
	case licence.ReasonUnknownVersion:
		return "This key was issued for a newer version of Tunnex. Upgrade, then install it again."
	case licence.ReasonUnknownKid:
		return "This key was not issued by this Tunnex. It may belong to another deployment, or it was " +
			"signed by a key this build no longer trusts."
	case licence.ReasonBadSignature:
		return "This key did not verify. It is most likely truncated — licence keys are one long line, " +
			"and some mail clients wrap them. Copy it again from the original email."
	default:
		return "This does not look like a Tunnex licence key. It should begin `tnxl_`."
	}
}
