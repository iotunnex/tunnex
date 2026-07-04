//go:build linux

package reconcile

import (
	"strings"
	"testing"
)

func TestParseWGDump(t *testing.T) {
	// Line 0 = interface (privkey, pubkey, listen-port, fwmark). Then two peers;
	// the second has no endpoint and no allowed-ips ("(none)").
	dump := strings.Join([]string{
		"iprivkey\tipubkey\t51820\toff",
		"peerkey1\t(none)\t203.0.113.5:51820\t10.0.0.2/32,10.0.0.3/32\t0\t100\t200\toff",
		"peerkey2\t(none)\t(none)\t(none)\t0\t0\t0\toff",
	}, "\n")

	peers := parseWGDump(dump)
	if len(peers) != 2 {
		t.Fatalf("want 2 peers, got %d: %+v", len(peers), peers)
	}
	if peers[0].PublicKey != "peerkey1" || peers[0].Endpoint != "203.0.113.5:51820" {
		t.Fatalf("peer0 mis-parsed: %+v", peers[0])
	}
	if len(peers[0].AllowedIPs) != 2 || peers[0].AllowedIPs[1] != "10.0.0.3/32" {
		t.Fatalf("peer0 allowed-ips mis-parsed: %+v", peers[0].AllowedIPs)
	}
	if peers[1].PublicKey != "peerkey2" || peers[1].Endpoint != "" || len(peers[1].AllowedIPs) != 0 {
		t.Fatalf("peer1 should have no endpoint/allowed-ips: %+v", peers[1])
	}
}

// TestSyncConfRoundTrip proves the config we write matches what the device would
// report back: build a syncconf, and (simulating a dump of that state) confirm
// the peer identities round-trip. This is the read-back invariant the compose
// e2e checks against a real device.
func TestSyncConfRoundTrip(t *testing.T) {
	peers := []Peer{
		{PublicKey: "k1", AllowedIPs: []string{"10.0.0.2/32"}, Endpoint: "198.51.100.7:51820"},
		{PublicKey: "k2", AllowedIPs: []string{"10.0.0.3/32"}},
	}
	conf := buildSyncConf(peers)
	if !strings.Contains(conf, "PublicKey = k1") || !strings.Contains(conf, "Endpoint = 198.51.100.7:51820") {
		t.Fatalf("k1 not rendered: %s", conf)
	}
	if !strings.Contains(conf, "AllowedIPs = 10.0.0.3/32") {
		t.Fatalf("k2 allowed-ips not rendered: %s", conf)
	}
	// A peer with no endpoint must not emit an Endpoint line.
	if strings.Count(conf, "Endpoint = ") != 1 {
		t.Fatalf("expected exactly one Endpoint line: %s", conf)
	}
}
