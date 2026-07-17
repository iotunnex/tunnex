//go:build linux

package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// wgctrlBackend is the real WireGuard data-plane adapter. It drives the standard
// tools (ip / wg / wireguard-go), so it works with kernel WireGuard or the
// userspace implementation. Peer convergence uses `wg syncconf`, which removes
// absent peers and leaves unchanged peers UNTOUCHED (no handshake reset) —
// idempotent against a dirty device.
type wgctrlBackend struct {
	iface  string
	logger *slog.Logger
	// Cached from the last Configure so ApplyPeers' `wg syncconf` can echo them in
	// its [Interface] section. An EMPTY [Interface] makes syncconf CLEAR the
	// private key (→ "(none)") and reset the listen port to a random value —
	// silently breaking every tunnel on the next reconcile (POC-surfaced bug).
	privKey    string
	listenPort int
}

func newWGCtrlBackend(iface string, logger *slog.Logger) (WGBackend, error) {
	return &wgctrlBackend{iface: iface, logger: logger}, nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Configure idempotently ensures the interface exists and has the given key,
// port, address, and MTU. It is DIRTY-CHECKED: it reads current device state and
// only applies what differs, so a steady-state reconcile touches nothing — in
// particular it never re-issues `wg set private-key` on an unchanged key, which
// would needlessly churn the interface. Diagnosable errors bubble up (agent
// readiness reflects them) rather than pretending success.
func (b *wgctrlBackend) Configure(ctx context.Context, cfg InterfaceConfig) error {
	if err := b.ensureDevice(ctx); err != nil {
		return err
	}
	// Cache for ApplyPeers' syncconf so it echoes (never clears) the key + port.
	b.privKey, b.listenPort = cfg.PrivateKey, cfg.ListenPort
	curPub, curPort := b.currentWGInterface(ctx)

	// (Re)set the private key only when the interface's PUBLIC key doesn't already
	// match ours. Comparing public keys is clamp-invariant: WireGuard clamps the
	// stored private key, so the raw private bytes always differ, but the public
	// key is stable — so this fires exactly once (or on a real re-key), never on
	// every reconcile. Done as its own `wg set` so it can't disturb the port.
	if cfg.PrivateKey != "" && (cfg.PublicKey == "" || cfg.PublicKey != curPub) {
		keyFile, err := os.CreateTemp("", "wgkey")
		if err != nil {
			return err
		}
		defer os.Remove(keyFile.Name())
		_ = os.Chmod(keyFile.Name(), 0o600)
		if _, err := keyFile.WriteString(cfg.PrivateKey); err != nil {
			_ = keyFile.Close()
			return err
		}
		_ = keyFile.Close()
		if _, err := run(ctx, "wg", "set", b.iface, "private-key", keyFile.Name()); err != nil {
			return err
		}
	}
	// Set the listen port only when it differs — re-setting it to the current
	// value fails with "Address in use". A zero desired port would make wg pick a
	// random port, so refuse it.
	if cfg.ListenPort > 0 && cfg.ListenPort != curPort {
		if _, err := run(ctx, "wg", "set", b.iface, "listen-port", strconv.Itoa(cfg.ListenPort)); err != nil {
			return err
		}
	}

	if cfg.Address != "" && !b.hasAddress(ctx, cfg.Address) {
		if _, err := run(ctx, "ip", "address", "replace", cfg.Address, "dev", b.iface); err != nil {
			return err
		}
	}
	return b.ensureLinkUp(ctx, cfg.MTU)
}

func (b *wgctrlBackend) ensureDevice(ctx context.Context) error {
	if _, err := run(ctx, "ip", "link", "show", b.iface); err == nil {
		return nil // already exists
	}
	// Kernel WireGuard (present on most modern kernels incl. Docker's LinuxKit
	// VM). Userspace wireguard-go is tried only if the binary is installed; a
	// clear error otherwise so readiness failure is diagnosable.
	if _, err := run(ctx, "ip", "link", "add", "dev", b.iface, "type", "wireguard"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("wireguard-go"); err == nil {
		if _, err := run(ctx, "wireguard-go", b.iface); err == nil {
			return nil
		}
	}
	return fmt.Errorf("cannot create wg device %q: kernel WireGuard module unavailable and no wireguard-go binary (need NET_ADMIN + WG kernel module or wireguard-go)", b.iface)
}

// currentWGInterface returns the interface's current PUBLIC key and listen port
// from `wg show <iface> dump` (line 0: private-key, public-key, listen-port,
// fwmark). The public key is used for the clamp-safe key-set check. Returns
// ("", 0) if unreadable.
func (b *wgctrlBackend) currentWGInterface(ctx context.Context) (pubKey string, listenPort int) {
	out, err := run(ctx, "wg", "show", b.iface, "dump")
	if err != nil {
		return "", 0
	}
	return parseWGInterface(out)
}

// parseWGInterface parses the interface (first) line of `wg show <iface> dump`
// (private-key, PUBLIC-key, listen-port, fwmark), returning the public key and
// port. We compare the PUBLIC key (field 1), not the private key (field 0):
// WireGuard clamps the stored private key, so its raw bytes differ from what the
// agent generated, but the derived public key is stable. Returns ("", 0) if the
// line is malformed or the interface has no key yet ("(none)").
func parseWGInterface(dump string) (pubKey string, listenPort int) {
	lines := strings.Split(strings.TrimSpace(dump), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", 0
	}
	f := strings.Split(lines[0], "\t")
	if len(f) < 3 {
		return "", 0
	}
	pub := f[1]
	if pub == "(none)" {
		pub = ""
	}
	port, _ := strconv.Atoi(f[2])
	return pub, port
}

// hasAddress reports whether addr (e.g. "10.99.0.1/32") is already on the device.
func (b *wgctrlBackend) hasAddress(ctx context.Context, addr string) bool {
	out, err := run(ctx, "ip", "-o", "addr", "show", "dev", b.iface)
	if err != nil {
		return false
	}
	return strings.Contains(out, addr)
}

// ensureLinkUp sets MTU + brings the link up only if it is not already at the
// desired MTU and up.
func (b *wgctrlBackend) ensureLinkUp(ctx context.Context, mtu int) error {
	out, err := run(ctx, "ip", "link", "show", b.iface)
	if err == nil {
		hasMTU := mtu <= 0 || strings.Contains(out, "mtu "+strconv.Itoa(mtu))
		isUp := strings.Contains(out, "state UP") || strings.Contains(out, "state UNKNOWN") ||
			strings.Contains(out, ",UP,") || strings.Contains(out, "<UP")
		if hasMTU && isUp {
			return nil
		}
	}
	setArgs := []string{"link", "set", "dev", b.iface}
	if mtu > 0 {
		setArgs = append(setArgs, "mtu", strconv.Itoa(mtu))
	}
	setArgs = append(setArgs, "up")
	_, err = run(ctx, "ip", setArgs...)
	return err
}

// Peers reads the current peer set from the device.
func (b *wgctrlBackend) Peers(ctx context.Context) ([]Peer, error) {
	out, err := run(ctx, "wg", "show", b.iface, "dump")
	if err != nil {
		return nil, err
	}
	return parseWGDump(out), nil
}

// Stats parses per-peer live telemetry from `wg show <iface> dump`.
func (b *wgctrlBackend) Stats(ctx context.Context) ([]PeerStat, error) {
	out, err := run(ctx, "wg", "show", b.iface, "dump")
	if err != nil {
		return nil, err
	}
	return parseWGStats(out), nil
}

// parseWGStats parses the peer lines of a dump into telemetry. Peer fields:
// pubkey, psk, endpoint, allowed-ips, latest-handshake(unix), rx, tx, keepalive.
func parseWGStats(out string) []PeerStat {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var stats []PeerStat
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		s := PeerStat{PublicKey: f[0]}
		if f[2] != "(none)" && f[2] != "" {
			s.Endpoint = f[2]
		}
		s.LastHandshake, _ = strconv.ParseInt(f[4], 10, 64)
		s.RxBytes, _ = strconv.ParseInt(f[5], 10, 64)
		s.TxBytes, _ = strconv.ParseInt(f[6], 10, 64)
		stats = append(stats, s)
	}
	return stats
}

// parseWGDump parses `wg show <iface> dump` output into peers. The first line is
// the interface itself (skipped); each subsequent tab-separated line is a peer:
// pubkey, preshared-key, endpoint, allowed-ips, ...
func parseWGDump(out string) []Peer {
	var peers []Peer
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, line := range lines {
		if i == 0 || line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		p := Peer{PublicKey: f[0]}
		if f[2] != "(none)" && f[2] != "" {
			p.Endpoint = f[2]
		}
		if f[3] != "(none)" && f[3] != "" {
			p.AllowedIPs = strings.Split(f[3], ",")
		}
		peers = append(peers, p)
	}
	return peers
}

// buildSyncConf renders a wg config containing only [Peer] sections, for
// `wg syncconf` (converges to this peer set; unchanged peers keep their session).
func buildSyncConf(privKey string, listenPort int, peers []Peer) string {
	var sb strings.Builder
	sb.WriteString("[Interface]\n")
	// Echo the key + port: `wg syncconf` CLEARS anything ABSENT from [Interface]
	// (an empty section wipes the private key + randomizes the listen port). Writing
	// them makes syncconf idempotent on the interface instead of destructive.
	if privKey != "" {
		sb.WriteString("PrivateKey = " + privKey + "\n")
	}
	if listenPort > 0 {
		sb.WriteString("ListenPort = " + strconv.Itoa(listenPort) + "\n")
	}
	for _, p := range peers {
		sb.WriteString("\n[Peer]\nPublicKey = " + p.PublicKey + "\n")
		if len(p.AllowedIPs) > 0 {
			sb.WriteString("AllowedIPs = " + strings.Join(p.AllowedIPs, ",") + "\n")
		}
		if p.Endpoint != "" {
			sb.WriteString("Endpoint = " + p.Endpoint + "\n")
		}
	}
	return sb.String()
}

// ApplyPeers converges the peer set via `wg syncconf` (idempotent; unchanged
// peers keep their sessions, absent peers are removed).
func (b *wgctrlBackend) ApplyPeers(ctx context.Context, peers []Peer) error {
	f, err := os.CreateTemp("", "wgconf")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(buildSyncConf(b.privKey, b.listenPort, peers)); err != nil {
		return err
	}
	_ = f.Close()
	_, err = run(ctx, "wg", "syncconf", b.iface, f.Name())
	return err
}

// ApplyRoutes reconciles the S8.2 site-to-site kernel routes on the tunnel iface. It installs each
// desired remote-subnet route with `proto static` (idempotent replace — heals a flushed route on the
// next tick) and PRUNES any of OUR routes (proto static, this iface) no longer desired — the full-sweep
// contract on the wire (a site unbind/subnet removal drops out of the desired set → its route is
// deleted). The `proto static` scoping is load-bearing: the interface's own on-link pool route is
// `proto kernel`, so it is NEVER enumerated here and can never be pruned by us.
func (b *wgctrlBackend) ApplyRoutes(ctx context.Context, cidrs []string) error {
	desired := make(map[string]bool, len(cidrs))
	for _, c := range cidrs {
		desired[c] = true
		if _, err := run(ctx, "ip", "route", "replace", c, "dev", b.iface, "proto", "static"); err != nil {
			return err
		}
	}
	// Enumerate ONLY our routes and prune the stale ones. If we can't enumerate, surface it (never guess).
	out, err := run(ctx, "ip", "route", "show", "dev", b.iface, "proto", "static")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		cidr := fields[0]
		if cidr == "" || desired[cidr] {
			continue
		}
		if _, err := run(ctx, "ip", "route", "del", cidr, "dev", b.iface, "proto", "static"); err != nil {
			return err
		}
	}
	return nil
}
