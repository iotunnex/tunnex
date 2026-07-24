package ovpn

import (
	"strings"
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/ovpnca"
)

// TestBuildProfileInlineAndServerPinned (S9.1 Slice 4b) locks the .ovpn shape: a standard client can
// import it (inline ca/cert/key), the gateway remote is set, and remote-cert-tls server pins the
// server-auth EKU so a client cert can't impersonate the gateway.
func TestBuildProfileInlineAndServerPinned(t *testing.T) {
	p := ovpnca.Profile{
		CertPEM:       "-----BEGIN CERTIFICATE-----\nCLIENTCERT\n-----END CERTIFICATE-----\n",
		PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nCLIENTKEY\n-----END RSA PRIVATE KEY-----\n",
	}
	out := BuildProfile("-----BEGIN CERTIFICATE-----\nCACERT\n-----END CERTIFICATE-----\n", p, "gw.example.com", 1194)

	for _, want := range []string{
		"client\n", "remote gw.example.com 1194\n", "remote-cert-tls server\n",
		"<ca>\n", "CACERT", "</ca>\n",
		"<cert>\n", "CLIENTCERT", "</cert>\n",
		"<key>\n", "CLIENTKEY", "</key>\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("profile missing %q; got:\n%s", want, out)
		}
	}
	// the inline material must be the ACTUAL key/cert (the one-time-delivered secret), not a placeholder.
	if !strings.Contains(out, "CLIENTKEY") {
		t.Fatal("the client private key must be inlined (delivered once)")
	}
}
