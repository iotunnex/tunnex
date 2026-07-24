package ovpnserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestMgr builds a Manager over a temp dir with an in-memory process seam (so tests never spawn
// openvpn). It records ensureProc calls so self-heal is observable.
func newTestMgr(t *testing.T) (*Manager, *int) {
	t.Helper()
	m := New(t.TempDir())
	starts := 0
	m.ensureProc = func(context.Context, string) error { starts++; return nil }
	return m, &starts
}

func readCCD(t *testing.T, m *Manager, cn string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.ccdDir, cn))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read ccd %s: %v", cn, err)
	}
	return string(b), true
}

// TestCCDPushesCPAssignedAddress is the D-S9.1-3 red that keeps B1 free: an OVPN client's /32 in the
// CCD is EXACTLY its CP-assigned pool address — the same /32 that is its policy subject in the
// compiled artifact. The server never allocates; it renders the control plane's assignment.
func TestCCDPushesCPAssignedAddress(t *testing.T) {
	m, _ := newTestMgr(t)
	m.SetDesired(Desired{
		PoolCIDR: "10.99.0.0/24",
		Clients:  []Client{{CommonName: "device-alice", IP: "10.99.0.7"}},
	})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	body, ok := readCCD(t, m, "device-alice")
	if !ok {
		t.Fatal("expected a CCD file for the client")
	}
	// the pushed address is the CP-assigned /32 (host + the pool mask), not an allocated one.
	if body != "ifconfig-push 10.99.0.7 255.255.255.0\n" {
		t.Fatalf("CCD must push the CP-assigned /32; got %q", body)
	}
}

// TestSelfAllocationDisabled is the allocator-single-authority red: the server config carries NO
// address-handing directive (`server` / `ifconfig-pool`) — every address comes from the CCD, so
// OpenVPN can never mint one. `ccd-exclusive` additionally REFUSES a client with no CCD entry.
func TestSelfAllocationDisabled(t *testing.T) {
	m, _ := newTestMgr(t)
	cfg := m.serverConfig()
	for _, forbidden := range []string{"\nserver ", "ifconfig-pool"} {
		if strings.Contains(cfg, forbidden) {
			t.Fatalf("server config must NOT self-allocate (found %q):\n%s", forbidden, cfg)
		}
	}
	for _, required := range []string{"dev " + TunName, "client-config-dir", "ccd-exclusive", "topology subnet"} {
		if !strings.Contains(cfg, required) {
			t.Fatalf("server config must contain %q (CCD is the single address authority):\n%s", required, cfg)
		}
	}
}

// TestCCDFullSweepOnDeparture is the CCD-reconcile red: a client that leaves the desired set has its
// CCD file REMOVED — not orphaned (an orphaned ifconfig-push would keep binding a departed identity).
func TestCCDFullSweepOnDeparture(t *testing.T) {
	m, _ := newTestMgr(t)
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24", Clients: []Client{
		{CommonName: "device-a", IP: "10.99.0.7"},
		{CommonName: "device-b", IP: "10.99.0.8"},
	}})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	if _, ok := readCCD(t, m, "device-b"); !ok {
		t.Fatal("device-b CCD should exist after first reconcile")
	}
	// device-b departs.
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24", Clients: []Client{
		{CommonName: "device-a", IP: "10.99.0.7"},
	}})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if _, ok := readCCD(t, m, "device-b"); ok {
		t.Fatal("a departed client's CCD file must be swept, not orphaned")
	}
	if _, ok := readCCD(t, m, "device-a"); !ok {
		t.Fatal("the surviving client's CCD must remain")
	}
}

// TestReconcileSelfHeals is the process-lifecycle red: every reconcile tick (re)asserts the process
// via ensureProc — the config is authoritative, so a dead/absent process is (re)started, never
// assumed running (the wg0 self-heal analog).
func TestReconcileSelfHeals(t *testing.T) {
	m, starts := newTestMgr(t)
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24"})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if *starts != 1 {
		t.Fatalf("reconcile must assert the process (self-heal), starts=%d", *starts)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if *starts != 2 {
		t.Fatalf("each tick re-asserts the process; starts=%d", *starts)
	}
}

// TestIdleWhenNoPool is the zero-config guard: with no pool (OVPN not configured for this gateway),
// Reconcile is a no-op — no process asserted, no CCD dir, so egress sees no tun and the ruleset stays
// byte-identical to a WireGuard-only deployment.
func TestIdleWhenNoPool(t *testing.T) {
	m, starts := newTestMgr(t)
	m.SetDesired(Desired{}) // no pool
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if *starts != 0 {
		t.Fatalf("an unconfigured gateway must not start openvpn; starts=%d", *starts)
	}
	if _, err := os.Stat(m.ccdDir); !os.IsNotExist(err) {
		t.Fatalf("an idle manager must not create the CCD dir; stat err=%v", err)
	}
}

// TestTunNameIsOneTruth pins the one-truth: the tun name the server config pins is exactly what
// TunName() reports (the value threaded to egress.SetOVPNTun). No second source.
func TestTunNameIsOneTruth(t *testing.T) {
	m, _ := newTestMgr(t)
	if !strings.Contains(m.serverConfig(), "dev "+m.TunName()) {
		t.Fatalf("config `dev` must be TunName()=%q; config:\n%s", m.TunName(), m.serverConfig())
	}
}
