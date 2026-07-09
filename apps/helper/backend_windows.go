//go:build windows

package helper

import (
	"fmt"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/firewall"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// wintunAdapter is the wintun adapter name the client tunnel uses.
const wintunAdapter = "tunnex"

// windowsBackend implements Backend on Windows: wireguard-go over a wintun adapter,
// a WFP kill-switch (the official wireguard-windows `firewall` package — the exact
// mechanism the WireGuard Windows client uses for "block untunneled traffic"), and
// winipcfg for addressing/routes. It mirrors backend_darwin's ordering and the
// BOUNDED fail-closed model: the WFP sublayer is KERNEL-RESIDENT (survives process
// death); Down and CleanStale remove it by the well-known provider GUID
// (firewall.DisableFirewall — idempotent, no persisted token needed, the GUID IS the
// durable key); the Supervisor's dead-man bounds an un-recovered crash.
type windowsBackend struct {
	mu     sync.Mutex
	dev    *device.Device
	tunDev tun.Device
	luid   uint64
	armed  bool // WFP kill-switch installed (kernel-resident)
}

// NewBackend returns the Windows tunnel backend.
func NewBackend() Backend { return &windowsBackend{} }

func (b *windowsBackend) Up(cfg *TunnelConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()

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
	// process (so wireguard-go's own encrypted packets to the endpoint egress — no
	// route-exclusion needed, unlike macOS), and blocks everything else. The filters
	// are kernel-resident and survive process death; removed by DisableFirewall
	// (Down / CleanStale) via the well-known provider GUID.
	if cfg.FullTunnel {
		if err := firewall.EnableFirewall(luid, false, nil); err != nil {
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

	b.dev, b.tunDev, b.luid = dev, tdev, luid
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
	if b.armed {
		firewall.DisableFirewall()
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
	firewall.DisableFirewall()
	b.armed = false
	return nil
}

func (b *windowsBackend) Stats() (TunnelStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dev == nil {
		return TunnelStatus{State: string(StateDown)}, nil
	}
	get, err := b.dev.IpcGet()
	if err != nil {
		return TunnelStatus{Interface: wintunAdapter}, err
	}
	return parseStats(get, wintunAdapter), nil
}
