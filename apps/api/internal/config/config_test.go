package config

import (
	"os"
	"testing"
)

// TestAppBaseURLLooksLocal guards the boot-time misconfiguration warning: a remote
// deploy left at the localhost default ships unreachable email links (POC-surfaced).
func TestAppBaseURLLooksLocal(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://localhost", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1", true},
		{"http://0.0.0.0", true},   // bind-any, unreachable as a public link
		{"http://0.0.0.0:8080", true},
		{"", true},                 // unset — no reachable URL
		{"https://tunnex.example.com", false},
		{"http://40.65.63.141", false}, // a real remote IP — not local
	}
	for _, c := range cases {
		if got := (Config{AppBaseURL: c.url}).AppBaseURLLooksLocal(); got != c.want {
			t.Errorf("AppBaseURLLooksLocal(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// TestShippedDefaultTripsLocalWarning is the POC's ACTUAL case: neither .env.example
// nor compose sets APP_BASE_URL, so a remote deploy runs on the code default —
// which MUST trip the warning. Asserts against Load() (the real default source),
// not a hand-written literal, so a future default change can't silently pass.
func TestShippedDefaultTripsLocalWarning(t *testing.T) {
	os.Unsetenv("APP_BASE_URL")
	if !Load().AppBaseURLLooksLocal() {
		t.Fatalf("shipped APP_BASE_URL default (%q) must trip the local-URL warning", Load().AppBaseURL)
	}
}
