//go:build !darwin

package helper

// NewBackend on platforms without a real tunnel backend yet returns the
// StubBackend (tunnel ops report not_implemented). The macOS backend is
// backend_darwin.go; the Windows (wireguard-nt + WFP) backend lands next.
func NewBackend() Backend { return StubBackend{} }
