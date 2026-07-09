//go:build darwin

package helper

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// pfAnchor is the pf anchor name the kill-switch rules load into. It is
// kernel-resident: it persists if the helper dies (fail-closed). It is released by
// a graceful Down, the dead-man timeout, OR — if the process died abnormally — the
// next helper's startup CleanStale. See the S6.3 KILL-SWITCH DESIGN in PLAN.
const pfAnchor = "tunnex"

// pfTokenPath persists the `pfctl -E` enable-reference token so that a FRESH helper
// process (after a crash / kill -9 lost the in-memory copy) can release the exact
// reference on startup instead of force-disabling pf for the whole system. Root-only.
const pfTokenPath = "/var/run/tunnex/pf.token"

// darwinBackend implements Backend on macOS with wireguard-go (userspace WG over a
// utun) + a pf kill-switch + ifconfig/route for addressing. Ordering invariant:
// Up arms the pf backstop BEFORE moving routes; Down restores routing then flushes
// pf LAST.
type darwinBackend struct {
	mu      sync.Mutex
	dev     *device.Device
	tunDev  tun.Device
	ifname  string
	pfToken string // reference-counted `pfctl -E` token, released (not -d) on Down
}

// NewBackend returns the macOS tunnel backend.
func NewBackend() Backend { return &darwinBackend{} }

func (b *darwinBackend) Up(cfg *TunnelConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 1) ARM the kill-switch FIRST — but ONLY for a FULL tunnel. A split tunnel
	//    routes just its allowed-IPs and leaves the rest of the user's traffic on
	//    the normal cleartext default route BY DESIGN, so there is nothing to
	//    kill-switch (block-all would wrongly kill the user's internet). Full
	//    tunnel: block everything except the WG endpoint + loopback + DHCP/NDP,
	//    before any route moves.
	if cfg.FullTunnel {
		if err := b.armPF(cfg.Endpoint, ""); err != nil {
			return fmt.Errorf("arm kill-switch: %w", err)
		}
	}

	// 2) Create the utun + wireguard-go device, configure it.
	tdev, err := tun.CreateTUN("utun", deviceMTU(cfg))
	if err != nil {
		return fmt.Errorf("create utun: %w", err)
	}
	name, _ := tdev.Name()
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "tunnex-helper: "))
	uapi, err := uapiConfig(cfg)
	if err != nil {
		_ = tdev.Close()
		return err
	}
	if err := dev.IpcSet(uapi); err != nil {
		_ = tdev.Close()
		return fmt.Errorf("configure device: %w", err)
	}
	if err := dev.Up(); err != nil {
		_ = tdev.Close()
		return fmt.Errorf("device up: %w", err)
	}

	// 2b) Full tunnel: reload the anchor now that the tunnel exists so traffic may
	//     leave on it (still BEFORE routes — a failure here keeps everything blocked).
	if cfg.FullTunnel {
		if err := b.armPF(cfg.Endpoint, name); err != nil {
			dev.Close()
			return fmt.Errorf("allow tunnel in kill-switch: %w", err)
		}
	}

	// 3) ONLY NOW move routes onto the tunnel (address + allowed-IPs).
	if err := run("ifconfig", name, "inet", ipOnly(cfg.Address), ipOnly(cfg.Address), "up"); err != nil {
		dev.Close()
		return fmt.Errorf("assign address: %w", err)
	}
	for _, aip := range cfg.AllowedIPs {
		for _, target := range routeTargets(aip) {
			args := []string{"-q", "add"}
			if strings.Contains(target, ":") {
				args = append(args, "-inet6")
			}
			args = append(args, "-net", target, "-interface", name)
			if err := run("route", args...); err != nil {
				dev.Close()
				return fmt.Errorf("route %s: %w", target, err)
			}
		}
	}

	b.dev, b.tunDev, b.ifname = dev, tdev, name
	return nil
}

func (b *darwinBackend) Down() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Graceful: restore routing (device close drops the utun + its routes), THEN
	// flush the pf backstop LAST so no window reverts to cleartext with the block
	// already gone.
	if b.dev != nil {
		b.dev.Close()
	}
	b.dev, b.tunDev, b.ifname = nil, nil, ""
	return b.releasePF()
}

func (b *darwinBackend) FailClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Alive-process fast path: tear the tun; the pf backstop stays (it was armed at
	// Up and outlives this process anyway). Re-assert it in case Up failed early.
	if b.dev != nil {
		b.dev.Close()
		b.dev, b.tunDev, b.ifname = nil, nil, ""
	}
	return nil
}

func (b *darwinBackend) Stats() (TunnelStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dev == nil {
		return TunnelStatus{State: string(StateDown)}, nil
	}
	get, err := b.dev.IpcGet()
	if err != nil {
		return TunnelStatus{Interface: b.ifname}, err
	}
	return parseStats(get, b.ifname), nil
}

// --- helpers ---

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

// buildPFRules is the kill-switch ruleset loaded into the anchor. Requirements:
//   - (2) loopback is EXEMPT (set skip on lo0) — also protects the app's own
//     127.0.0.1 loopback callback flow.
//   - (4) `block drop out all` covers BOTH inet and inet6 (unqualified = all AFs);
//     NDP is explicitly passed for v6.
//   - the WG endpoint passes (so handshakes/data reach the gateway); once the
//     tunnel exists, its interface is skipped so user traffic may leave on it.
//   - (3) DHCP/NDP pass — a DELIBERATE, threat-model-argued exception so long
//     sessions don't lose their lease/neighbor state. Risk: these are local-link
//     UDP/ICMPv6 protocols; the exposure is a local attacker on the same segment
//     spoofing DHCP/RA, which is out of scope for a VPN egress kill-switch (and
//     already a risk pre-VPN). Worth it to avoid the tunnel silently dying on a
//     DHCP renew.
func buildPFRules(endpoint, ifname string) string {
	host, port := splitEndpoint(endpoint)
	var b strings.Builder
	b.WriteString("set skip on lo0\n")
	if ifname != "" {
		fmt.Fprintf(&b, "set skip on %s\n", ifname)
	}
	b.WriteString("block drop out all\n")
	fmt.Fprintf(&b, "pass out proto udp to %s port %s\n", host, port)
	b.WriteString("pass out proto udp from any port 68 to any port 67\n")   // DHCPv4
	b.WriteString("pass out proto udp from any port 546 to any port 547\n") // DHCPv6
	b.WriteString("pass out inet6 proto icmp6 all\n")                       // NDP
	return b.String()
}

// armPF loads the ruleset into the anchor and enables pf with a REFERENCE-COUNTED
// token (`pfctl -E`), captured once so Down can RELEASE it (`pfctl -X <token>`)
// rather than force-disabling pf for the whole system.
//
// NOTE (lifecycle): a named anchor is only evaluated if the main ruleset
// references it (`anchor "tunnex"` in pf.conf). The SMAppService/installer adds
// that reference (removed on uninstall). The smoke asserts ENFORCEMENT (a blocked
// ping), not rule presence — so a non-referenced anchor is caught.
func (b *darwinBackend) armPF(endpoint, ifname string) error {
	if err := runStdin(buildPFRules(endpoint, ifname), "pfctl", "-a", pfAnchor, "-f", "-"); err != nil {
		return err
	}
	if b.pfToken == "" {
		out, _ := exec.Command("pfctl", "-E").CombinedOutput() // "Token : NNN" (stderr)
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Token") {
				f := strings.Fields(line)
				if len(f) > 0 {
					b.pfToken = f[len(f)-1]
				}
			}
		}
		// Persist the token so a crashed-then-restarted helper can release THIS exact
		// enable-reference (CleanStale) instead of leaking it or force-disabling pf.
		if b.pfToken != "" {
			_ = os.MkdirAll("/var/run/tunnex", 0o755)
			_ = os.WriteFile(pfTokenPath, []byte(b.pfToken), 0o600)
		}
	}
	return nil
}

// releasePF flushes our anchor rules and releases the pf enable reference (both the
// in-memory token and any persisted copy).
func (b *darwinBackend) releasePF() error {
	err := run("pfctl", "-a", pfAnchor, "-F", "all")
	if b.pfToken != "" {
		_ = exec.Command("pfctl", "-X", b.pfToken).Run()
		b.pfToken = ""
	}
	_ = os.Remove(pfTokenPath)
	return err
}

// CleanStale releases a kill-switch stranded by a prior process that exited without
// a graceful Down (crash / kill -9). The crux — flushing the anchor rules — removes
// the block even if the enable-reference can't be identified; releasing the persisted
// token additionally restores pf's prior enable state. Idempotent: a missing token /
// empty anchor is a no-op. This is what un-strands a host after an abnormal exit.
func (b *darwinBackend) CleanStale() error {
	// Flush the block rules FIRST — this is the un-strand. Ignore errors (anchor may
	// be empty / pf disabled — both fine).
	_ = run("pfctl", "-a", pfAnchor, "-F", "all")
	// Release the persisted enable-reference if one survived the crash.
	if tok, err := os.ReadFile(pfTokenPath); err == nil {
		if t := strings.TrimSpace(string(tok)); t != "" {
			_ = exec.Command("pfctl", "-X", t).Run()
		}
		_ = os.Remove(pfTokenPath)
	}
	return nil
}

// routeTargets maps a route destination to the actual OS routes to install. A
// full-tunnel default (0.0.0.0/0 or ::/0) is installed as the WG-standard PAIR of
// half-routes (0.0.0.0/1 + 128.0.0.0/1 ; ::/1 + 8000::/1): they cover all traffic and
// are MORE SPECIFIC than the physical default, so they take precedence WITHOUT
// destroying it. When the utun disappears (Down, crash, kill -9), the halves vanish
// with the interface and the physical default resurfaces automatically — no
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

// splitEndpoint splits host:port, unwrapping a bracketed IPv6 host.
func splitEndpoint(endpoint string) (host, port string) {
	if i := strings.LastIndex(endpoint, ":"); i > 0 {
		host, port = endpoint[:i], endpoint[i+1:]
	} else {
		host = endpoint
	}
	return strings.Trim(host, "[]"), port
}

func ipOnly(cidr string) string {
	if i := strings.Index(cidr, "/"); i >= 0 {
		return cidr[:i]
	}
	return cidr
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runStdin(stdin, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseStats pulls handshake + transfer counters out of an IpcGet dump.
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
