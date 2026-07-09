package helper

import (
	"encoding/base64"
	"net/netip"
	"strconv"
	"strings"
)

// TunnelConfig is the STRUCTURED WireGuard config the app hands the helper over
// IPC. It is deliberately NOT a file path: passing a path to a root process
// invites TOCTOU + arbitrary-read; a validated struct closes both. Every field is
// checked by Validate BEFORE the helper touches the network, and malformed input
// is rejected with a stable code rather than best-effort'd.
type TunnelConfig struct {
	// PrivateKey is the interface's own WireGuard key: base64 of exactly 32 bytes.
	PrivateKey string `json:"private_key"`
	// PeerPublicKey is the server peer's public key: base64 of exactly 32 bytes.
	PeerPublicKey string `json:"peer_public_key"`
	// Endpoint is the server host:port (host may be IP or DNS name).
	Endpoint string `json:"endpoint"`
	// Address is the interface address as a single-host CIDR (e.g. 10.99.0.2/32).
	Address string `json:"address"`
	// AllowedIPs are the CIDRs routed INTO the tunnel (e.g. ["0.0.0.0/0","::/0"]
	// for full-tunnel, or specific subnets for split).
	AllowedIPs []string `json:"allowed_ips"`
	// DNS are resolver IPs applied while the tunnel is up (optional).
	DNS []string `json:"dns,omitempty"`
	// MTU is the interface MTU (0 = default; else 1280..1500).
	MTU int `json:"mtu,omitempty"`
	// PersistentKeepalive seconds (0 = off; else 1..65535).
	PersistentKeepalive int `json:"persistent_keepalive,omitempty"`
}

const wgKeyLen = 32

// Validate fails CLOSED on anything malformed. It returns a *ProtocolError whose
// Code is stable so the app can branch/telemetry on it. It does NOT mutate the
// config and it never logs key material.
func (c *TunnelConfig) Validate() error {
	if c == nil {
		return &ProtocolError{Code: "config_required", Msg: "config is nil"}
	}
	if err := validKey(c.PrivateKey); err != nil {
		return &ProtocolError{Code: "bad_private_key", Msg: err.Error()}
	}
	if err := validKey(c.PeerPublicKey); err != nil {
		return &ProtocolError{Code: "bad_peer_key", Msg: err.Error()}
	}
	if !validEndpoint(c.Endpoint) {
		return &ProtocolError{Code: "bad_endpoint", Msg: "endpoint must be host:port"}
	}
	if !validHostCIDR(c.Address) {
		return &ProtocolError{Code: "bad_address", Msg: "address must be a valid CIDR"}
	}
	if len(c.AllowedIPs) == 0 {
		return &ProtocolError{Code: "bad_allowed_ips", Msg: "allowed_ips must not be empty"}
	}
	for _, a := range c.AllowedIPs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(a)); err != nil {
			return &ProtocolError{Code: "bad_allowed_ips", Msg: "invalid CIDR in allowed_ips: " + a}
		}
	}
	for _, d := range c.DNS {
		if _, err := netip.ParseAddr(strings.TrimSpace(d)); err != nil {
			return &ProtocolError{Code: "bad_dns", Msg: "invalid DNS IP: " + d}
		}
	}
	if c.MTU != 0 && (c.MTU < 1280 || c.MTU > 1500) {
		return &ProtocolError{Code: "bad_mtu", Msg: "mtu must be 0 or 1280..1500"}
	}
	if c.PersistentKeepalive < 0 || c.PersistentKeepalive > 65535 {
		return &ProtocolError{Code: "bad_keepalive", Msg: "persistent_keepalive must be 0..65535"}
	}
	return nil
}

// validKey requires standard base64 decoding to EXACTLY 32 bytes — the WireGuard
// key size. Rejects wrong length, non-base64, and empty.
func validKey(s string) error {
	if s == "" {
		return &ProtocolError{Code: "empty_key", Msg: "key is empty"}
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return &ProtocolError{Code: "not_base64", Msg: "key is not valid base64"}
	}
	if len(raw) != wgKeyLen {
		return &ProtocolError{Code: "bad_key_len", Msg: "key must decode to 32 bytes"}
	}
	return nil
}

// validEndpoint requires host:port with a numeric port in 1..65535 and a non-empty
// host. It does not resolve DNS (the helper does that at dial time).
func validEndpoint(s string) bool {
	host, port, ok := splitHostPort(s)
	if !ok || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535
}

// splitHostPort splits "host:port" handling bracketed IPv6 ("[::1]:51820"). It
// returns ok=false if there is no single trailing :port.
func splitHostPort(s string) (host, port string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if strings.HasPrefix(s, "[") { // [ipv6]:port
		end := strings.LastIndex(s, "]")
		if end < 0 || end+1 >= len(s) || s[end+1] != ':' {
			return "", "", false
		}
		return s[1:end], s[end+2:], true
	}
	i := strings.LastIndex(s, ":")
	if i < 0 || i == len(s)-1 || strings.Contains(s[:i], ":") {
		return "", "", false // no port, or a bare IPv6 without brackets
	}
	return s[:i], s[i+1:], true
}

// validHostCIDR requires a parseable CIDR (host or network form both fine).
func validHostCIDR(s string) bool {
	_, err := netip.ParsePrefix(strings.TrimSpace(s))
	return err == nil
}
