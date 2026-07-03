//go:build !enterprise

package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

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
