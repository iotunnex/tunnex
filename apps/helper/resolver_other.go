//go:build !darwin

package helper

// setResolvers on non-macOS platforms is not implemented in v1. Windows domain-scoped
// resolvers (NRPT) are S8.4b, triggered by S8.5's device-routes slice. Until then a
// set_resolvers call is refused with a stable code; the app fail-STATIC (tunnel stays
// up, cross-site names just don't resolve) exactly as it does against an old helper.
func setResolvers(_ []ResolverForward) error {
	return &ProtocolError{Code: "resolvers_unsupported", Msg: "domain-scoped resolvers are not supported on this platform"}
}
