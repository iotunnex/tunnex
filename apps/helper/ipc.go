package helper

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
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

// Server runs the helper's request loop for one listener. It authenticates each
// connection's caller BEFORE any dispatch, enforces the negotiated auth mode, and
// — critically — FAILS CLOSED if the controlling app disconnects while a tunnel is
// up (the connection owning the tunnel dropping is treated as app death).
type Server struct {
	sup      *Supervisor
	verify   CallerVerifier
	resolve  PeerResolver
	enforced AuthMode
}

// NewServer wires the dispatch dependencies. enforced is the auth mode this helper
// requires (path_check now; code_signing at S6.5b).
func NewServer(sup *Supervisor, verify CallerVerifier, resolve PeerResolver, enforced AuthMode) *Server {
	return &Server{sup: sup, verify: verify, resolve: resolve, enforced: enforced}
}

// Serve accepts connections until the listener closes. Each connection is handled
// serially in its own goroutine.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

// handle authenticates then serves one connection. On loop exit — client EOF, a
// read error, or a malformed frame — it fails the tunnel closed IF this connection
// left one up (app death must not silently drop protection to cleartext).
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

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
		var req Request
		if err := ReadMessage(conn, &req); err != nil {
			// Connection ended. If it owned a live tunnel, fail closed.
			s.sup.OnPeerLost()
			return
		}
		if err := WriteMessage(conn, s.dispatch(&req)); err != nil {
			s.sup.OnPeerLost()
			return
		}
	}
}

// dispatch validates the envelope, enforces auth mode, and runs the verb. It never
// panics on bad input — every failure is a typed Response with a stable code.
func (s *Server) dispatch(req *Request) *Response {
	if err := ValidateRequest(req); err != nil {
		return errorResponse(codeOf(err), err.Error())
	}
	if _, err := Negotiate(req.AuthMode, s.enforced); err != nil {
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
