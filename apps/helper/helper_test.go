package helper

import "testing"

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

func TestValidateRequest(t *testing.T) {
	base := func() *Request {
		return &Request{Version: ProtocolVersion, AuthMode: AuthModePathCheck, Verb: VerbStatus}
	}
	if err := ValidateRequest(base()); err != nil {
		t.Fatalf("valid status request rejected: %v", err)
	}
	// Version mismatch.
	r := base()
	r.Version = 999
	mustCode(t, ValidateRequest(r), "version_mismatch")
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
	upErr                error
	up, down, failClosed int
	fc                   chan struct{}
}

func (f *fakeBackend) Up(*TunnelConfig) error { f.up++; return f.upErr }
func (f *fakeBackend) Down() error            { f.down++; return nil }
func (f *fakeBackend) FailClosed() error {
	f.failClosed++
	if f.fc != nil {
		select {
		case f.fc <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *fakeBackend) Stats() (TunnelStatus, error) { return TunnelStatus{RxBytes: 1}, nil }

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
	s.OnPeerLost()
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

func mustCode(t *testing.T, err error, code string) {
	t.Helper()
	pe, ok := err.(*ProtocolError)
	if !ok || pe.Code != code {
		t.Fatalf("want code %q, got %v", code, err)
	}
}
