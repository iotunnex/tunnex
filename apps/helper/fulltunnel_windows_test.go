//go:build windows

package helper

import (
	"errors"
	"testing"
)

// TestWindowsBackendRefusesFullTunnel is the S6.9 guard's teeth: the Windows backend must
// REFUSE a full-tunnel Up with the typed full_tunnel_unsupported code and arm NOTHING (no
// wintun adapter, no WFP kill-switch) — turning Windows full-tunnel from "unproven" into
// "un-connectable" until the parity + kill-switch-persistence work lands and its kill -9
// pcap passes. The refusal is the FIRST thing Up does, before any privileged action, so it
// is the un-bypassable gate (the server-side devices.Create refusal is the clean client
// error; this is the safety boundary). See docs/windows-fulltunnel-decisions.md.
func TestWindowsBackendRefusesFullTunnel(t *testing.T) {
	wb := &windowsBackend{}
	err := wb.Up(fullConfig())
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "full_tunnel_unsupported" {
		t.Fatalf("windows backend must refuse a full tunnel with full_tunnel_unsupported, got %v", err)
	}
	if wb.armed || wb.dev != nil || wb.tunDev != nil {
		t.Fatalf("a refused full tunnel must arm nothing: armed=%v dev=%v tun=%v", wb.armed, wb.dev, wb.tunDev)
	}
}

// TestWindowsFullTunnelRequiresDNS: with the dev bypass ON, a full tunnel that carries NO
// DNS server is refused (full_tunnel_requires_dns) BEFORE any adapter/WFP state is created —
// otherwise the base WFP block would drop all port-53 with no tunnel resolver = a silent
// total resolution outage (review). Checked before CreateTUN so it needs no wintun driver.
func TestWindowsFullTunnelRequiresDNS(t *testing.T) {
	t.Setenv("TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL", "1") // bypass the S6.9 guard to reach the DNS check
	wb := &windowsBackend{}
	cfg := fullConfig()
	cfg.DNS = nil // no resolver

	err := wb.Up(cfg)
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "full_tunnel_requires_dns" {
		t.Fatalf("full tunnel with no DNS must be refused with full_tunnel_requires_dns, got %v", err)
	}
	if wb.armed || wb.dev != nil || wb.tunDev != nil {
		t.Fatalf("empty-DNS refusal must arm/create nothing: armed=%v dev=%v tun=%v", wb.armed, wb.dev, wb.tunDev)
	}
}
