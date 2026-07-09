package config

import "testing"

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
		{"https://tunnex.example.com", false},
		{"http://40.65.63.141", false}, // a real remote IP — not local
	}
	for _, c := range cases {
		if got := (Config{AppBaseURL: c.url}).AppBaseURLLooksLocal(); got != c.want {
			t.Errorf("AppBaseURLLooksLocal(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
