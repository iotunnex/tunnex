package helper

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// MaxMessageBytes caps a single framed message so a hostile/confused caller can't
// make the root helper allocate unboundedly. WireGuard configs are tiny; 64 KiB
// is generous.
const MaxMessageBytes = 64 * 1024

// Framing: a 4-byte big-endian length prefix followed by that many JSON bytes.
// Simple, language-neutral (the Electron main side speaks the same framing in TS).

// WriteMessage frames and writes v as length-prefixed JSON.
func WriteMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxMessageBytes {
		return errors.New("message exceeds MaxMessageBytes")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// ReadMessage reads one length-prefixed JSON message into v, rejecting oversize
// frames before allocating the body.
func ReadMessage(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxMessageBytes {
		return errors.New("incoming message exceeds MaxMessageBytes")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// PeerResolver resolves the on-disk executable path of the process on the other
// end of conn. It is PLATFORM-SPECIFIC (macOS: audit token → pid → path; Windows:
// GetNamedPipeClientProcessId → image path) and injected into the Server so the
// dispatch logic stays pure + testable. A resolver that cannot determine the peer
// MUST return an error — the Server then refuses the caller (fail closed).
type PeerResolver func(conn net.Conn) (exePath string, err error)

// Default connection bounds. The read deadline doubles as the app-LIVENESS
// timeout: the app holds ONE control connection open and must send a request (a
// status heartbeat suffices, and the UI wants live stats anyway) within this
// window; silence past it means the app crashed/hung → the owner connection is
// dropped → fail closed. It also defeats slow-loris against the root process.
const (
	defaultReadTimeout  = 30 * time.Second
	defaultWriteTimeout = 10 * time.Second
	defaultMaxConns     = 16
)

// Server runs the helper's request loop for one listener. It authenticates each
// connection's caller BEFORE any dispatch, enforces the mode of its ACTUAL
// verifier, and — critically — FAILS CLOSED only when the connection that OWNS the
// live tunnel goes away (crash/hang/close-without-down). A benign second
// connection (e.g. a status poll) closing never tears down a tunnel another
// connection owns.
type Server struct {
	sup     *Supervisor
	verify  CallerVerifier
	resolve PeerResolver

	readTimeout  time.Duration
	writeTimeout time.Duration
	sem          chan struct{} // caps concurrent connections against a local flood

	mu    sync.Mutex
	owner net.Conn // the connection that brought the current tunnel up (nil if down)
}

// NewServer wires the dispatch dependencies. The enforced auth mode is the mode of
// the verifier itself (path_check now; code_signing at S6.5b) — there is no
// separate knob to drift out of sync with the real check.
func NewServer(sup *Supervisor, verify CallerVerifier, resolve PeerResolver) *Server {
	return &Server{
		sup:          sup,
		verify:       verify,
		resolve:      resolve,
		readTimeout:  defaultReadTimeout,
		writeTimeout: defaultWriteTimeout,
		sem:          make(chan struct{}, defaultMaxConns),
	}
}

// Serve accepts connections until the listener closes. Each is handled in its own
// goroutine, bounded by the connection semaphore (excess connections are refused,
// not queued, so a local flood can't exhaust the root process).
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.handle(conn)
			}()
		default:
			_ = conn.Close() // at capacity — refuse
		}
	}
}

// handle authenticates then serves one connection. A recover() makes a single bad
// connection unable to crash the root helper. On loop exit it fails the tunnel
// closed IFF this connection OWNED a live tunnel.
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	// recover first (runs last, catching panics from the loop), then owner cleanup.
	defer s.onClose(conn)
	defer func() { _ = recover() }()

	exe, err := s.resolve(conn)
	if err != nil {
		_ = WriteMessage(conn, errorResponse("peer_unresolved", "could not identify the caller"))
		return
	}
	if err := s.verify.Verify(exe); err != nil {
		_ = WriteMessage(conn, errorResponse(codeOf(err), "caller not trusted"))
		return
	}

	for {
		_ = conn.SetReadDeadline(time.Now().Add(s.readTimeout))
		var req Request
		if err := ReadMessage(conn, &req); err != nil {
			return // deferred onClose fails closed if this conn owned the tunnel
		}
		resp := s.dispatch(&req)
		// Ownership follows a successful up/down so only the owner's loss matters.
		if resp.OK {
			switch req.Verb {
			case VerbTunnelUp:
				s.setOwner(conn)
			case VerbTunnelDown:
				s.clearOwner(conn)
			}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
		if err := WriteMessage(conn, resp); err != nil {
			return
		}
	}
}

func (s *Server) setOwner(conn net.Conn) {
	s.mu.Lock()
	s.owner = conn
	s.mu.Unlock()
}

func (s *Server) clearOwner(conn net.Conn) {
	s.mu.Lock()
	if s.owner == conn {
		s.owner = nil
	}
	s.mu.Unlock()
}

// onClose fails the tunnel closed only if the closing connection is the owner —
// app death (crash/hang/close-without-down) must not silently drop protection, but
// a non-owner connection closing must not disturb a live tunnel.
func (s *Server) onClose(conn net.Conn) {
	s.mu.Lock()
	isOwner := s.owner == conn
	if isOwner {
		s.owner = nil
	}
	s.mu.Unlock()
	if isOwner {
		s.sup.OnPeerLost() // no-op unless a tunnel is actually up
	}
}

// dispatch validates the envelope, enforces auth mode (of the actual verifier), and
// runs the verb. It never panics on bad input — every failure is a typed Response
// with a stable code.
func (s *Server) dispatch(req *Request) *Response {
	if err := ValidateRequest(req); err != nil {
		return errorResponse(codeOf(err), err.Error())
	}
	if _, err := Negotiate(req.AuthMode, s.verify.Mode()); err != nil {
		return errorResponse(codeOf(err), err.Error())
	}
	switch req.Verb {
	case VerbTunnelUp:
		if err := s.sup.Up(req.Config); err != nil {
			return errorResponse(codeOf(err), err.Error())
		}
		st, _ := s.sup.Status()
		return okResponse(&st)
	case VerbTunnelDown:
		if err := s.sup.Down(); err != nil {
			return errorResponse(codeOf(err), err.Error())
		}
		return okResponse(nil)
	case VerbStatus:
		st, err := s.sup.Status()
		if err != nil {
			return errorResponse(codeOf(err), err.Error())
		}
		return okResponse(&st)
	default:
		return errorResponse("unknown_verb", "unknown verb")
	}
}

func okResponse(st *TunnelStatus) *Response {
	return &Response{Version: ProtocolVersion, OK: true, Status: st}
}

func errorResponse(code, msg string) *Response {
	return &Response{Version: ProtocolVersion, OK: false, Code: code, Error: msg}
}

// codeOf extracts a ProtocolError's stable code, defaulting to "internal".
func codeOf(err error) string {
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return "internal"
}

// Do is a minimal request/response client round-trip over conn — used by tests and
// callers that speak Go (the Electron main side speaks the same framing in TS).
func Do(conn net.Conn, req *Request) (*Response, error) {
	if err := WriteMessage(conn, req); err != nil {
		return nil, err
	}
	var resp Response
	if err := ReadMessage(conn, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
