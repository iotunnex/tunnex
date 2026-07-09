//go:build windows

package helper

import "net"

// NewPeerResolver (Windows) — the real resolver needs the named-pipe handle to
// call GetNamedPipeClientProcessId → QueryFullProcessImageName, so it lands with
// the go-winio pipe listener (a net.Conn alone can't reach the pipe handle).
// Until then it FAILS CLOSED (refuses every caller), matching the pipe listener
// stub that refuses to start.
func NewPeerResolver() PeerResolver {
	return func(net.Conn) (string, error) {
		return "", &ProtocolError{Code: "peer_resolution_unavailable", Msg: "windows caller-path resolution lands with the named-pipe listener (S6.3)"}
	}
}
