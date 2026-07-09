package http

import (
	"net/http/httptest"
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/session"
)

// TestAcceptInviteResponseAutoLogin proves the invite-accept response establishes a
// session (auto-login). Without this Set-Cookie the invited user lands
// UNAUTHENTICATED after accepting and — having no org yet — gets bounced into
// create-org onboarding instead of their new org. That was the reported bug; this
// is its regression guard at the response layer (the service Accept is covered by
// the invites package tests).
func TestAcceptInviteResponseAutoLogin(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := acceptInviteResponse{
		body:      api.GenericMessage{Message: "Invitation accepted."},
		sess:      session.Session{ID: "sess-abc"},
		secure:    true,
		requestID: "req-1",
	}
	if err := resp.VisitAcceptInvitationResponse(rec); err != nil {
		t.Fatalf("visit: %v", err)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name != session.CookieName {
			continue
		}
		found = true
		if c.Value != "sess-abc" {
			t.Fatalf("session cookie value = %q, want the minted session id", c.Value)
		}
		if !c.Secure || !c.HttpOnly {
			t.Fatalf("session cookie must be Secure+HttpOnly, got %+v", c)
		}
	}
	if !found {
		t.Fatalf("accept response must set the %q session cookie (auto-login)", session.CookieName)
	}
}
