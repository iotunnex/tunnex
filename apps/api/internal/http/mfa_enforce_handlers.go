package http

import (
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/authctx"
)

// enrollmentGateAllow is the EXACT, minimal allowlist of operationIds a MFA-enforcement-gated user
// (org enforces + no confirmed TOTP — D8) may reach: enroll (start/confirm), sign out, and read
// their own state (me carries enrollment_required so the client can route). Everything else — incl.
// disenroll (nothing to disenroll; self-cycling) and cliAuthorize (must not birth a credential that
// outlives the gate) — is DENIED. Keyed by operationId (route identity), not path-string.
var enrollmentGateAllow = map[string]bool{
	"mfaEnrollStart":   true,
	"mfaEnrollConfirm": true,
	"logout":           true,
	"currentUser":      true,
}

// mfaEnrollmentGate is the DEFAULT-DENY authorization overlay for the D8 grandfather path (Option A):
// an authenticated user whose org enforces MFA but has no confirmed TOTP gets a gated session — full
// authentication, but authorization restricted to enrollment until they confirm. The gate is the
// entire security property, so it fails CLOSED: an operation whose identity can't be resolved is
// DENIED for a gated user, never passed through (an unregistered/renamed route can't become a bypass).
// Enterprise-only (s.mfaEnforceEnabled); the open build never engages it (D2 downgrade-release).
func (s apiServer) mfaEnrollmentGate(swagger *openapi3.T) (func(http.Handler) http.Handler, error) {
	// The spec's OWN request→operation matcher — resolves operationId directly from the request, so
	// a path rename that regenerates the spec keeps the operationId allowlist correct by construction.
	oapiRouter, err := gorillamux.NewRouter(swagger)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.mfaEnforceEnabled || s.mfa == nil {
				next.ServeHTTP(w, r) // open build / no MFA service: no gate (D2)
				return
			}
			p, ok := authctx.PrincipalFrom(r.Context())
			if !ok || p == nil {
				next.ServeHTTP(w, r) // unauthenticated → the auth layer owns the 401
				return
			}
			gated, err := s.mfa.IsEnrollmentGated(r.Context(), p.UserID)
			if err != nil {
				apierr.Write(w, r, err) // determination failed → surface, never silently pass a gated user
				return
			}
			if !gated {
				next.ServeHTTP(w, r)
				return
			}
			// GATED: fail CLOSED on anything unresolvable or not allowlisted.
			if !gateAllows(oapiRouter, r) {
				apierr.Write(w, r, apierr.New(http.StatusForbidden, "mfa_enrollment_required",
					"Set up two-factor authentication to continue."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// gateAllows resolves the request's operation identity via the spec's own matcher and returns whether
// a gated user may reach it. FAIL-CLOSED: an unresolvable route (no match / renamed / no operationId)
// returns false — it can never become a bypass. Extracted so the self-arming walk can drive it over
// every spec operation without a live server.
func gateAllows(oapiRouter routers.Router, r *http.Request) bool {
	route, _, err := oapiRouter.FindRoute(r)
	if err != nil || route == nil || route.Operation == nil {
		return false
	}
	return enrollmentGateAllow[route.Operation.OperationID]
}
