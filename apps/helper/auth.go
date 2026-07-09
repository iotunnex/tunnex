package helper

import (
	"path/filepath"
	"strings"
)

// CallerVerifier authenticates that the process on the other end of the IPC
// channel is the REAL Tunnex app. The platform code resolves the peer's on-disk
// executable path (macOS: audit token → pid → path; Windows:
// GetNamedPipeClientProcessId → image path) and hands it here; the DECISION logic
// is pure + unit-tested.
type CallerVerifier interface {
	Mode() AuthMode
	// Verify returns nil if peerExePath belongs to a trusted Tunnex binary.
	Verify(peerExePath string) error
}

// PathCheckVerifier is the INTERIM (unsigned-build) verifier: it trusts a caller
// whose executable lives inside the app's install dir.
//
// THREAT MODEL (recorded — this is WEAKER than code-signing pinning, retire at
// S6.5b): it STOPS an unrelated, non-privileged local process from driving the
// root helper (that process runs from somewhere else on disk). It does NOT stop
// an attacker who can already write into / replace a binary inside the install
// dir — but doing that needs admin/root, i.e. the game is already over — nor a
// determined path-resolution race. The wire protocol carries auth_mode so this
// upgrades to code_signing pinning without a break. See PLAN.md SECURITY
// LIMITATION (S6.3).
type PathCheckVerifier struct {
	// InstallDirs are the app's absolute install directories (already realpath'd by
	// the platform caller — e.g. /Applications/Tunnex.app, C:\Program Files\Tunnex).
	// A caller is trusted if its exe lies within ANY of them. Production sets a
	// single dir; a DEV install sets several (e.g. /usr/local/tunnex for the
	// tunnelctl driver AND the dev Electron binary dir for the desktop app), so the
	// same helper serves both callers without a manual repoint.
	InstallDirs []string
}

func (v PathCheckVerifier) Mode() AuthMode { return AuthModePathCheck }

func (v PathCheckVerifier) Verify(peerExePath string) error {
	if len(v.InstallDirs) == 0 {
		return &ProtocolError{Code: "install_dir_unset", Msg: "helper has no install dir configured"}
	}
	if peerExePath == "" {
		return &ProtocolError{Code: "peer_unresolved", Msg: "could not resolve the caller's executable"}
	}
	for _, dir := range v.InstallDirs {
		if dir != "" && isWithin(dir, peerExePath) {
			return nil
		}
	}
	return &ProtocolError{Code: "caller_untrusted", Msg: "caller executable is not inside a Tunnex install dir"}
}

// isWithin reports whether target is dir itself or lies underneath it. Both are
// Clean'd first; the trailing-separator check defeats the sibling-prefix trap
// (…/Tunnex.app must not match …/Tunnex.app-evil).
func isWithin(dir, target string) bool {
	dir = filepath.Clean(dir)
	target = filepath.Clean(target)
	if target == dir {
		return true
	}
	return strings.HasPrefix(target, dir+string(filepath.Separator))
}
