//go:build windows

package helper

import (
	"errors"
	"net"
)

// DefaultSocketPath is the named pipe the helper listens on (matches the TS
// helperSocketPath in apps/client).
func DefaultSocketPath() string { return `\\.\pipe\tunnex-helper` }

// NewListener on Windows must create the named pipe with a security descriptor
// restricting it to the installing user + LocalSystem. TODO(S6.3): implement via
// a named-pipe library (e.g. go-winio) with an explicit SDDL; until then it
// refuses to start rather than open an unprotected pipe.
func NewListener(string) (net.Listener, error) {
	return nil, errors.New("windows named-pipe listener not yet implemented (S6.3)")
}
