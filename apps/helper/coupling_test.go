package helper

import (
	"os"
	"strings"
	"testing"
)

// TestWindowsBypassFlagRequiresGuard enforces the S6.10 ATOMIC-COUPLING condition: the dev
// bypass flag (TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL) may exist in the tree ONLY while the S6.9
// full-tunnel guard is still present. When Story B (S6.7) lifts the guard, the flag AND the
// guard are removed together. If a change removes the guard but leaves the flag, this test
// FAILS — so the bypass can never silently outlive the guard and re-expose Windows full
// tunnel unguarded. (Runs on every platform, so the linux `gates` job enforces it too.)
func TestWindowsBypassFlagRequiresGuard(t *testing.T) {
	src, err := os.ReadFile("backend_windows.go")
	if err != nil {
		t.Fatalf("read backend_windows.go: %v", err)
	}
	s := string(src)
	hasFlag := strings.Contains(s, "TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL")
	hasGuard := strings.Contains(s, `Code: "full_tunnel_unsupported"`)
	if hasFlag && !hasGuard {
		t.Fatal("S6.10 coupling: the dev bypass flag is present without the full_tunnel_unsupported guard — the flag and guard must be removed TOGETHER (Story B lift), never the guard alone")
	}
}
