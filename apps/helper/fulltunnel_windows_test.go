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
