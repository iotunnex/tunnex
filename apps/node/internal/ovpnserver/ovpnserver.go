// Package ovpnserver is the agent-owned OpenVPN server lifecycle (S9.1 Slice 3). It is a PARALLEL
// data-plane manager (a sibling to egress + dnsforward, NOT part of the reconcile loop that owns
// wg0) — desired state pushed in via SetDesired on each control-plane fetch, converged on the
// reconcile tick, self-healed, never assumed-in-sync.
//
// Ownership boundaries (the reconcile-ownership + allocator tripwires):
//   - The reconcile loop owns wg0; THIS owns the openvpn process, its config, and its CCD dir. No
//     overlap — the tun interface name is pinned here (TunName) and threaded to egress as the ONE
//     truth (egress.SetOVPNTun). If the observed interface disagrees with the configured name, the
//     CONFIG is authoritative: reconcile re-asserts it (a differently-named interface is drift to be
//     corrected on the next process (re)start, never a truth to adopt).
//   - The address allocator (CP-side) is the SINGLE authority: OpenVPN self-allocation is DISABLED
//     (no `server` / `ifconfig-pool` directive) and every client's fixed /32 is pushed from its
//     CP-assigned address via client-config-dir. This package never mints an address — it renders
//     the one the control plane assigned, so an OVPN client's /32 is the SAME /32 that is its policy
//     subject in the compiled artifact (D-S9.1-3: indistinguishable from a WG device's /32, which is
//     what keeps B1 free).
package ovpnserver

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// TunName is the OpenVPN server's tun interface — pinned in config, and the ONE truth threaded to
// the egress tunnel-ingress set (egress.SetOVPNTun(TunName)). Never re-derived or observed-then-adopted.
const TunName = "tunnex-ovpn"

// Client is one OpenVPN client's desired binding: its cert common name (device identity) and the
// CP-ASSIGNED pool /32 (the allocator is authoritative; this package never allocates).
type Client struct {
	CommonName string
	IP         string // the CP-assigned host address, e.g. "10.99.0.7"
}

// Desired is the full desired state pushed on each control-plane fetch.
type Desired struct {
	PoolCIDR string   // the org device pool (for the CCD ifconfig-push netmask); "" => server idle
	Clients  []Client // the clients homed to this gateway
	// Routes (S9.1 Part-3 fold): the org's approved reachable ranges (site subnets etc.). Unlike a
	// WireGuard static config — which must BAKE routes because official apps do not poll — OpenVPN
	// PUSHES routes dynamically, so an OVPN client reaches site subnets WITHOUT a client-side edit:
	// the server emits `push "route <range>"` and the client installs them on connect. CIDR strings.
	Routes []string
	// DNS (S9.1 Part-3 fold): reachable cross-site DNS resolvers, pushed as `dhcp-option DNS` so name
	// resolution works on a standard OpenVPN client (the WG static-config DNS gap has no OVPN twin).
	DNS []string
}

// Health kinds surfaced to the control plane (D-S9.5-OPTIN c + 4d). These are SURFACED HEALTH, not
// logs: an operator who enables OpenVPN on a gateway missing its material sees WHY on the health
// surface, and the gateway keeps doing everything else correctly (the conntrack_flush_unavailable /
// D4 precedent). "" = healthy.
const (
	HealthOK           = ""
	HealthCertsAbsent  = "ovpn_certs_absent"  // enabled + roster, but ca/server cert/key not placed
	HealthBinaryAbsent = "ovpn_binary_absent" // enabled, but the openvpn binary is not on PATH
)

// Manager owns the openvpn process + its config/CCD. Process control + filesystem are injectable so
// the lifecycle is unit-testable without spawning openvpn or touching a shared FS.
type Manager struct {
	cfgDir  string
	ccdDir  string
	desired atomic.Pointer[Desired]
	health  atomic.Pointer[string]
	serving atomic.Bool // the tun is up (preconditions met + process asserted) — gates egress.SetOVPNTun

	// injectable seams (real implementations wired in New):
	ensureProc func(ctx context.Context, confPath string) error // (re)start the process if not running (self-heal)
	writeFile  func(path string, data []byte) error
	removeFile func(path string) error
	listCCD    func() ([]string, error)
	// PRECONDITION probes (preconditions-before-exec): the supervisor is structurally UNABLE to spawn
	// when either is false — refuse-loudly is a GUARD before ensureProc, never a post-failure handler.
	binaryPresent func() bool // the openvpn binary is on PATH (ships unconditionally, D-S9.5-OPTIN b)
	certsPresent  func() bool // ca.crt + server.crt + server.key are placed at cfgDir
}

// New builds a Manager rooted at cfgDir (server.conf + ccd/ live under it). Process control is a
// stub by default (wired to a real supervisor in main); the FS uses the real filesystem.
func New(cfgDir string) *Manager {
	m := &Manager{
		cfgDir: cfgDir,
		ccdDir: filepath.Join(cfgDir, "ccd"),
	}
	m.ensureProc = func(context.Context, string) error { return nil } // wired in main
	m.writeFile = func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
	m.removeFile = os.Remove
	m.binaryPresent = func() bool { _, err := exec.LookPath("openvpn"); return err == nil }
	m.certsPresent = func() bool {
		for _, f := range []string{"ca.crt", "server.crt", "server.key"} {
			if _, err := os.Stat(filepath.Join(m.cfgDir, f)); err != nil {
				return false
			}
		}
		return true
	}
	m.listCCD = func() ([]string, error) {
		ents, err := os.ReadDir(m.ccdDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		var names []string
		for _, e := range ents {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		return names, nil
	}
	return m
}

// TunName returns the pinned tun interface name — the ONE truth for egress.SetOVPNTun.
func (m *Manager) TunName() string { return TunName }

// SetDesired atomically swaps the desired state (called from the reconcile loop's OnPolicy each tick).
func (m *Manager) SetDesired(d Desired) { m.desired.Store(&d) }

// SetEnsureProc wires the real process supervisor (Supervisor.Ensure) — called only once preconditions
// pass, so the supervisor is structurally unable to crash-loop. Default is a no-op stub (tests).
func (m *Manager) SetEnsureProc(fn func(ctx context.Context, confPath string) error) { m.ensureProc = fn }

// WriteServerMaterial writes the CP-delivered CA + server cert + server KEY to cfgDir (D-S9.6). The key
// is 0600; the certs 0644. Idempotent — re-asserted every tick, so a hand-deleted file heals on the
// next reconcile (like wg0's rules). This is what clears the ovpn_certs_absent precondition.
func (m *Manager) WriteServerMaterial(ca, cert, key string) error {
	if err := os.MkdirAll(m.cfgDir, 0o700); err != nil {
		return err
	}
	files := []struct {
		name string
		data string
		perm os.FileMode
	}{
		{"ca.crt", ca, 0o644},
		{"server.crt", cert, 0o644},
		{"server.key", key, 0o600}, // the private key — restrictive perms, never logged
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(m.cfgDir, f.name), []byte(f.data), f.perm); err != nil {
			return err
		}
	}
	return nil
}

// SweepServerMaterial removes the server cert files (D-S9.6: disable means nothing exists on disk;
// the DB record survives, so re-enable re-delivers the same serial).
func (m *Manager) SweepServerMaterial() {
	for _, f := range []string{"ca.crt", "server.crt", "server.key"} {
		_ = os.Remove(filepath.Join(m.cfgDir, f))
	}
}

// Health returns the surfaced health kind ("" ok, or ovpn_certs_absent / ovpn_binary_absent) — the
// agent reports it so an operator sees WHY an enabled gateway is not serving (surfaced, not logged).
func (m *Manager) Health() string {
	if h := m.health.Load(); h != nil {
		return *h
	}
	return HealthOK
}

func ptr(s string) *string { return &s }

// serverConfig renders the openvpn server.conf. SELF-ALLOCATION IS DISABLED (the allocator tripwire):
// there is deliberately NO `server` or `ifconfig-pool` directive — every client's address comes from
// its per-client CCD ifconfig-push (the CP-assigned /32). `topology subnet` + `client-config-dir` make
// the CCD authoritative. The tun name is pinned (dev TunName) so egress' tunnel-ingress set matches.
func (m *Manager) serverConfig(routes, dns []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dev %s\n", TunName)
	b.WriteString("dev-type tun\n")
	b.WriteString("topology subnet\n")
	// NO `server`/`ifconfig-pool`: self-allocation disabled — addresses come ONLY from the CCD.
	fmt.Fprintf(&b, "client-config-dir %s\n", m.ccdDir)
	b.WriteString("ccd-exclusive\n") // a client with NO CCD entry is REFUSED — belt-and-suspenders on the single-authority rule
	// Trust material (placed by the enrollment/export path; referenced by fixed name here).
	fmt.Fprintf(&b, "ca %s\n", filepath.Join(m.cfgDir, "ca.crt"))
	fmt.Fprintf(&b, "cert %s\n", filepath.Join(m.cfgDir, "server.crt"))
	fmt.Fprintf(&b, "key %s\n", filepath.Join(m.cfgDir, "server.key"))
	fmt.Fprintf(&b, "crl-verify %s\n", filepath.Join(m.cfgDir, "crl.pem")) // S9.1 Slice 5 revocation rides this
	b.WriteString("persist-tun\n")
	b.WriteString("persist-key\n")
	// S9.1 Part-3 fold: PUSH the org's approved ranges + DNS. OpenVPN installs these on the client at
	// connect (dynamic, server-side) — so a standard OpenVPN client reaches site subnets + resolves
	// cross-site names WITHOUT the client-side edit a static WireGuard config would need. Ranges are
	// canonically re-emitted (net + dotted mask) so nothing injects config directives.
	for _, c := range routes {
		if net, mask, ok := routeNetMask(c); ok {
			fmt.Fprintf(&b, "push \"route %s %s\"\n", net, mask)
		}
	}
	for _, d := range dns {
		if a, err := netip.ParseAddr(d); err == nil && a.Is4() {
			fmt.Fprintf(&b, "push \"dhcp-option DNS %s\"\n", a.String())
		}
	}
	return b.String()
}

// routeNetMask converts a CIDR to OpenVPN's `route <network> <netmask>` form (dotted mask, not /bits).
func routeNetMask(cidr string) (net, mask string, ok bool) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return "", "", false
	}
	m, err := poolMask(cidr) // dotted mask from the prefix length
	if err != nil {
		return "", "", false
	}
	return p.Masked().Addr().String(), m, true
}

// ccdEntry renders one client's CCD file: a fixed ifconfig-push from the CP-assigned /32 (never an
// allocated address). mask is the pool subnet's dotted mask (topology subnet needs ip + mask).
func ccdEntry(ip, mask string) string {
	return fmt.Sprintf("ifconfig-push %s %s\n", ip, mask)
}

// Reconcile converges the openvpn server toward desired state: writes the server config, reconciles
// the CCD dir (write desired clients, SWEEP departed ones — no orphans), and self-heals the process.
// Idempotent; safe to call every tick. When the desired state is idle (no pool), it is a no-op (the
// server stays down, egress sees no tun → the zero-config golden holds).
func (m *Manager) Reconcile(ctx context.Context) error {
	d := m.desired.Load()
	if d == nil || d.PoolCIDR == "" {
		m.health.Store(ptr(HealthOK)) // idle / not opted-in on this gateway — nothing to be unhealthy about
		m.serving.Store(false)        // no tun up when idle
		return nil                    // not configured / no pool yet — stay idle
	}
	// PRECONDITIONS BEFORE EXEC (4d, ruled): the supervisor is structurally unable to spawn when the
	// binary or certs are missing — these guards run BEFORE ensureProc, refuse LOUDLY via the health
	// surface (not a log, not a post-failure handler), and the gateway keeps doing everything else.
	// Binary first (nothing to serve without it), then certs.
	if !m.binaryPresent() {
		m.health.Store(ptr(HealthBinaryAbsent))
		m.serving.Store(false) // the tun is NOT up → the agent publishes SetOVPNTun("") → Slice-3 sweep
		return nil
	}
	if !m.certsPresent() {
		m.health.Store(ptr(HealthCertsAbsent))
		m.serving.Store(false)
		return nil
	}
	m.health.Store(ptr(HealthOK)) // preconditions met — clears any prior refusal on recovery
	mask, err := poolMask(d.PoolCIDR)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.ccdDir, 0o700); err != nil {
		return err
	}
	// Server config (idempotent write).
	if err := m.writeFile(filepath.Join(m.cfgDir, "server.conf"), []byte(m.serverConfig(d.Routes, d.DNS))); err != nil {
		return err
	}
	// CCD: desired set, keyed by common name.
	want := map[string]string{} // CN -> rendered CCD body
	for _, c := range d.Clients {
		if c.CommonName == "" || c.IP == "" {
			continue // fail-static: skip a malformed client rather than write a bad ifconfig-push
		}
		want[c.CommonName] = ccdEntry(c.IP, mask)
	}
	// Write/refresh desired CCD files.
	names := make([]string, 0, len(want))
	for cn := range want {
		names = append(names, cn)
	}
	sort.Strings(names) // deterministic write order (steady-state reconcile is byte-stable)
	for _, cn := range names {
		if err := m.writeFile(filepath.Join(m.ccdDir, cn), []byte(want[cn])); err != nil {
			return err
		}
	}
	// FULL-SWEEP: remove CCD files for clients no longer desired (a departed client leaves — no orphan
	// grant-by-address in the server). Mirrors the DOCKER-USER + peer sweep discipline.
	existing, err := m.listCCD()
	if err != nil {
		return err
	}
	for _, name := range existing {
		if _, keep := want[name]; !keep {
			if err := m.removeFile(filepath.Join(m.ccdDir, name)); err != nil {
				return err
			}
		}
	}
	// Self-heal: (re)start the process if it isn't running.
	if err := m.ensureProc(ctx, filepath.Join(m.cfgDir, "server.conf")); err != nil {
		m.serving.Store(false) // spawn failed — the tun is not up
		return err
	}
	m.serving.Store(true) // the process is asserted → the tun is up → the agent may publish SetOVPNTun
	return nil
}

// TunActive reports whether the OpenVPN tun is up (preconditions met + the process asserted). The
// agent publishes egress.SetOVPNTun(TunName) ONLY when this is true — a tunnelIfaces() entry for a
// tun that isn't up would render forward accepts for a non-existent ingress. When the server later
// DIES (TunActive → false), the agent publishes SetOVPNTun("") and the Slice-3 sweep-on-departed-tun
// path removes the tun's egress rules. Cross-slice: ovpnserver liveness drives egress' tunnel set.
func (m *Manager) TunActive() bool { return m.serving.Load() }

// poolMask returns the dotted-decimal netmask for a pool CIDR (topology subnet's ifconfig-push needs it).
func poolMask(cidr string) (string, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil || !p.Addr().Is4() {
		return "", fmt.Errorf("ovpnserver: pool CIDR must be IPv4, got %q", cidr)
	}
	bits := p.Bits()
	var mask [4]byte
	for i := 0; i < 4; i++ {
		n := bits - i*8
		switch {
		case n >= 8:
			mask[i] = 0xff
		case n <= 0:
			mask[i] = 0x00
		default:
			mask[i] = byte(0xff << (8 - n))
		}
	}
	return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3]), nil
}
