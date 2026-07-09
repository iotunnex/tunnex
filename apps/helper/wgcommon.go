//go:build darwin || windows

package helper

// Shared wireguard-go helpers used by the REAL tunnel backends (macOS pf / Windows
// WFP). Platform-agnostic: uapi rendering, MTU, stats parsing, and the split-default
// route mapping. Excluded from the stub (linux) build, which needs no wireguard-go.

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"golang.zx2c4.com/wireguard/device"
)

// splitEndpoint splits host:port, unwrapping a bracketed IPv6 host.
func splitEndpoint(endpoint string) (host, port string) {
	if i := strings.LastIndex(endpoint, ":"); i > 0 {
		host, port = endpoint[:i], endpoint[i+1:]
	} else {
		host = endpoint
	}
	return strings.Trim(host, "[]"), port
}

// resolveEndpoint replaces a hostname endpoint with a SINGLE resolved IP so the pf/WFP
// pass rule, the endpoint host-route, AND wireguard-go all pin the SAME address. A
// multi-address DNS name resolved independently by each could otherwise pin/permit a
// different IP than the tunnel actually dials → the kill-switch would block the
// handshake (review #10). Already-IP endpoints pass through unchanged.
func resolveEndpoint(endpoint string) (string, error) {
	host, port := splitEndpoint(endpoint)
	if net.ParseIP(host) != nil {
		return endpoint, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", &ProtocolError{Code: "endpoint_unresolved", Msg: "cannot resolve WG endpoint " + host + ": " + err.Error()}
	}
	if len(ips) == 0 {
		return "", &ProtocolError{Code: "endpoint_unresolved", Msg: "no addresses for WG endpoint " + host}
	}
	return net.JoinHostPort(ips[0].String(), port), nil
}

func deviceMTU(cfg *TunnelConfig) int {
	if cfg.MTU > 0 {
		return cfg.MTU
	}
	return device.DefaultMTU
}

// uapiConfig renders the wireguard-go IpcSet string. Keys are HEX in the uapi, so
// the base64 config keys are converted.
func uapiConfig(cfg *TunnelConfig) (string, error) {
	priv, err := b64ToHex(cfg.PrivateKey)
	if err != nil {
		return "", &ProtocolError{Code: "bad_private_key", Msg: err.Error()}
	}
	pub, err := b64ToHex(cfg.PeerPublicKey)
	if err != nil {
		return "", &ProtocolError{Code: "bad_peer_key", Msg: err.Error()}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "private_key=%s\n", priv)
	fmt.Fprintf(&sb, "listen_port=0\n")
	fmt.Fprintf(&sb, "replace_peers=true\n")
	fmt.Fprintf(&sb, "public_key=%s\n", pub)
	fmt.Fprintf(&sb, "endpoint=%s\n", cfg.Endpoint)
	if cfg.PersistentKeepalive > 0 {
		fmt.Fprintf(&sb, "persistent_keepalive_interval=%d\n", cfg.PersistentKeepalive)
	}
	for _, aip := range cfg.AllowedIPs {
		fmt.Fprintf(&sb, "allowed_ip=%s\n", aip)
	}
	return sb.String(), nil
}

func b64ToHex(k string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// routeTargets maps a route destination to the actual OS routes to install. A
// full-tunnel default (0.0.0.0/0 or ::/0) is installed as the WG-standard PAIR of
// half-routes (0.0.0.0/1 + 128.0.0.0/1 ; ::/1 + 8000::/1): they cover all traffic and
// are MORE SPECIFIC than the physical default, so they take precedence WITHOUT
// destroying it. When the tunnel adapter disappears (Down, crash, kill -9), the halves
// vanish with the interface and the physical default resurfaces automatically — no
// capture/restore, no stranded host. Non-default destinations pass through unchanged.
func routeTargets(allowedIP string) []string {
	switch allowedIP {
	case "0.0.0.0/0":
		return []string{"0.0.0.0/1", "128.0.0.0/1"}
	case "::/0":
		return []string{"::/1", "8000::/1"}
	default:
		return []string{allowedIP}
	}
}

// parseStats pulls handshake + transfer counters out of a wireguard-go IpcGet dump.
func parseStats(get, ifname string) TunnelStatus {
	st := TunnelStatus{State: string(StateUp), Interface: ifname}
	for _, line := range strings.Split(get, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "last_handshake_time_sec":
			fmt.Sscanf(v, "%d", &st.LastHandshakeSec)
		case "rx_bytes":
			fmt.Sscanf(v, "%d", &st.RxBytes)
		case "tx_bytes":
			fmt.Sscanf(v, "%d", &st.TxBytes)
		}
	}
	return st
}
