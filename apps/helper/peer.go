package helper

import "net"

// NewPeerResolver returns the platform PeerResolver used to authenticate a
// connection's caller (its on-disk executable path → CallerVerifier).
//
// TODO(S6.3): the REAL per-platform resolution — macOS: the XPC/socket audit
// token → pid → proc_pidpath; Linux: SO_PEERCRED → /proc/pid/exe; Windows:
// GetNamedPipeClientProcessId → QueryFullProcessImageName. Until each lands, this
// FAILS CLOSED: it refuses to resolve any peer, so the Server rejects every caller
// (a helper that cannot identify its caller must trust no one, never everyone).
func NewPeerResolver() PeerResolver {
	return func(net.Conn) (string, error) {
		return "", &ProtocolError{Code: "peer_resolution_unavailable", Msg: "platform caller-path resolution not yet implemented"}
	}
}
