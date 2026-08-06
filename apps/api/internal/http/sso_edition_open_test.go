package http

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// TestGetSsoConfigEditionGatedInOpenBuild proves the SSO-config READ endpoint is
// edition-enforced SERVER-side in the open build (not merely hidden in the UI):
// an authenticated, authorized owner still gets 403 edition_required because the
// SSO port is nil. authorize() runs first (so a sessionless request 401s — the
// spec walk stays honest); the edition gate fires for authenticated callers.
func TestGetSsoConfigEditionGatedInOpenBuild(t *testing.T) {
	s := apiServer{} // open build: sso port is nil
	org := uuid.New()
	ctx := principalWithRole(org, rbac.RoleOwner) // authed + verified owner
	_, err := s.GetSsoConfig(ctx, api.GetSsoConfigRequestObject{OrgId: org, Provider: "google"})
	if !hasCode(err, 403, "edition_required") {
		t.Fatalf("open-build GetSsoConfig: want 403 edition_required, got %v", err)
	}
}

// Open build: SSO is not wired, and the SSO endpoints return the edition_required
// envelope (not a missing route or a crash).
// ⛔ REVERSED (S12.1), AND THIS ONE LEAVES A GAP ON PURPOSE. It asserted the open build wired no SSO port.
// With one binary the port is wired for everyone, and SSO is a PAID gate — so until the LicenseManager
// slice reads a licence, SSO IS AVAILABLE TO EVERY DEPLOYMENT.
//
// ⚠ That is why this asserts the port is wired AND names the gap: see TestPaidCapabilitiesAreNotYetEnforced.
// DO NOT RELEASE between this slice and the LicenseManager slice.
func TestSSOPortIsWiredAndNotYetGated(t *testing.T) {
	if NewSSOPort(nil, nil, nil, "", slog.Default()) == nil {
		t.Fatal("the SSO port must be wired in the single binary")
	}
}

func contains(h []byte, s string) bool {
	for i := 0; i+len(s) <= len(h); i++ {
		if string(h[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
