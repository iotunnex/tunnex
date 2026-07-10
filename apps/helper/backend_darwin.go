//go:build darwin

package helper

import (
	"encoding/json"
	"fmt"
	"net"
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

// dnsBackupPath persists each network service's PRIOR DNS setting while a full tunnel
// has hijacked the system resolver, so Down (or a crashed-then-restarted helper's
// CleanStale) can restore it. Same crash-safe pattern as pfTokenPath. Root-only.
const dnsBackupPath = "/var/run/tunnex/dns.json"

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
	// endpointHost is the WG endpoint IP for which a full tunnel pins a host route
	// via the PHYSICAL gateway (so WG's own encrypted packets don't loop back into
	// the tunnel). endpointFam is "-inet"/"-inet6" so Down deletes it correctly.
	endpointHost string
	endpointFam  string
}

// NewBackend returns the macOS tunnel backend.
func NewBackend() Backend { return &darwinBackend{} }

func (b *darwinBackend) Up(cfg *TunnelConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Resolve a hostname endpoint to ONE IP up front so the pf pass rule, the endpoint
	// host-route, and wireguard-go all pin the same address (review #10).
	ep, err := resolveEndpoint(cfg.Endpoint)
	if err != nil {
		return err
	}
	cfg.Endpoint = ep

	// 0) CLEAN stale kill-switch state from a prior FailClosed/crash before (re)arming.
	//    A SPLIT tunnel must NOT inherit a full tunnel's block-all (it wants cleartext
	//    routing) — release it. (Full-tunnel re-arm below is idempotent; the stale
	//    endpoint route is cleared idempotently at the add site in step 3a.)
	if !cfg.FullTunnel {
		_ = b.releasePF()
	}

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
	// 3a) FULL TUNNEL: pin a /32 host route for the WG endpoint via the CURRENT
	//     physical default gateway, BEFORE the default moves onto utun. Without this,
	//     wireguard-go's own OUTER (encrypted) packets to the gateway match the
	//     0.0.0.0/1 tunnel route and loop back into the tunnel — tx explodes, nothing
	//     egresses (what `wg-quick` calls the endpoint route).
	if cfg.FullTunnel {
		if epHost, _ := splitEndpoint(cfg.Endpoint); epHost != "" {
			v6 := strings.Contains(epHost, ":")
			fam := "-inet"
			if v6 {
				fam = "-inet6"
			}
			if gw := gatewayFor(epHost, fam); gw != "" {
				// Idempotent: a prior FailClosed/crash may have left this route (which
				// survives the utun since it's via the PHYSICAL gateway) — delete first
				// so the re-add can't fail "File exists" and block reconnect (review #3).
				_ = run("route", "-q", "delete", fam, "-host", epHost)
				if err := run("route", "-q", "add", fam, "-host", epHost, gw); err != nil {
					dev.Close()
					return fmt.Errorf("pin endpoint route %s via %s: %w", epHost, gw, err)
				}
				b.endpointHost, b.endpointFam = epHost, fam
			}
		}
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

	// 4) FULL TUNNEL: move the system resolver onto the tunnel. Full-tunnel captures
	//    ALL traffic (0.0.0.0/0) AND the kill-switch blocks every non-tunnel egress —
	//    so the machine's existing DHCP/LAN resolver is now UNREACHABLE and name
	//    resolution would die (ping-by-IP works, everything by-name fails). Point every
	//    network service at the config's DNS (reachable through the tunnel), saving the
	//    prior setting so Down/CleanStale restores it. Split tunnel keeps its own DNS
	//    (cfg.DNS is empty there — config.go dnsFor), so this is scoped to full-tunnel.
	if cfg.FullTunnel && len(cfg.DNS) > 0 {
		applyDNS(cfg.DNS)
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
	if b.endpointHost != "" {
		_ = run("route", "-q", "delete", b.endpointFam, "-host", b.endpointHost)
		b.endpointHost, b.endpointFam = "", ""
	}
	// Restore the system resolver a full tunnel hijacked (no-op if none was saved).
	restoreDNS()
	return b.releasePF()
}

// gatewayFor returns the physical next-hop for reaching a SPECIFIC address (v4 or v6
// via fam "-inet"/"-inet6"), read BEFORE the tunnel default is installed. Using the
// route to the endpoint itself (not the default) handles an endpoint reached via a
// non-default route. Empty if on-link / unresolved (then no host route is pinned).
func gatewayFor(ip, fam string) string {
	out, err := exec.Command("route", "-n", "get", fam, ip).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "gateway:" {
			return f[1]
		}
	}
	return ""
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

// --- helpers (shared uapi/MTU/stats/routeTargets live in wgcommon.go) ---

// buildPFRules is the kill-switch ruleset loaded into the anchor. Requirements:
//   - (2) loopback is EXEMPT (pass quick on lo0) — also protects the app's own
//     127.0.0.1 loopback callback flow. (pass quick, NOT set skip — see below.)
//   - (4) `block drop out all` covers BOTH inet and inet6 (unqualified = all AFs);
//     NDP is explicitly passed for v6.
//   - the WG endpoint passes (so handshakes/data reach the gateway); once the
//     tunnel exists, its interface is passed quick so user traffic may leave on it.
//   - (3) DHCP/NDP pass — a DELIBERATE, threat-model-argued exception so long
//     sessions don't lose their lease/neighbor state. Risk: these are local-link
//     UDP/ICMPv6 protocols; the exposure is a local attacker on the same segment
//     spoofing DHCP/RA, which is out of scope for a VPN egress kill-switch (and
//     already a risk pre-VPN). Worth it to avoid the tunnel silently dying on a
//     DHCP renew.
func buildPFRules(endpoint, ifname string) string {
	host, port := splitEndpoint(endpoint)
	var b strings.Builder
	// `set skip on <iface>` is REJECTED inside a pf anchor — `set` options are
	// main-ruleset-only, so pfctl silently DROPS these lines when we load them via
	// `pfctl -a tunnex -f -`. The interface is then NOT skipped: every packet the
	// kernel routes onto utun (i.e. ALL of the user's tunnelled traffic) falls through
	// to `block drop out all` and is dropped BEFORE wireguard-go can read+encrypt it —
	// the tunnel handshakes (that's the outer socket on the physical iface hitting the
	// endpoint pass) but carries no data. Use `pass quick` instead: quick short-circuits
	// ABOVE the block, giving loopback + the tunnel interface the exact bypass that
	// `set skip` was meant to, but in a form an anchor honors.
	b.WriteString("pass quick on lo0 all\n")
	if ifname != "" {
		fmt.Fprintf(&b, "pass quick on %s all\n", ifname)
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
	// Restore the system resolver if a crashed full tunnel left it pointed at the
	// tunnel DNS (same un-strand intent as flushing the pf block above).
	restoreDNS()
	return nil
}


// networkServices returns the ENABLED macOS network services (Wi-Fi, Ethernet, …).
// networksetup's first output line is a header; disabled services are '*'-prefixed.
func networkServices() []string {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var svcs []string
	for i, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue // header, blank, or disabled service
		}
		svcs = append(svcs, line)
	}
	return svcs
}

// currentDNS returns the EXPLICIT DNS servers set on a service, or nil when it is on
// automatic/DHCP ("There aren't any DNS Servers set on <svc>."). nil is meaningful:
// restoreDNS maps it back to `-setdnsservers <svc> empty` (return to automatic).
func currentDNS(svc string) []string {
	out, err := exec.Command("networksetup", "-getdnsservers", svc).Output()
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ip := strings.TrimSpace(line); net.ParseIP(ip) != nil {
			servers = append(servers, ip)
		}
	}
	return servers
}

// applyDNS points EVERY enabled network service at the tunnel resolver, first saving
// each service's prior setting to dnsBackupPath so Down/CleanStale can restore it. The
// backup is written BEFORE any mutation, so a crash mid-apply is still recoverable.
// macOS resolves via the primary service's configured DNS regardless of the utun
// default route, so the resolver must be set on the real services, not the utun.
func applyDNS(servers []string) {
	svcs := networkServices()
	if len(svcs) == 0 {
		return
	}
	backup := make(map[string][]string, len(svcs))
	for _, svc := range svcs {
		backup[svc] = currentDNS(svc) // nil = was automatic/DHCP
	}
	if data, err := json.Marshal(backup); err == nil {
		_ = os.MkdirAll("/var/run/tunnex", 0o755)
		_ = os.WriteFile(dnsBackupPath, data, 0o600)
	}
	for _, svc := range svcs {
		_ = run("networksetup", append([]string{"-setdnsservers", svc}, servers...)...)
	}
}

// restoreDNS puts every service's DNS back to what applyDNS saved (automatic when the
// prior list was empty), then removes the backup. Idempotent: no backup = no-op.
func restoreDNS() {
	data, err := os.ReadFile(dnsBackupPath)
	if err != nil {
		return
	}
	var backup map[string][]string
	if json.Unmarshal(data, &backup) == nil {
		for svc, servers := range backup {
			args := []string{"-setdnsservers", svc}
			if len(servers) == 0 {
				args = append(args, "empty") // back to automatic/DHCP
			} else {
				args = append(args, servers...)
			}
			_ = run("networksetup", args...)
		}
	}
	_ = os.Remove(dnsBackupPath)
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

