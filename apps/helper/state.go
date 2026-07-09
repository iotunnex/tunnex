package helper

import (
	"sync"
	"time"
)

// DeadManDefault is the conservative window after which an un-heartbeated tunnel
// auto-releases its kill-switch. This BOUNDS the fail-closed model: a helper that
// is alive but whose owning app has crashed/wedged (no heartbeats) can't strand the
// host forever — after this window the block is released. The app heartbeats every
// ~10s, so this is many missed beats. See PLAN "S6.3 KILL-SWITCH DESIGN" (bounded).
const DeadManDefault = 90 * time.Second

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
//     ORDERING INVARIANT (leak-safety): Up MUST arm the kill-switch backstop
//     BEFORE it moves any routes onto the tunnel. So if Up is interrupted at any
//     point after arming, traffic is BLOCKED (fail-closed), never leaked out the
//     cleartext default route during a half-built setup. Graceful Down is the
//     inverse — restore normal routing, then drop the backstop LAST — so a
//     teardown transient is at worst a brief DROP, never a leak. (This is a
//     contract on each platform Backend's Up/Down; the Supervisor guarantees the
//     state-level fail-closed, the ordering guarantees the setup/teardown windows.)
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
	// CleanStale releases any kill-switch state left over from a PRIOR process that
	// exited without a graceful Down (a crash / kill -9). The helper calls it ONCE
	// at startup, BEFORE serving, so a KeepAlive-restarted helper self-heals a block
	// that would otherwise strand the host. Must be safe to call when nothing is
	// stale (idempotent no-op).
	CleanStale() error
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
	// Dead-man: lastBeat is refreshed on Up + every heartbeat (Status while up); if
	// the tunnel is up/failed and no beat lands within deadMan, CheckDeadMan releases
	// the kill-switch. now/deadMan are injectable for tests.
	lastBeat time.Time
	deadMan  time.Duration
	now      func() time.Time
}

func NewSupervisor(be Backend) *Supervisor {
	return &Supervisor{be: be, state: StateDown, deadMan: DeadManDefault, now: time.Now}
}

// SelfHeal releases any kill-switch state stranded by a prior crashed process. The
// helper calls it ONCE at startup, before serving. Idempotent.
func (s *Supervisor) SelfHeal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateDown
	return s.be.CleanStale()
}

// beat refreshes the dead-man deadline. Caller holds s.mu.
func (s *Supervisor) beat() { s.lastBeat = s.now() }

// CheckDeadMan releases the kill-switch if the tunnel is up/failed but no heartbeat
// has landed within the dead-man window (owner crashed/wedged). Returns whether it
// fired. A background loop calls it periodically; tests call it directly.
func (s *Supervisor) CheckDeadMan() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateUp && s.state != StateFailed {
		return false
	}
	if s.now().Sub(s.lastBeat) <= s.deadMan {
		return false
	}
	// Bounded fail-closed: past the window with no owner → release so an unrecovered
	// crash can't strand the host. Down restores routing + drops the block.
	_ = s.be.Down()
	s.state = StateDown
	s.lastCfg = nil
	return true
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
	s.beat() // start the dead-man clock
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
	if state == StateUp {
		s.beat() // a status call from the app IS the heartbeat — refresh the dead-man
	}
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
