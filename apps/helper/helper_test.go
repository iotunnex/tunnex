package helper

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A valid base64-32 WireGuard key for fixtures (32 zero bytes).
const zeroKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func goodConfig() *TunnelConfig {
	return &TunnelConfig{
		PrivateKey:    zeroKey,
		PeerPublicKey: zeroKey,
		Endpoint:      "vpn.example.com:51820",
		Address:       "10.99.0.2/32",
		AllowedIPs:    []string{"0.0.0.0/0", "::/0"},
		DNS:           []string{"10.99.0.1"},
		MTU:           1420,
	}
}

func TestConfigValidate(t *testing.T) {
	if err := goodConfig().Validate(); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	// Each mutation must fail with its specific stable code.
	cases := []struct {
		name string
		code string
		mut  func(c *TunnelConfig)
	}{
		{"short key", "bad_private_key", func(c *TunnelConfig) { c.PrivateKey = "AAAA" }},
		{"non-base64 key", "bad_peer_key", func(c *TunnelConfig) { c.PeerPublicKey = "!!!!not b64!!!!" }},
		{"empty key", "bad_private_key", func(c *TunnelConfig) { c.PrivateKey = "" }},
		{"endpoint no port", "bad_endpoint", func(c *TunnelConfig) { c.Endpoint = "vpn.example.com" }},
		{"endpoint bad port", "bad_endpoint", func(c *TunnelConfig) { c.Endpoint = "vpn.example.com:0" }},
		{"bad address", "bad_address", func(c *TunnelConfig) { c.Address = "10.99.0.2" }},
		{"empty allowed", "bad_allowed_ips", func(c *TunnelConfig) { c.AllowedIPs = nil }},
		{"bad allowed cidr", "bad_allowed_ips", func(c *TunnelConfig) { c.AllowedIPs = []string{"nope"} }},
		{"bad dns", "bad_dns", func(c *TunnelConfig) { c.DNS = []string{"not-an-ip"} }},
		{"bad mtu", "bad_mtu", func(c *TunnelConfig) { c.MTU = 100 }},
		{"bad keepalive", "bad_keepalive", func(c *TunnelConfig) { c.PersistentKeepalive = -1 }},
		{"endpoint metachars", "bad_endpoint", func(c *TunnelConfig) { c.Endpoint = "a b;c:51820" }},
		{"endpoint loopback", "bad_endpoint", func(c *TunnelConfig) { c.Endpoint = "127.0.0.1:51820" }},
		{"endpoint unspecified", "bad_endpoint", func(c *TunnelConfig) { c.Endpoint = "0.0.0.0:51820" }},
		{"incomplete full tunnel v6 missing", "incomplete_full_tunnel", func(c *TunnelConfig) {
			c.FullTunnel = true
			c.AllowedIPs = []string{"0.0.0.0/0"}
		}},
	}
	for _, tc := range cases {
		c := goodConfig()
		tc.mut(c)
		err := c.Validate()
		if err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		pe, ok := err.(*ProtocolError)
		if !ok || pe.Code != tc.code {
			t.Fatalf("%s: want code %q, got %v", tc.name, tc.code, err)
		}
	}
}

func TestEndpointIPv6(t *testing.T) {
	c := goodConfig()
	c.Endpoint = "[2001:db8::1]:51820"
	if err := c.Validate(); err != nil {
		t.Fatalf("bracketed IPv6 endpoint rejected: %v", err)
	}
	// A bare IPv6 without brackets has no unambiguous port → rejected.
	c.Endpoint = "2001:db8::1:51820"
	if err := c.Validate(); err == nil {
		t.Fatal("bare IPv6 endpoint should be rejected (ambiguous port)")
	}
}

func TestEndpointAndFullTunnel(t *testing.T) {
	// Valid public IP + both default routes under full-tunnel → accepted.
	c := goodConfig()
	c.Endpoint = "203.0.113.10:51820"
	c.FullTunnel = true
	c.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid full-tunnel rejected: %v", err)
	}
	// Split tunnel (FullTunnel=false) with a single subnet is fine.
	c = goodConfig()
	c.FullTunnel = false
	c.AllowedIPs = []string{"10.0.0.0/8"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid split-tunnel rejected: %v", err)
	}
}

func TestPathCheckVerifier(t *testing.T) {
	v := PathCheckVerifier{InstallDirs: []string{"/Applications/Tunnex.app"}}
	if v.Mode() != AuthModePathCheck {
		t.Fatal("mode")
	}
	// Inside → trusted.
	if err := v.Verify("/Applications/Tunnex.app/Contents/MacOS/Tunnex"); err != nil {
		t.Fatalf("in-dir caller rejected: %v", err)
	}
	// The dir itself → trusted.
	if err := v.Verify("/Applications/Tunnex.app"); err != nil {
		t.Fatalf("exact dir rejected: %v", err)
	}
	// Sibling-prefix trap MUST be rejected.
	if err := v.Verify("/Applications/Tunnex.app-evil/x"); err == nil {
		t.Fatal("sibling-prefix caller must be rejected")
	}
	// Outside → rejected.
	if err := v.Verify("/tmp/evil"); err == nil {
		t.Fatal("out-of-dir caller must be rejected")
	}
	// Traversal that escapes → rejected (Clean resolves ..).
	if err := v.Verify("/Applications/Tunnex.app/../evil"); err == nil {
		t.Fatal("traversal escape must be rejected")
	}
	// MULTI-DIR (dev install): a caller inside ANY listed dir is trusted; one inside
	// none is rejected. Lets one helper serve both /usr/local/tunnex (tunnelctl) and
	// the dev Electron binary dir without a manual repoint.
	multi := PathCheckVerifier{InstallDirs: []string{"/usr/local/tunnex", "/repo/node_modules/electron/dist/Electron.app/Contents/MacOS"}}
	if err := multi.Verify("/usr/local/tunnex/tunnelctl"); err != nil {
		t.Fatalf("caller in first dir rejected: %v", err)
	}
	if err := multi.Verify("/repo/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"); err != nil {
		t.Fatalf("caller in second dir rejected: %v", err)
	}
	if err := multi.Verify("/somewhere/else/app"); err == nil {
		t.Fatal("caller in none of the dirs must be rejected")
	}
	// Unresolved peer / unset dir → rejected with codes.
	if err := v.Verify(""); err == nil {
		t.Fatal("empty peer path must be rejected")
	}
	if err := (PathCheckVerifier{}).Verify("/x"); err == nil {
		t.Fatal("unset install dir must be rejected")
	}
}

func TestNegotiate(t *testing.T) {
	// Client meets the enforced minimum → enforce it.
	if m, err := Negotiate(AuthModePathCheck, AuthModePathCheck); err != nil || m != AuthModePathCheck {
		t.Fatalf("path/path: %v %v", m, err)
	}
	// A stronger client is fine; helper still enforces its own mode.
	if m, err := Negotiate(AuthModeCodeSigning, AuthModePathCheck); err != nil || m != AuthModePathCheck {
		t.Fatalf("code/path: %v %v", m, err)
	}
	// Once the helper enforces code_signing, a path_check-only client is REFUSED
	// (the one-way ratchet — no silent downgrade).
	if _, err := Negotiate(AuthModePathCheck, AuthModeCodeSigning); err == nil {
		t.Fatal("path client against code-signing helper must be refused")
	}
	// Unknown modes rejected either side.
	if _, err := Negotiate("wat", AuthModePathCheck); err == nil {
		t.Fatal("unknown client mode must be rejected")
	}
}

// TestPathCheckVerifierCanonicalization guards review #5: a symlinked install dir
// (e.g. pnpm-linked Electron) must trust a caller whose exe resolves under the real
// target, and case must not matter on case-insensitive filesystems.
func TestPathCheckVerifierCanonicalization(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// Create the real exe so it resolves (the peer resolver reports a realpath'd, EXISTING exe).
	exe := filepath.Join(real, "app", "Tunnex")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Install dir given via a SYMLINK; caller resolves to the real path underneath.
	if err := (PathCheckVerifier{InstallDirs: []string{link}}).Verify(exe); err != nil {
		t.Fatalf("symlinked install dir must trust a caller under its resolved target: %v", err)
	}
	// Sibling-prefix trap still rejected after canonicalization.
	if err := (PathCheckVerifier{InstallDirs: []string{real}}).Verify(real + "-evil/x"); err == nil {
		t.Fatal("sibling-prefix caller must still be rejected")
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if err := (PathCheckVerifier{InstallDirs: []string{real}}).Verify(filepath.Join(strings.ToUpper(real), "app", "Tunnex")); err != nil {
			t.Fatalf("case-insensitive FS must trust a case-differing caller path: %v", err)
		}
	}
}

func TestNegotiateVersion(t *testing.T) {
	if err := NegotiateVersion(1, 1); err != nil {
		t.Fatalf("equal versions must pass: %v", err)
	}
	// App newer than the installed helper → the helper is stale (normal upgrade path).
	mustCode(t, NegotiateVersion(2, 1), "helper_outdated")
	// App older than a newer helper → refuse (downgrade-refused ratchet).
	mustCode(t, NegotiateVersion(1, 2), "client_outdated")
}

func TestValidateRequest(t *testing.T) {
	base := func() *Request {
		return &Request{Version: ProtocolVersion, AuthMode: AuthModePathCheck, Verb: VerbStatus}
	}
	if err := ValidateRequest(base()); err != nil {
		t.Fatalf("valid status request rejected: %v", err)
	}
	// Version mismatch — a newer app vs an older helper reports helper_outdated.
	r := base()
	r.Version = ProtocolVersion + 1
	mustCode(t, ValidateRequest(r), "helper_outdated")
	// Unknown verb.
	r = base()
	r.Verb = "delete_everything"
	mustCode(t, ValidateRequest(r), "unknown_verb")
	// tunnel_up without config.
	r = base()
	r.Verb = VerbTunnelUp
	mustCode(t, ValidateRequest(r), "config_required")
	// config on a non-up verb (smuggling) rejected.
	r = base()
	r.Config = goodConfig()
	mustCode(t, ValidateRequest(r), "unexpected_config")
	// Unknown auth mode.
	r = base()
	r.AuthMode = "wat"
	mustCode(t, ValidateRequest(r), "auth_mode_unsupported")
}

// fakeBackend records calls and can be told to error on Up. fc (optional) is
// signaled on FailClosed so a test can wait for an async fail-closed.
type fakeBackend struct {
	upErr                            error
	up, down, failClosed, cleanStale int
	armed                            bool // models the kernel-resident kill-switch
	fc                               chan struct{}
}

func (f *fakeBackend) Up(cfg *TunnelConfig) error {
	f.up++
	if cfg.FullTunnel {
		f.armed = true // full tunnel arms the block FIRST (before the parts that can fail)
	}
	return f.upErr
}
func (f *fakeBackend) Down() error { f.down++; f.armed = false; return nil } // graceful: block released
func (f *fakeBackend) FailClosed() error {
	f.failClosed++
	// Fail-closed KEEPS the block armed (death = enforcement) — does NOT release it.
	if f.fc != nil {
		select {
		case f.fc <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *fakeBackend) CleanStale() error { f.cleanStale++; f.armed = false; return nil } // un-strand
func (f *fakeBackend) Stats() (TunnelStatus, error) { return TunnelStatus{RxBytes: 1}, nil }

// TestDeadManHoldsAfterUpFailure is the guard for the FAIL-OPEN regression (review
// #2): a full-tunnel Up that arms the block then fails must keep the block for the
// FULL dead-man window — not release it on the next tick because the failure path
// forgot to beat() (leaving lastBeat stale/zero).
func TestDeadManHoldsAfterUpFailure(t *testing.T) {
	fb := &fakeBackend{upErr: &ProtocolError{Code: "x", Msg: "endpoint unreachable"}}
	s := NewSupervisor(fb)
	base := time.Unix(1_700_000_000, 0)
	clock := base
	s.now = func() time.Time { return clock }
	s.deadMan = 90 * time.Second

	if err := s.Up(fullConfig()); err == nil {
		t.Fatal("expected up failure")
	}
	if s.State() != StateFailed || !fb.armed {
		t.Fatalf("up-failure must fail closed with the block armed: state=%s armed=%v", s.State(), fb.armed)
	}
	// A tick WELL within the window must NOT release — the block holds (fail-CLOSED).
	clock = base.Add(30 * time.Second)
	if s.CheckDeadMan() {
		t.Fatal("dead-man must NOT fire 30s after an up-failure (fail-OPEN regression)")
	}
	if fb.down != 0 {
		t.Fatal("no release expected inside the window")
	}
	// Past the window → bounded auto-release.
	clock = base.Add(120 * time.Second)
	if !s.CheckDeadMan() || fb.down != 1 {
		t.Fatalf("dead-man must release after the window: fired=%v down=%d", s.State() == StateDown, fb.down)
	}
}

func TestSupervisorFailClosed(t *testing.T) {
	// Backend errors on Up → FailClosed installed, state Failed (NOT Down → no leak).
	fb := &fakeBackend{upErr: &ProtocolError{Code: "x", Msg: "boom"}}
	s := NewSupervisor(fb)
	if err := s.Up(goodConfig()); err == nil {
		t.Fatal("expected up error")
	}
	if s.State() != StateFailed {
		t.Fatalf("want failed, got %s", s.State())
	}
	if fb.failClosed != 1 {
		t.Fatalf("FailClosed must be called on up failure, got %d", fb.failClosed)
	}

	// Happy path up, then app disconnect while up → FAIL CLOSED, not a silent Down.
	fb = &fakeBackend{}
	s = NewSupervisor(fb)
	if err := s.Up(goodConfig()); err != nil {
		t.Fatalf("up: %v", err)
	}
	if s.State() != StateUp {
		t.Fatal("want up")
	}
	if err := s.Up(goodConfig()); err == nil {
		t.Fatal("second up must be already_up")
	}
	s.OnPeerLost(true)
	if s.State() != StateFailed || fb.failClosed != 1 {
		t.Fatalf("peer loss must fail closed: state=%s failClosed=%d", s.State(), fb.failClosed)
	}
	// Graceful Down restores routing (Down, not FailClosed) and is idempotent.
	fb = &fakeBackend{}
	s = NewSupervisor(fb)
	_ = s.Up(goodConfig())
	if err := s.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if s.State() != StateDown || fb.down != 1 || fb.failClosed != 0 {
		t.Fatalf("graceful down: state=%s down=%d failClosed=%d", s.State(), fb.down, fb.failClosed)
	}
	if err := s.Down(); err != nil {
		t.Fatalf("idempotent down: %v", err)
	}
}

func fullConfig() *TunnelConfig {
	c := goodConfig()
	c.FullTunnel = true
	return c
}

// TestSupervisorSelfHeal proves startup recovery (RC1): a kill-switch stranded by a
// PRIOR crashed process is released when a fresh Supervisor self-heals before it
// serves — so a KeepAlive restart un-strands the host instead of re-serving the block.
func TestSupervisorSelfHeal(t *testing.T) {
	fb := &fakeBackend{armed: true} // a block survived a prior crash; no live owner
	s := NewSupervisor(fb)
	if err := s.SelfHeal(); err != nil {
		t.Fatalf("self-heal: %v", err)
	}
	if fb.cleanStale != 1 || fb.armed {
		t.Fatalf("self-heal must release the stale block: cleanStale=%d armed=%v", fb.cleanStale, fb.armed)
	}
	if s.State() != StateDown {
		t.Fatalf("post self-heal state = %s, want down", s.State())
	}
}

// TestSupervisorDeadMan proves the bounded fail-closed (RC1), INDEPENDENT of the
// restart path: a full tunnel whose owner stops heartbeating past the window
// auto-releases the block, so a wedged/crashed app can't strand a live helper's host.
func TestSupervisorDeadMan(t *testing.T) {
	fb := &fakeBackend{}
	s := NewSupervisor(fb)
	base := time.Unix(1_700_000_000, 0)
	clock := base
	s.now = func() time.Time { return clock }
	s.deadMan = 90 * time.Second

	if err := s.Up(fullConfig()); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !fb.armed {
		t.Fatal("full-tunnel up must arm the block")
	}
	// A heartbeat (status) within the window refreshes the deadline → no fire.
	clock = base.Add(60 * time.Second)
	if _, err := s.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}
	clock = base.Add(140 * time.Second) // 80s since the last beat (< 90)
	if s.CheckDeadMan() {
		t.Fatal("dead-man must NOT fire while heartbeats are fresh")
	}
	// Owner socket CLOSES (crash/kill): OnPeerLost(true) fails closed, block STAYS armed
	// (death = enforcement).
	s.OnPeerLost(true)
	if s.State() != StateFailed || !fb.armed {
		t.Fatalf("peer loss must fail closed with the block armed: state=%s armed=%v", s.State(), fb.armed)
	}
	// Past the window with no heartbeat → auto-release (un-strand).
	clock = base.Add(260 * time.Second) // well past any window since the last beat
	if !s.CheckDeadMan() {
		t.Fatal("dead-man must fire once the owner is gone past the window")
	}
	if fb.armed || fb.down != 1 || s.State() != StateDown {
		t.Fatalf("dead-man must release the block: armed=%v down=%d state=%s", fb.armed, fb.down, s.State())
	}
}

// TestDeadManOrphanVsWedge pins the S6.8 split: a DEFINITIVELY-lost owner (OnPeerLost →
// socket closed) releases on the SHORT orphan window, while a still-open connection that
// merely stopped heartbeating waits the full conservative window.
func TestDeadManOrphanVsWedge(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	// --- orphan (socket closed): releases fast, on the SHORT window ---
	fb := &fakeBackend{}
	s := NewSupervisor(fb)
	clock := base
	s.now = func() time.Time { return clock }
	s.deadMan, s.deadManOrphan = 90*time.Second, 12*time.Second
	if err := s.Up(fullConfig()); err != nil {
		t.Fatalf("up: %v", err)
	}
	s.OnPeerLost(true) // socket CLOSED → definitively gone → orphaned; beat resets here
	clock = base.Add(6 * time.Second)
	if s.CheckDeadMan() {
		t.Fatal("orphan must NOT release within the short window (6s < 12s)")
	}
	clock = base.Add(20 * time.Second) // > orphan (12) but < full (90)
	if !s.CheckDeadMan() {
		t.Fatal("orphan MUST release once past the short window, without waiting the full one")
	}
	if fb.armed || s.State() != StateDown {
		t.Fatalf("orphan release must drop the block: armed=%v state=%s", fb.armed, s.State())
	}

	// --- wedge (READ-TIMEOUT, socket still open): OnPeerLost(false) → FULL window ---
	// A wedged-but-alive app trips the helper's 30s read deadline, so OnPeerLost DOES
	// fire — but with definitive=false (the socket did not close). It must NOT be treated
	// as orphaned, or a merely-slow app would be released to cleartext early (review #1).
	fb = &fakeBackend{}
	s = NewSupervisor(fb)
	clock = base
	s.now = func() time.Time { return clock }
	s.deadMan, s.deadManOrphan = 90*time.Second, 12*time.Second
	if err := s.Up(fullConfig()); err != nil {
		t.Fatalf("up: %v", err)
	}
	s.OnPeerLost(false) // read-deadline timeout, NOT a close → not orphaned
	if s.orphaned {
		t.Fatal("a read-timeout (definitive=false) must NOT set orphaned")
	}
	clock = base.Add(30 * time.Second) // > orphan (12), < full (90)
	if s.CheckDeadMan() {
		t.Fatal("a wedged-but-connected owner must NOT release on the short orphan window")
	}
	clock = base.Add(100 * time.Second) // > full (90)
	if !s.CheckDeadMan() {
		t.Fatal("wedge must release once past the full window")
	}

	// --- a fresh Up after an orphan clears the flag (back to the full window) ---
	fb = &fakeBackend{}
	s = NewSupervisor(fb)
	clock = base
	s.now = func() time.Time { return clock }
	s.deadMan, s.deadManOrphan = 90*time.Second, 12*time.Second
	_ = s.Up(fullConfig())
	s.OnPeerLost(true) // orphaned = true
	if err := s.Up(fullConfig()); err != nil {
		t.Fatalf("re-up from failed: %v", err)
	}
	clock = base.Add(30 * time.Second) // > orphan, < full
	if s.CheckDeadMan() {
		t.Fatal("a fresh Up must clear the orphan flag → full window applies again")
	}
}

// TestTickIntervalUsesShorterWindow: the loop must poll fine enough to honor the short
// orphan window (min of the two windows / 3), else a 12s release waits for a 30s tick.
func TestTickIntervalUsesShorterWindow(t *testing.T) {
	s := NewSupervisor(&fakeBackend{})
	s.deadMan, s.deadManOrphan = 90*time.Second, 12*time.Second
	if got, want := s.TickInterval(), 4*time.Second; got != want {
		t.Fatalf("TickInterval = %s, want %s (min(90,12)/3)", got, want)
	}
}

// TestDeadManOrphanClampedToFull: an operator who shortens deadMan below the orphan
// default must not end up with the "definitely gone" path SLOWER than the "maybe slow" one.
func TestDeadManOrphanClampedToFull(t *testing.T) {
	t.Setenv("TUNNEX_DEADMAN", "8s")
	s := NewSupervisor(&fakeBackend{})
	if s.deadManOrphan > s.deadMan {
		t.Fatalf("orphan window %s must be clamped to <= deadMan %s", s.deadManOrphan, s.deadMan)
	}
}

// TestUpUnsupportedFullTunnelIsCleanReject (S6.9b): a backend that refuses full-tunnel
// with full_tunnel_unsupported (the Windows guard, which returns BEFORE arming anything)
// must be a CLEAN rejection — the Supervisor returns the code as-is, stays StateDown, and
// does NOT fail-closed. Otherwise the UI shows a phantom "failed / kill-switch active" for
// a request that blocked nothing.
func TestUpUnsupportedFullTunnelIsCleanReject(t *testing.T) {
	fb := &fakeBackend{upErr: &ProtocolError{Code: "full_tunnel_unsupported", Msg: "not on windows"}}
	s := NewSupervisor(fb)

	err := s.Up(fullConfig())
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "full_tunnel_unsupported" {
		t.Fatalf("want raw full_tunnel_unsupported (not wrapped as tunnel_up_failed), got %v", err)
	}
	if fb.failClosed != 0 {
		t.Fatalf("a pre-arm refusal must NOT fail closed: failClosed=%d", fb.failClosed)
	}
	if s.State() != StateDown {
		t.Fatalf("state must stay down after a clean refusal, got %s", s.State())
	}
}

func mustCode(t *testing.T, err error, code string) {
	t.Helper()
	pe, ok := err.(*ProtocolError)
	if !ok || pe.Code != code {
		t.Fatalf("want code %q, got %v", code, err)
	}
}
