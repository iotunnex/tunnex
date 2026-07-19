//go:build darwin || windows

package helper

import (
	"strings"
	"testing"
)

// TestAllowedIPsUAPINoBounce (S8.5 Slice 2a) — the live-apply uapi carries NO private_key / replace_peers /
// endpoint (so the device + peer identity + the session/handshake SURVIVE — no bounce, by construction) and
// DOES carry update_only=true (an absent peer is never CREATED — a typo can't conjure a phantom peer) +
// replace_allowed_ips=true (full-sweep) + the allowed_ip lines. Lives in a darwin||windows-tagged file
// because allowedIPsUAPI (wgcommon.go) is only compiled on the real-backend platforms.
func TestAllowedIPsUAPINoBounce(t *testing.T) {
	const zeroKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	uapi, err := allowedIPsUAPI(zeroKey, []string{"10.0.0.0/16", "192.168.1.0/24"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, must := range []string{"update_only=true", "replace_allowed_ips=true", "allowed_ip=10.0.0.0/16", "allowed_ip=192.168.1.0/24"} {
		if !strings.Contains(uapi, must) {
			t.Errorf("uapi missing %q:\n%s", must, uapi)
		}
	}
	for _, forbidden := range []string{"private_key=", "replace_peers=", "endpoint="} {
		if strings.Contains(uapi, forbidden) {
			t.Errorf("uapi MUST NOT contain %q (would reset the peer/session — a bounce):\n%s", forbidden, uapi)
		}
	}
}
