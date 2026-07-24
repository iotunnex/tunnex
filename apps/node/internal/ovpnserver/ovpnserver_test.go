package ovpnserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestMgr builds a Manager over a temp dir with an in-memory process seam (so tests never spawn
// openvpn). It records ensureProc calls so self-heal is observable, and stubs the preconditions
// PRESENT (binary + certs) so the happy-path tests reach ensureProc — the precondition-refusal tests
// flip them explicitly.
func newTestMgr(t *testing.T) (*Manager, *int) {
	t.Helper()
	m := New(t.TempDir())
	starts := 0
	m.ensureProc = func(context.Context, string) error { starts++; return nil }
	m.binaryPresent = func() bool { return true }
	m.certsPresent = func() bool { return true }
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
	cfg := m.serverConfig(nil, nil)
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
	if !strings.Contains(m.serverConfig(nil, nil), "dev "+m.TunName()) {
		t.Fatalf("config `dev` must be TunName()=%q; config:\n%s", m.TunName(), m.serverConfig(nil, nil))
	}
}

// TestServerConfigPushesRoutesAndDNS (S9.1 Part-3 fold) locks the OVPN answer to the static-config
// site-subnet gap: the server PUSHES the org's approved ranges + DNS, so a standard OpenVPN client
// reaches site subnets + resolves cross-site names WITHOUT a client-side edit (unlike a static WG
// config). With no routes/DNS the config emits no push lines (byte-identical to pre-fold).
func TestServerConfigPushesRoutesAndDNS(t *testing.T) {
	m, _ := newTestMgr(t)
	cfg := m.serverConfig([]string{"10.0.0.0/16", "172.31.0.0/16"}, []string{"10.0.0.2"})
	for _, want := range []string{
		`push "route 10.0.0.0 255.255.0.0"`,
		`push "route 172.31.0.0 255.255.0.0"`,
		`push "dhcp-option DNS 10.0.0.2"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("server config must push %q (Part-3: OVPN reaches site subnets server-side); got:\n%s", want, cfg)
		}
	}
	if strings.Contains(m.serverConfig(nil, nil), "push ") {
		t.Fatalf("with no routes/dns the config must emit NO push directives; got:\n%s", m.serverConfig(nil, nil))
	}
}

// TestReconcileRefusesLoudlyWhenBinaryAbsent (4d) locks the precondition-before-exec guard: an
// enabled gateway (pool + roster) whose openvpn BINARY is missing REFUSES — the supervisor never
// spawns (ensureProc not called) and the reason is surfaced on the health surface, not logged.
func TestReconcileRefusesLoudlyWhenBinaryAbsent(t *testing.T) {
	m, starts := newTestMgr(t)
	m.binaryPresent = func() bool { return false }
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24", Clients: []Client{{CommonName: "d", IP: "10.99.0.7"}}})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if *starts != 0 {
		t.Fatalf("the supervisor must NOT spawn when the binary is absent; starts=%d", *starts)
	}
	if m.Health() != HealthBinaryAbsent {
		t.Fatalf("health must surface %q, got %q", HealthBinaryAbsent, m.Health())
	}
}

// TestReconcileRefusesLoudlyWhenCertsAbsent (4d) — same guard for the CA/server material.
func TestReconcileRefusesLoudlyWhenCertsAbsent(t *testing.T) {
	m, starts := newTestMgr(t)
	m.certsPresent = func() bool { return false }
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24"})
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if *starts != 0 {
		t.Fatalf("the supervisor must NOT spawn when certs are absent; starts=%d", *starts)
	}
	if m.Health() != HealthCertsAbsent {
		t.Fatalf("health must surface %q, got %q", HealthCertsAbsent, m.Health())
	}
	// no server.conf written either — refuse is a full stop before any output.
	if _, err := os.Stat(filepath.Join(m.cfgDir, "server.conf")); !os.IsNotExist(err) {
		t.Fatalf("a refused reconcile must write no server.conf; stat err=%v", err)
	}
}

// TestHealthClearsOnRecovery (4d) locks the recovery-clears half: certs appear on a later tick →
// health returns to OK and the supervisor spawns. Refuse-loudly is not sticky.
func TestHealthClearsOnRecovery(t *testing.T) {
	m, starts := newTestMgr(t)
	absent := true
	m.certsPresent = func() bool { return !absent }
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24"})
	_ = m.Reconcile(context.Background())
	if m.Health() != HealthCertsAbsent || *starts != 0 {
		t.Fatalf("expected certs-absent refusal first; health=%q starts=%d", m.Health(), *starts)
	}
	absent = false // certs placed
	_ = m.Reconcile(context.Background())
	if m.Health() != HealthOK {
		t.Fatalf("health must clear to OK on recovery, got %q", m.Health())
	}
	if *starts != 1 {
		t.Fatalf("the supervisor must spawn once preconditions are met; starts=%d", *starts)
	}
}

// TestTunActiveGatesTunPublish (4d, step-5 ordering + sweep-on-death) locks the cross-slice interaction:
// TunActive is true ONLY while the server is up (preconditions met + process asserted) — the agent
// publishes egress.SetOVPNTun(TunName) only then. When the server DIES (certs vanish → refuse), TunActive
// flips false, so the agent publishes SetOVPNTun("") and the Slice-3 sweep-on-departed-tun removes the
// tun's egress rules. Ordering: the tun is never in tunnelIfaces() unless it is actually up.
func TestTunActiveGatesTunPublish(t *testing.T) {
	m, _ := newTestMgr(t)
	// idle → not active.
	m.SetDesired(Desired{})
	_ = m.Reconcile(context.Background())
	if m.TunActive() {
		t.Fatal("idle gateway: TunActive must be false (agent must NOT publish the tun)")
	}
	// serving → active (agent publishes SetOVPNTun(TunName)).
	m.SetDesired(Desired{PoolCIDR: "10.99.0.0/24"})
	_ = m.Reconcile(context.Background())
	if !m.TunActive() {
		t.Fatal("serving gateway: TunActive must be true")
	}
	// the server dies (certs vanish) → refuse → NOT active → agent publishes SetOVPNTun("") → egress
	// sweeps the departed tun (proven at the egress tier by the Slice-3 sweep-on-departed-tun reds).
	m.certsPresent = func() bool { return false }
	_ = m.Reconcile(context.Background())
	if m.TunActive() {
		t.Fatal("after the server dies, TunActive must be false so the agent clears the egress tun")
	}
}
