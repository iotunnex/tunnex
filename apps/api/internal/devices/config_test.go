package devices

import (
	"strconv"
	"strings"
	"testing"
)

func TestAllocateIPLowestFree(t *testing.T) {
	two, three := "10.99.0.2", "10.99.0.3"
	ip, err := allocateIP([]*string{&three}) // .3 taken -> .2 is lowest free
	if err != nil || ip != "10.99.0.2" {
		t.Fatalf("want 10.99.0.2, got %q err=%v", ip, err)
	}
	ip, err = allocateIP([]*string{&two, &three}) // .2,.3 taken -> .4
	if err != nil || ip != "10.99.0.4" {
		t.Fatalf("want 10.99.0.4, got %q err=%v", ip, err)
	}
}

func TestAllocateIPExhausted(t *testing.T) {
	used := make([]*string, 0, 253)
	for i := 2; i <= 254; i++ {
		s := "10.99.0." + strconv.Itoa(i)
		used = append(used, &s)
	}
	if _, err := allocateIP(used); code(err) != "pool_exhausted" {
		t.Fatalf("want pool_exhausted, got %v", err)
	}
}

func TestBuildConfigSplitTunnel(t *testing.T) {
	conf := buildConfig(configParams{
		address:      "10.99.0.2",
		privateKey:   "PRIVKEY==",
		serverPubKey: "SERVERPUB==",
		endpoint:     "gw.example.com:51820",
		allowedIPs:   allowedIPsFor(false),
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
}

func TestBuildConfigFullTunnel(t *testing.T) {
	conf := buildConfig(configParams{
		address: "10.99.0.2", privateKey: "k", serverPubKey: "s",
		endpoint: "h:51820", allowedIPs: allowedIPsFor(true),
	})
	if !strings.Contains(conf, "AllowedIPs = 0.0.0.0/0") {
		t.Fatalf("full-tunnel config must route all traffic:\n%s", conf)
	}
}
