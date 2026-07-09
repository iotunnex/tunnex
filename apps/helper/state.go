package helper

import "sync"

// TunnelState is the helper's view of the data plane.
type TunnelState string

const (
	StateDown   TunnelState = "down"   // no tunnel; normal routing
	StateUp     TunnelState = "up"     // tunnel active
	StateFailed TunnelState = "failed" // tunnel lost + FAIL-CLOSED kill-switch installed
)

// Backend is the platform tunnel implementation (wireguard-go on macOS,
// wireguard-nt on Windows). The core dispatches through it; the platform files
// provide it.
//
// KILL-SWITCH INVARIANT (see PLAN "S6.3 KILL-SWITCH DESIGN"): fail-closed must
// require NO LIVE CODE to act, because the helper can be `kill -9`'d (no handlers
// run). So the kill-switch is KERNEL-RESIDENT STATE (pf anchor on macOS, WFP
// filters on Windows) that Up ARRANGES and that PERSISTS however the process
// exits. Death itself enforces no-cleartext.
//
//   - Up(cfg)      bring the tunnel up AND arrange the persistent kill-switch
//     (block all egress except via the tunnel / to the WG endpoint). The block
//     outlives the process; only Down removes it.
//   - Down()       graceful: remove the interface AND remove the kill-switch —
//     restore normal routing (the user clicked Disconnect and wants cleartext back).
//   - FailClosed() alive-process FAST PATH (app died / a mid-up error): tear the
//     interface and ASSERT the kill-switch is present. It does NOT "install" the
//     guarantee — Up already did; this is convenience for when code is still
//     running. On `kill -9` this never runs, and the pre-arranged block is what
//     holds the line.
type Backend interface {
	Up(cfg *TunnelConfig) error
	Down() error
	FailClosed() error
	Stats() (TunnelStatus, error)
}

// Supervisor owns the tunnel lifecycle and enforces the FAIL-CLOSED invariant: any
// unexpected loss of the tunnel — a backend error mid-bring-up, or the IPC channel
// to the app dropping while up (helper death is handled by the OS keeping the
// interface down; app death is handled here) — transitions to a kill-switched
// Failed state, never to a silent Down that would leak. It is safe for concurrent
// use.
type Supervisor struct {
	mu      sync.Mutex
	be      Backend
	state   TunnelState
	lastCfg *TunnelConfig
}

func NewSupervisor(be Backend) *Supervisor {
	return &Supervisor{be: be, state: StateDown}
}

func (s *Supervisor) State() TunnelState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Up validates then brings the tunnel up. On ANY backend failure it fails closed
// (kill-switch) rather than leaving traffic on the cleartext default route.
func (s *Supervisor) Up(cfg *TunnelConfig) error {
	if err := cfg.Validate(); err != nil {
		return err // nothing touched yet — plain Down, no kill-switch needed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateUp {
		return &ProtocolError{Code: "already_up", Msg: "a tunnel is already up"}
	}
	if err := s.be.Up(cfg); err != nil {
		// Partial bring-up may have created an interface/routes: fail closed so a
		// half-configured tunnel can't leak.
		_ = s.be.FailClosed()
		s.state = StateFailed
		return &ProtocolError{Code: "tunnel_up_failed", Msg: err.Error()}
	}
	s.state = StateUp
	s.lastCfg = cfg
	return nil
}

// Down is the GRACEFUL, user-initiated teardown: restore normal routing.
// Idempotent — calling it while already down is a no-op success.
func (s *Supervisor) Down() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == StateDown {
		return nil
	}
	err := s.be.Down()
	s.state = StateDown
	s.lastCfg = nil
	if err != nil {
		return &ProtocolError{Code: "tunnel_down_failed", Msg: err.Error()}
	}
	return nil
}

// OnPeerLost is invoked when the IPC channel to the app drops while a tunnel is
// up (the app crashed or was killed). It FAILS CLOSED — the user's protected
// traffic must not silently revert to cleartext just because the UI went away.
// A no-op when already down/failed.
func (s *Supervisor) OnPeerLost() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateUp {
		return
	}
	_ = s.be.FailClosed()
	s.state = StateFailed
	s.lastCfg = nil // drop the private-key reference, like the graceful Down path
}

// Status returns live stats. In Failed state it reports "failed" without touching
// the backend (the interface is already gone).
func (s *Supervisor) Status() (TunnelStatus, error) {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != StateUp {
		return TunnelStatus{State: string(state)}, nil
	}
	st, err := s.be.Stats()
	if err != nil {
		return TunnelStatus{State: string(StateUp)}, &ProtocolError{Code: "stats_failed", Msg: err.Error()}
	}
	st.State = string(StateUp)
	return st, nil
}
