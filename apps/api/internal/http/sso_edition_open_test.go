//go:build !enterprise

package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
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
func TestSSONotWiredInOpenBuild(t *testing.T) {
	if NewSSOPort(nil, nil, nil, "", slog.Default()) != nil {
		t.Fatal("open build must NOT wire an SSO port")
	}

	h, err := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Orgs: tenancy.NewService(nil)})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/v1/auth/sso/google/start?org=demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("open build SSO start status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(body, "edition_required") {
		t.Fatalf("expected edition_required envelope, got: %s", body)
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
