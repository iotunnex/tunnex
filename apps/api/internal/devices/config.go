package devices

import (
	"fmt"
	"strings"
)

const (
	clientMTU = 1420 // match the server interface MTU (S3.2 decision)
	keepalive = 25   // seconds; makes the client initiate a handshake through NAT
	// fullTunnelDNS is handed to full-tunnel clients so name resolution still
	// works once 0.0.0.0/0 captures all traffic (the previous resolver may be
	// unreachable through the tunnel). Split-tunnel clients keep their own DNS.
	fullTunnelDNS = "1.1.1.1"
)

// configParams are the inputs to a client .conf.
type configParams struct {
	address      string // peer tunnel IP (no mask)
	privateKey   string // the client's private key (one-time, server-generated flow)
	serverPubKey string
	endpoint     string   // host:port
	allowedIPs   []string // split (org CIDR) or full-tunnel (0.0.0.0/0)
	dns          string   // optional
}

// buildConfig renders a wg-quick-compatible client configuration. The private
// key is embedded only for the server-generated flow (delivered once).
func buildConfig(p configParams) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + p.privateKey + "\n")
	b.WriteString("Address = " + p.address + "/32\n")
	if p.dns != "" {
		b.WriteString("DNS = " + p.dns + "\n")
	}
	b.WriteString(fmt.Sprintf("MTU = %d\n", clientMTU))
	b.WriteString("\n[Peer]\n")
	b.WriteString("PublicKey = " + p.serverPubKey + "\n")
	b.WriteString("Endpoint = " + p.endpoint + "\n")
	b.WriteString("AllowedIPs = " + strings.Join(p.allowedIPs, ", ") + "\n")
	b.WriteString(fmt.Sprintf("PersistentKeepalive = %d\n", keepalive))
	return b.String()
}

// allowedIPsFor returns the client's AllowedIPs: split-tunnel (the org pool only,
// the default — zero-trust posture) or full-tunnel (all traffic).
func allowedIPsFor(fullTunnel bool, poolCIDR string) []string {
	if fullTunnel {
		return []string{"0.0.0.0/0"}
	}
	return []string{poolCIDR}
}

// dnsFor returns the DNS server to embed: a resolver for full-tunnel (all traffic
// captured), none for split-tunnel (the client keeps its own resolver).
func dnsFor(fullTunnel bool) string {
	if fullTunnel {
		return fullTunnelDNS
	}
	return ""
}
