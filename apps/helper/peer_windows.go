//go:build windows

package helper

import (
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

// peerAuthKind — Windows uses the real GetNamedPipeClientProcessId resolver.
const peerAuthKind = "native"

// NewPeerResolver (Windows) authenticates the caller via GetNamedPipeClientProcessId
// on the pipe handle → the client pid → QueryFullProcessImageName. The SDDL on the
// pipe (listener_windows.go) governs who may CONNECT; this governs which PROCESS is
// trusted. If the pipe conn does not expose its handle it FAILS CLOSED (refuses).
func NewPeerResolver() PeerResolver {
	return func(c net.Conn) (string, error) {
		sc, ok := c.(syscall.Conn)
		if !ok {
			return "", &ProtocolError{Code: "peer_no_handle", Msg: "pipe connection exposes no OS handle"}
		}
		raw, err := sc.SyscallConn()
		if err != nil {
			return "", err
		}
		var pid uint32
		var cerr error
		if err := raw.Control(func(fd uintptr) {
			cerr = windows.GetNamedPipeClientProcessId(windows.Handle(fd), &pid)
		}); err != nil {
			return "", err
		}
		if cerr != nil {
			return "", &ProtocolError{Code: "peer_pid_unresolved", Msg: cerr.Error()}
		}
		// EDGE (refuse-path): if the client already died, OpenProcess (or the query
		// below) errors → we return an error → the Server refuses the caller. Never
		// trust an unresolvable peer. (Test when Windows tests are runnable.)
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return "", &ProtocolError{Code: "peer_open_failed", Msg: err.Error()}
		}
		defer windows.CloseHandle(h)
		buf := make([]uint16, windows.MAX_PATH)
		n := uint32(len(buf))
		if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
			return "", &ProtocolError{Code: "peer_path_unresolved", Msg: err.Error()}
		}
		return windows.UTF16ToString(buf[:n]), nil
	}
}
