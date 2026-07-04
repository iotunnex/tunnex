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
	curPriv, curPort := b.currentWGInterface(ctx)

	// Build a single `wg set` with only the fields that changed.
	args := []string{"set", b.iface}
	var keyFileName string
	if cfg.PrivateKey != "" && cfg.PrivateKey != curPriv {
		f, err := os.CreateTemp("", "wgkey")
		if err != nil {
			return err
		}
		keyFileName = f.Name()
		defer os.Remove(keyFileName)
		_ = os.Chmod(keyFileName, 0o600)
		if _, err := f.WriteString(cfg.PrivateKey); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
		args = append(args, "private-key", keyFileName)
	}
	// A zero listen-port would make wg pick a RANDOM port; refuse it rather than
	// silently move off the published UDP port.
	if cfg.ListenPort > 0 && cfg.ListenPort != curPort {
		args = append(args, "listen-port", strconv.Itoa(cfg.ListenPort))
	}
	if len(args) > 2 {
		if _, err := run(ctx, "wg", args...); err != nil {
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

// currentWGInterface returns the interface's current private key and listen port
// from `wg show <iface> dump` (line 0: private-key, public-key, listen-port,
// fwmark). Returns ("", 0) if unreadable.
func (b *wgctrlBackend) currentWGInterface(ctx context.Context) (privKey string, listenPort int) {
	out, err := run(ctx, "wg", "show", b.iface, "dump")
	if err != nil {
		return "", 0
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return "", 0
	}
	f := strings.Split(lines[0], "\t")
	if len(f) < 3 {
		return "", 0
	}
	priv := f[0]
	if priv == "(none)" {
		priv = ""
	}
	port, _ := strconv.Atoi(f[2])
	return priv, port
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
func buildSyncConf(peers []Peer) string {
	var sb strings.Builder
	sb.WriteString("[Interface]\n") // syncconf ignores interface fields but wants the section
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
	if _, err := f.WriteString(buildSyncConf(peers)); err != nil {
		return err
	}
	_ = f.Close()
	_, err = run(ctx, "wg", "syncconf", b.iface, f.Name())
	return err
}
