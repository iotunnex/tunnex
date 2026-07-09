package helper

import (
	"net"
	"testing"
	"time"
)

// trustedResolver reports a caller path inside /app; PathCheckVerifier{InstallDir:"/app"}
// accepts it. untrustedResolver reports one outside.
func trustedResolver(net.Conn) (string, error)   { return "/app/tunnex", nil }
func untrustedResolver(net.Conn) (string, error) { return "/evil/mal", nil }

func newServer(t *testing.T, be Backend, resolve PeerResolver) (*Server, *Supervisor) {
	t.Helper()
	sup := NewSupervisor(be)
	return NewServer(sup, PathCheckVerifier{InstallDir: "/app"}, resolve, AuthModePathCheck), sup
}

func req(verb Verb, cfg *TunnelConfig) *Request {
	return &Request{Version: ProtocolVersion, AuthMode: AuthModePathCheck, Verb: verb, Config: cfg}
}

func TestIPCRoundTrip(t *testing.T) {
	srv, sup := newServer(t, &fakeBackend{}, trustedResolver)
	c1, c2 := net.Pipe()
	go srv.handle(c2)
	defer c1.Close()

	// up
	resp, err := Do(c1, req(VerbTunnelUp, goodConfig()))
	if err != nil || !resp.OK || resp.Status == nil || resp.Status.State != "up" {
		t.Fatalf("up: err=%v resp=%+v", err, resp)
	}
	// status
	resp, err = Do(c1, req(VerbStatus, nil))
	if err != nil || !resp.OK || resp.Status.State != "up" {
		t.Fatalf("status: err=%v resp=%+v", err, resp)
	}
	// down → graceful (restores routing, not a kill-switch)
	resp, err = Do(c1, req(VerbTunnelDown, nil))
	if err != nil || !resp.OK {
		t.Fatalf("down: err=%v resp=%+v", err, resp)
	}
	if sup.State() != StateDown {
		t.Fatalf("want down, got %s", sup.State())
	}
}

func TestIPCUntrustedCallerRejected(t *testing.T) {
	srv, _ := newServer(t, &fakeBackend{}, untrustedResolver)
	c1, c2 := net.Pipe()
	go srv.handle(c2)
	defer c1.Close()

	// The server proactively writes a rejection before reading anything, so we READ.
	var resp Response
	if err := ReadMessage(c1, &resp); err != nil {
		t.Fatalf("read rejection: %v", err)
	}
	if resp.OK || resp.Code != "caller_untrusted" {
		t.Fatalf("want caller_untrusted rejection, got %+v", resp)
	}
}

func TestIPCBadConfigRejectedNoTunnel(t *testing.T) {
	be := &fakeBackend{}
	srv, sup := newServer(t, be, trustedResolver)
	c1, c2 := net.Pipe()
	go srv.handle(c2)
	defer c1.Close()

	bad := goodConfig()
	bad.PrivateKey = "AAAA" // too short
	resp, err := Do(c1, req(VerbTunnelUp, bad))
	if err != nil || resp.OK || resp.Code != "bad_private_key" {
		t.Fatalf("want bad_private_key, got err=%v resp=%+v", err, resp)
	}
	// Nothing touched the backend; still down.
	if be.up != 0 || sup.State() != StateDown {
		t.Fatalf("bad config must not reach the backend: up=%d state=%s", be.up, sup.State())
	}
}

func TestIPCPeerLossFailsClosed(t *testing.T) {
	be := &fakeBackend{fc: make(chan struct{}, 1)}
	srv, sup := newServer(t, be, trustedResolver)
	c1, c2 := net.Pipe()
	go srv.handle(c2)

	if resp, err := Do(c1, req(VerbTunnelUp, goodConfig())); err != nil || !resp.OK {
		t.Fatalf("up: %v %+v", err, resp)
	}
	// App dies: drop the controlling connection while the tunnel is up.
	c1.Close()

	select {
	case <-be.fc:
		// FailClosed fired.
	case <-time.After(2 * time.Second):
		t.Fatal("peer loss did not fail closed within 2s")
	}
	if sup.State() != StateFailed {
		t.Fatalf("want failed after peer loss, got %s", sup.State())
	}
}
