//go:build !windows

package helper

import (
	"net"
	"os"
	"path/filepath"
)

// DefaultSocketPath is the unix-domain socket the helper listens on (matches the
// TS helperSocketPath in apps/client).
func DefaultSocketPath() string { return "/var/run/tunnex/helper.sock" }

// NewListener creates the helper's local IPC endpoint. On unix (macOS) it is a
// unix-domain socket with OWNER-ONLY (0600) permissions in a 0700 directory, so
// only the same user can connect — the caller's identity is then verified by the
// PeerResolver + CallerVerifier. A stale socket from a prior crash is removed.
func NewListener(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath) // clear a stale socket
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}
