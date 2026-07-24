package ovpn

import (
	"fmt"
	"strings"

	"github.com/tunnexio/tunnex/apps/api/internal/ovpnca"
)

// BuildProfile assembles a standard `.ovpn` profile importable by the official OpenVPN clients
// (OpenVPN Connect / Tunnelblick / mobile — B3): client directives + INLINE ca/cert/key. The client
// private key is inlined here and delivered EXACTLY ONCE (the S3.4/D2 one-time ceremony, D-S9.2-1) —
// it is never stored server-side, so it cannot be re-fetched; a lost profile is re-MINTED, not
// re-served. `remote-cert-tls server` pins the server-auth EKU so a stolen CLIENT cert cannot
// impersonate the gateway (the role separation IssueServer/IssueClient enforces, Slice 4a).
func BuildProfile(caPEM string, p ovpnca.Profile, host string, port int) string {
	var b strings.Builder
	b.WriteString("client\n")
	b.WriteString("dev tun\n")
	b.WriteString("proto udp\n")
	fmt.Fprintf(&b, "remote %s %d\n", host, port)
	b.WriteString("resolv-retry infinite\n")
	b.WriteString("nobind\n")
	b.WriteString("remote-cert-tls server\n") // verify the gateway presents a SERVER-auth cert (Slice 4a)
	b.WriteString("cipher AES-256-GCM\n")
	b.WriteString("auth SHA256\n")
	b.WriteString("verb 3\n")
	fmt.Fprintf(&b, "<ca>\n%s</ca>\n", ensureNL(caPEM))
	fmt.Fprintf(&b, "<cert>\n%s</cert>\n", ensureNL(p.CertPEM))
	fmt.Fprintf(&b, "<key>\n%s</key>\n", ensureNL(p.PrivateKeyPEM))
	return b.String()
}

func ensureNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
