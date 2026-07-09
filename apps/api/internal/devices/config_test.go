package devices

import (
	"strings"
	"testing"
)

func TestBuildConfigSplitTunnel(t *testing.T) {
	conf := buildConfig(configParams{
		address:      "10.99.0.2",
		privateKey:   "PRIVKEY==",
		serverPubKey: "SERVERPUB==",
		endpoint:     "gw.example.com:51820",
		allowedIPs:   allowedIPsFor(false, "10.99.0.0/24"),
	})
	for _, want := range []string{
		"[Interface]", "PrivateKey = PRIVKEY==", "Address = 10.99.0.2/32", "MTU = 1420",
		"[Peer]", "PublicKey = SERVERPUB==", "Endpoint = gw.example.com:51820",
		"AllowedIPs = 10.99.0.0/24", "PersistentKeepalive = 25",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("config missing %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "0.0.0.0/0") {
		t.Fatal("split-tunnel config must not route all traffic")
	}
	if strings.Contains(conf, "DNS =") {
		t.Fatal("split-tunnel config should not force a DNS server")
	}
}

func TestBuildConfigFullTunnel(t *testing.T) {
	conf := buildConfig(configParams{
		address: "10.99.0.2", privateKey: "k", serverPubKey: "s",
		endpoint: "h:51820", allowedIPs: allowedIPsFor(true, "10.99.0.0/24"),
		dns: dnsFor(true),
	})
	// Full-tunnel MUST cover BOTH families or IPv6 leaks (and the client kill-switch
	// rejects it as incomplete_full_tunnel).
	if !strings.Contains(conf, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Fatalf("full-tunnel config must route BOTH 0.0.0.0/0 AND ::/0:\n%s", conf)
	}
	if !strings.Contains(conf, "DNS = "+fullTunnelDNS) {
		t.Fatalf("full-tunnel config must set a DNS server:\n%s", conf)
	}
}
