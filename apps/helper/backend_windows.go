//go:build windows

package helper

import (
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"

	"github.com/tunnexio/tunnex/apps/helper/internal/wfp"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// wintunAdapter is the wintun adapter name the client tunnel uses.
const wintunAdapter = "tunnex"

// windowsFullTunnelAllowed reports whether the S6.9 Windows full-tunnel guard is bypassed
// by the LOUD dev flag TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL=1. This exists ONLY to box-prove
// Story A (traffic correctness) while the guard keeps production users safe. It is UNSAFE:
// the WFP kill-switch does NOT survive process death until Story B (S6.7) lands, so a hard
// kill can leak. Stats() reports unsafe_dev_mode when active so the app shows a banner. This
// flag AND the guard are removed together when Story B's kill -9 pcap passes — enforced by
// TestWindowsBypassFlagRequiresGuard so the flag can't silently outlive the guard.
func windowsFullTunnelAllowed() bool {
	return os.Getenv("TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL") == "1"
}

// dnsAddrs parses the config DNS strings into netip.Addr, split by family (v4/v6) plus a
// combined list. Invalid entries are skipped (Validate already accepted the config).
func dnsAddrs(dns []string) (v4, v6, all []netip.Addr) {
	for _, s := range dns {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			continue
		}
		all = append(all, a)
		if a.Is4() {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	return
}

// hasV6Default reports whether ::/0 is among the AllowedIPs (full v6 tunnel).
func hasV6Default(allowedIPs []string) bool {
	for _, a := range allowedIPs {
		if strings.TrimSpace(a) == "::/0" {
			return true
		}
	}
	return false
}

// windowsBackend implements Backend on Windows: wireguard-go over a wintun adapter, a WFP
// kill-switch (the vendored internal/wfp — the wireguard-windows filter set on a S6.7 PERSISTENT
// non-dynamic session + fixed provider GUID, so the block genuinely survives process death), and
// winipcfg for addressing/routes. It mirrors backend_darwin's ordering and the BOUNDED
// fail-closed model: the WFP objects are KERNEL-RESIDENT and outlive the process; Down / CleanStale
// / `tunnex-helper --wfp-clean` remove them by enumerate-and-delete under our fixed provider GUID
// (idempotent, no persisted token — the GUID IS the durable key); the Supervisor's dead-man + the
// auto-start service's startup CleanStale + reboot bound an un-recovered crash.
type windowsBackend struct {
	mu     sync.Mutex
	dev    *device.Device
	tunDev tun.Device
	luid   uint64
	armed  bool // WFP kill-switch installed (kernel-resident)
	// Endpoint host-route pinned on the PHYSICAL interface (so WG's own encrypted
	// packets don't loop into the tunnel); removed on Down.
	epDest   netip.Prefix
	epLUID   winipcfg.LUID
	epNH     netip.Addr
	epPinned bool
}

// NewBackend returns the Windows tunnel backend.
func NewBackend() Backend { return &windowsBackend{} }

func (b *windowsBackend) Up(cfg *TunnelConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// S6.9 GUARD (+ S6.10 dev bypass): full tunnel is NOT yet safe on Windows — Story A
	// adds the DNS correctness below, but the WFP block still does not survive process
	// death until Story B (S6.7). So refuse full tunnel UNLESS the loud dev flag is set
	// (box-proving Story A while production users stay guarded — see
	// docs/windows-fulltunnel-decisions.md). Remove the flag AND this guard together when
	// Story B's kill -9 pcap passes.
	if cfg.FullTunnel && !windowsFullTunnelAllowed() {
		return &ProtocolError{Code: "full_tunnel_unsupported", Msg: "full tunnel is not available on Windows yet"}
	}

	// Parse the tunnel DNS UP FRONT (before creating any adapter/WFP state, so an invalid
	// config fails cleanly). Used for BOTH EnableFirewall's restrictToDNSServers (blockDNS)
	// and the adapter SetDNS below. useV6DNS gates the v6 SetDNS AND the v6 WFP-permit
	// together, so the permitted resolver set exactly matches what the adapter uses (no v6
	// resolver permitted-but-never-set); v6 DNS only when v6 is tunnelled.
	dnsV4, dnsV6, _ := dnsAddrs(cfg.DNS)
	useV6DNS := len(dnsV6) > 0 && hasV6Default(cfg.AllowedIPs)
	dnsRestrict := append([]netip.Addr{}, dnsV4...)
	if useV6DNS {
		dnsRestrict = append(dnsRestrict, dnsV6...)
	}
	// A full tunnel with NO usable DNS server would block ALL DNS (the base WFP block drops
	// port 53) with no tunnel resolver set → a silent, total resolution outage. Refuse up
	// front. config.go always supplies one; this guards the invariant so a future or
	// hand-crafted empty-DNS full-tunnel config can't blackhole resolution (review).
	if cfg.FullTunnel && len(dnsRestrict) == 0 {
		return &ProtocolError{Code: "full_tunnel_requires_dns", Msg: "full tunnel requires a DNS server"}
	}

	// Resolve a hostname endpoint to ONE IP so the WFP pass, the endpoint route, and
	// wireguard-go all pin the same address (review #10).
	ep, err := resolveEndpoint(cfg.Endpoint)
	if err != nil {
		return err
	}
	cfg.Endpoint = ep

	// CLEAN any stale Tunnex WFP kill-switch (S6.7: PERSISTENT — survives a prior crash) before
	// (re)arming: a re-arm with our fixed GUID would else fail ALREADY_EXISTS, and a stale
	// full-tunnel block must not persist under a new SPLIT tunnel. Idempotent enumerate-and-delete.
	wfp.DisableFirewall()
	b.armed = false

	tdev, err := tun.CreateTUN(wintunAdapter, deviceMTU(cfg))
	if err != nil {
		return fmt.Errorf("create wintun adapter: %w", err)
	}
	nt, ok := tdev.(*tun.NativeTun)
	if !ok {
		_ = tdev.Close()
		return fmt.Errorf("wintun: unexpected tun device type %T", tdev)
	}
	luid := nt.LUID()

	// ARM the kill-switch FIRST (before any routing) — ONLY for a full tunnel (a
	// split tunnel leaves the rest of the user's traffic on the cleartext default BY
	// DESIGN). EnableFirewall permits the tunnel adapter, loopback, DHCP/NDP, and THIS
	// process, and blocks everything else. NOTE: the WFP permit lets our encrypted
	// packets PASS the filter, but it does NOT stop the routing table from sending them
	// into the tunnel — so a full tunnel STILL needs the physical endpoint route pinned
	// below (review #1). The filters are kernel-resident and survive process death;
	// removed by DisableFirewall (Down / CleanStale) via the well-known provider GUID.
	// Parse the tunnel DNS once (used for BOTH the WFP DNS-restrict below and SetDNS after
	// addressing). Story A: passing these as EnableFirewall's restrictToDNSServers runs its
	// blockDNS so DNS can ONLY egress to the tunnel resolver (the current nil skipped it →
	// a leak out other NICs). The 2nd param STAYS false (doNotRestrict — true would disarm
	// the whole kill-switch); the fix is the 3rd param.
	if cfg.FullTunnel {
		if err := wfp.EnableFirewall(luid, false, dnsRestrict); err != nil {
			_ = tdev.Close()
			return fmt.Errorf("arm kill-switch (WFP): %w", err)
		}
		b.armed = true
	}

	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "tunnex-helper: "))
	uapi, err := uapiConfig(cfg)
	if err != nil {
		dev.Close() // keeps the WFP block armed (fail-closed); the Supervisor → FailClosed
		return err
	}
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return fmt.Errorf("configure device: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return fmt.Errorf("device up: %w", err)
	}

	// Address + routes on the adapter. Split-default for full-tunnel so the physical
	// default is NEVER destroyed (auto-recovers on teardown/crash). On-link via the
	// adapter (next-hop unspecified).
	wl := winipcfg.LUID(luid)
	addr, err := netip.ParsePrefix(cfg.Address)
	if err != nil {
		dev.Close()
		return fmt.Errorf("parse address %q: %w", cfg.Address, err)
	}
	if err := wl.SetIPAddresses([]netip.Prefix{addr}); err != nil {
		dev.Close()
		return fmt.Errorf("set address: %w", err)
	}
	for _, aip := range cfg.AllowedIPs {
		for _, target := range routeTargets(aip) {
			dest, perr := netip.ParsePrefix(target)
			if perr != nil {
				dev.Close()
				return fmt.Errorf("parse route %q: %w", target, perr)
			}
			nh := netip.IPv4Unspecified()
			if dest.Addr().Is6() {
				nh = netip.IPv6Unspecified()
			}
			if err := wl.AddRoute(dest, nh, 0); err != nil {
				dev.Close()
				return fmt.Errorf("route %s: %w", target, err)
			}
		}
	}

	// Pin the endpoint route on the PHYSICAL interface (review #1): with 0.0.0.0/1 on
	// the tunnel, wireguard-go's own encrypted packets to the endpoint would otherwise
	// match the tunnel route and loop — conn.NewDefaultBind() does NOT bind the socket
	// to the physical NIC, so the loop is real, not merely theoretical.
	if cfg.FullTunnel {
		if epHost, _ := splitEndpoint(cfg.Endpoint); epHost != "" {
			if ip, perr := netip.ParseAddr(epHost); perr == nil {
				if err := b.pinEndpointRoute(ip, luid); err != nil {
					dev.Close()
					return err
				}
			}
		}
	}

	// FULL TUNNEL: point the system resolver at the tunnel DNS ON THE WINTUN ADAPTER
	// (Story A). Adapter-scoped, so it AUTO-VANISHES when the adapter is torn down (Down /
	// crash) — no backup/restore/strand, unlike macOS's per-service networksetup. Without
	// this the OS keeps resolving via the (now WFP-blocked) LAN DNS and names don't
	// resolve. v6 DNS only when a v6 default is tunnelled (else v6 stays dropped — no
	// NAT66, matching macOS). domains=nil (full tunnel = all DNS to the resolver).
	if cfg.FullTunnel {
		if len(dnsV4) > 0 {
			if err := wl.SetDNS(winipcfg.AddressFamily(windows.AF_INET), dnsV4, nil); err != nil {
				dev.Close()
				return fmt.Errorf("set tunnel DNS (v4): %w", err)
			}
		}
		if useV6DNS {
			if err := wl.SetDNS(winipcfg.AddressFamily(windows.AF_INET6), dnsV6, nil); err != nil {
				dev.Close()
				return fmt.Errorf("set tunnel DNS (v6): %w", err)
			}
		}
	}

	b.dev, b.tunDev, b.luid = dev, tdev, luid
	return nil
}

// pinEndpointRoute installs a host route for the endpoint via the PHYSICAL default
// next-hop (the lowest-metric default route NOT on the tunnel adapter), so WG's own
// encrypted packets egress physically instead of matching the tunnel's split-default.
// Idempotent (a prior FailClosed/crash may have left it — the route is on the physical
// interface and survives the wintun teardown, like the macOS endpoint route).
func (b *windowsBackend) pinEndpointRoute(ep netip.Addr, tunnelLUID uint64) error {
	fam := winipcfg.AddressFamily(windows.AF_INET)
	bits := 32
	if ep.Is6() {
		fam = winipcfg.AddressFamily(windows.AF_INET6)
		bits = 128
	}
	rows, err := winipcfg.GetIPForwardTable2(fam)
	if err != nil {
		return fmt.Errorf("read routing table: %w", err)
	}
	best := ^uint32(0)
	var nh netip.Addr
	var luid winipcfg.LUID
	found := false
	for i := range rows {
		r := &rows[i]
		if r.DestinationPrefix.PrefixLength != 0 || uint64(r.InterfaceLUID) == tunnelLUID {
			continue // default routes NOT on the tunnel adapter
		}
		if r.Metric < best {
			best, nh, luid, found = r.Metric, r.NextHop.Addr(), r.InterfaceLUID, true
		}
	}
	if !found {
		return nil // no physical default — nothing to loop through
	}
	dest := netip.PrefixFrom(ep, bits)
	_ = luid.DeleteRoute(dest, nh) // idempotent
	if err := luid.AddRoute(dest, nh, 0); err != nil {
		return fmt.Errorf("pin endpoint route %s: %w", dest, err)
	}
	b.epDest, b.epLUID, b.epNH, b.epPinned = dest, luid, nh, true
	return nil
}

func (b *windowsBackend) Down() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Graceful: closing the adapter drops its addresses + routes (the physical default
	// was never destroyed → normal routing returns), THEN remove the WFP block LAST so
	// no window reverts to cleartext with the block already gone.
	if b.dev != nil {
		b.dev.Close()
	}
	b.dev, b.tunDev, b.luid = nil, nil, 0
	if b.epPinned {
		_ = b.epLUID.DeleteRoute(b.epDest, b.epNH) // on the physical iface → not auto-removed
		b.epPinned = false
	}
	if b.armed {
		wfp.DisableFirewall()
		b.armed = false
	}
	return nil
}

func (b *windowsBackend) FailClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Alive-process fast path: tear the adapter; the WFP block STAYS armed (it was
	// installed at Up and outlives this process). On kill -9 this never runs; the
	// kernel-resident filters hold the line until the next start's CleanStale or the
	// dead-man releases them.
	if b.dev != nil {
		b.dev.Close()
		b.dev, b.tunDev, b.luid = nil, nil, 0
	}
	return nil
}

func (b *windowsBackend) CleanStale() error {
	// Release a WFP kill-switch stranded by a PRIOR process that exited without a
	// graceful Down (crash / kill). DisableFirewall removes every object under our
	// well-known provider GUID — idempotent, no persisted token needed. This is the
	// startup self-heal that un-strands a Windows host after an abnormal exit.
	b.mu.Lock()
	defer b.mu.Unlock()
	// S6.7: remove the PERSISTENT Tunnex WFP block a prior crash / --wfp-arm-test left. This is the
	// reboot/startup RECOVERY — the service is auto-start, so a boot runs this before serving and
	// un-wedges the host. Same code path as the --wfp-clean escape hatch.
	_ = wfp.Clean()
	b.armed = false
	return nil
}

func (b *windowsBackend) Stats() (TunnelStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dev == nil {
		return TunnelStatus{State: string(StateDown)}, nil
	}
	// UnsafeDevMode reflects a STANDING condition (a full tunnel armed under the dev flag),
	// not a per-stat value — so it must be reported even when IpcGet fails, or the loud
	// banner would be missing right after connect if the first stats read errors (review).
	unsafe := b.armed && windowsFullTunnelAllowed()
	get, err := b.dev.IpcGet()
	if err != nil {
		return TunnelStatus{Interface: wintunAdapter, UnsafeDevMode: unsafe}, err
	}
	st := parseStats(get, wintunAdapter)
	st.UnsafeDevMode = unsafe
	return st, nil
}
