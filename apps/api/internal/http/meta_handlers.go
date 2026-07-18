package http

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/enterprise"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// GetMeta returns public deployment metadata so the SPA can gate edition-only UI
// (hide SSO in the open build) from one bundle — no build-time web fork. SSO
// providers are advertised only when the enterprise SSO port is wired.
func (s apiServer) GetMeta(ctx context.Context, _ api.GetMetaRequestObject) (api.GetMetaResponseObject, error) {
	providers := []api.MetaSsoProviders{}
	if s.sso != nil {
		providers = []api.MetaSsoProviders{api.MetaSsoProvidersGoogle, api.MetaSsoProvidersMicrosoft}
	}
	return api.GetMeta200JSONResponse{
		Body:    api.Meta{Edition: api.MetaEdition(enterprise.Name), SsoProviders: providers, ProtocolVersion: policyspec.ProtocolVersion},
		Headers: api.GetMeta200ResponseHeaders{XRequestId: middleware.GetReqID(ctx)},
	}, nil
}
