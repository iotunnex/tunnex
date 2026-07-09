//go:build darwin && !cgo

package helper

import "net"

// NewPeerResolver (macOS, CGO disabled) — a fail-closed stub so the CI
// cross-compile (CGO_ENABLED=0) compiles the darwin build. The REAL resolver
// (peer_darwin.go) needs cgo/libproc and is built + smoke-verified on a Mac.
func NewPeerResolver() PeerResolver {
	return func(net.Conn) (string, error) {
		return "", &ProtocolError{Code: "peer_resolution_needs_cgo", Msg: "macOS caller-path resolution requires a cgo build"}
	}
}
