package devices

import (
	"os"
	"strings"
	"testing"
)

// TestWindowsBypassFlagRequiresGuard enforces the S6.10 ATOMIC-COUPLING condition on the
// server side: the dev bypass flag (TUNNEX_ALLOW_WINDOWS_FULLTUNNEL) may exist ONLY while the
// win32 full-tunnel refusal is still present. If a change removes the guard but leaves the
// flag, this FAILS — so the bypass can't silently outlive the guard and let a Windows client
// mint a full-tunnel device unguarded. The flag + guard are removed together when Story B's
// pcap passes.
func TestWindowsBypassFlagRequiresGuard(t *testing.T) {
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("read service.go: %v", err)
	}
	s := string(src)
	hasFlag := strings.Contains(s, "TUNNEX_ALLOW_WINDOWS_FULLTUNNEL")
	// Require the refusal code, the win32 check, AND the negated env gate (`!= "1"`) so a
	// logic-only weakening is more likely to trip this too. The DEFINITIVE semantic check is
	// the behavioral TestFullTunnelRefusedOnWindows (win32 full-tunnel, flag unset → refused
	// even on an egress-capable gateway), which fails if the guard is disabled by default.
	hasGuard := strings.Contains(s, `"full_tunnel_unsupported"`) &&
		strings.Contains(s, `"win32"`) &&
		strings.Contains(s, `!= "1"`)
	if hasFlag && !hasGuard {
		t.Fatal("S6.10 coupling: the dev bypass flag is present without the win32 full_tunnel_unsupported guard gated on `!= \"1\"` — remove the flag and guard TOGETHER, never the guard alone")
	}
}
