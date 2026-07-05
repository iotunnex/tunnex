package devices

import (
	"fmt"
	"strings"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
)

// Tunnel pool constants. TODO(S3.5): the org pool + allocation (release/reuse,
// CIDR resize) is owned by S3.5; S3.4 uses a fixed /24 with lowest-free assignment.
const (
	orgPoolCIDR = "10.99.0.0/24"
	clientMTU   = 1420 // match the server interface MTU (S3.2 decision)
	keepalive   = 25   // seconds; makes the client initiate a handshake through NAT
	// fullTunnelDNS is handed to full-tunnel clients so name resolution still
	// works once 0.0.0.0/0 captures all traffic (the previous resolver may be
	// unreachable through the tunnel). Split-tunnel clients keep their own DNS.
	fullTunnelDNS = "1.1.1.1"
)

// allocateIP returns the lowest free host (.2–.254) in the org pool not already
// taken, or a pool-exhausted error. Minimal allocator; S3.5 owns the real one.
func allocateIP(used []*string) (string, error) {
	taken := map[string]bool{}
	for _, u := range used {
		if u != nil {
			taken[*u] = true
		}
	}
	for i := 2; i <= 254; i++ {
		ip := fmt.Sprintf("10.99.0.%d", i)
		if !taken[ip] {
			return ip, nil
		}
	}
	return "", apierr.Conflict("pool_exhausted", "no free tunnel address in the org pool")
}

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

// allowedIPsFor returns the client's AllowedIPs: split-tunnel (org network only,
// the default — zero-trust posture) or full-tunnel (all traffic).
func allowedIPsFor(fullTunnel bool) []string {
	if fullTunnel {
		return []string{"0.0.0.0/0"}
	}
	return []string{orgPoolCIDR}
}

// dnsFor returns the DNS server to embed: a resolver for full-tunnel (all traffic
// captured), none for split-tunnel (the client keeps its own resolver).
func dnsFor(fullTunnel bool) string {
	if fullTunnel {
		return fullTunnelDNS
	}
	return ""
}
