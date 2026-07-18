package dnsforward

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// ── per-source rate limiting (D2 hygiene) ────────────────────────────────────────────────────────────
// A token bucket per source IP: capacity dnsRateBurst, refilled dnsRatePerSec tokens/sec. now() is
// injectable so the red drives the clock without sleeping.

const (
	dnsRateBurst  = 50 // tokens a single source may spend at once
	dnsRatePerSec = 20 // steady-state queries/sec per source
)

type bucket struct {
	tokens float64
	last   time.Time
}

// allow consumes one token for src, refilling by elapsed time. now() defaults to time.Now (test-injectable).
func (f *Forwarder) allow(src netip.Addr) bool {
	now := time.Now
	if f.now != nil {
		now = f.now
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.buckets[src]
	t := now()
	if b == nil {
		b = &bucket{tokens: dnsRateBurst, last: t}
		f.buckets[src] = b
	}
	b.tokens += t.Sub(b.last).Seconds() * dnsRatePerSec
	if b.tokens > dnsRateBurst {
		b.tokens = dnsRateBurst
	}
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// udpExchange relays a raw query to resolver:53 over UDP with a short deadline, returning the raw response.
// A timeout/unreachable resolver -> error -> the caller answers SERVFAIL (fail-static).
func udpExchange(resolver netip.Addr, query []byte) ([]byte, error) {
	c, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(resolver, 53)))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// wgBindAddrs returns the DNS listen addresses — the wg INTERFACE's own addresses ONLY (D2 bind scope). The
// forwarder must NEVER bind a public interface (an open resolver on a cloud VM is an abuse vector), so the
// bind set is derived from the wg iface alone; a non-wg address can never enter it.
func wgBindAddrs(wgIface string) ([]netip.Addr, error) {
	ifi, err := net.InterfaceByName(wgIface)
	if err != nil {
		return nil, err
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ad, ok := netip.AddrFromSlice(ipn.IP); ok && ad.IsValid() {
				out = append(out, ad.Unmap())
			}
		}
	}
	return out, nil
}

// Serve binds the forwarder to the wg interface's addresses on :53 and answers queries until ctx is done.
// Best-effort per the CONVENIENCE rule (D5): a bind failure is returned but must never fail the agent's
// reconcile — the caller logs and continues (DNS-down is not tunnel-down).
func (f *Forwarder) Serve(ctx context.Context, wgIface string) error {
	binds, err := wgBindAddrs(wgIface)
	if err != nil {
		return err
	}
	if len(binds) == 0 {
		return fmt.Errorf("dnsforward: wg iface %q has no bindable address", wgIface)
	}
	for _, b := range binds {
		pc, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(b, 53)))
		if err != nil {
			if f.log != nil {
				f.log.Warn("dns_forward_bind_failed", "addr", b.String(), "error", err.Error())
			}
			continue // best-effort per bind addr
		}
		go f.serveConn(ctx, pc)
	}
	<-ctx.Done()
	return nil
}

func (f *Forwarder) serveConn(ctx context.Context, pc *net.UDPConn) {
	go func() { <-ctx.Done(); _ = pc.Close() }()
	buf := make([]byte, 1500)
	for {
		n, src, err := pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			return // conn closed (ctx done)
		}
		q := append([]byte(nil), buf[:n]...)
		srcAddr := src.Addr().Unmap()
		go func() {
			if resp := f.handle(q, srcAddr); resp != nil {
				_, _ = pc.WriteToUDPAddrPort(resp, src)
			}
		}()
	}
}
