// Package helper is the Tunnex privileged tunnel helper (S6.3).
//
// It is a SEPARATE privileged process (not Electron, not Node) that owns the one
// operation the desktop app cannot do unprivileged: bring a WireGuard tunnel
// up/down and configure its interface. The app (Electron main) drives it over a
// local IPC channel using the typed protocol in this file — a fixed verb set, no
// generic passthrough (same allowlist posture as the S6.1 preload). The verb set
// IS the privileged attack surface; keep it minimal.
package helper

// ProtocolVersion is bumped on any wire-incompatible change. Both sides send it on
// every message so a mismatched app/helper pair fails fast and loud rather than
// misparsing. It is intentionally independent of the app version.
const ProtocolVersion = 1

// AuthMode is how the helper authenticates that its caller is the REAL Tunnex app
// and not another local process. It rides on every request FROM DAY ONE so that
// the S6.5b hardening (crypto code-signing identity pinning) is a negotiated MODE
// UPGRADE on this same protocol, never a breaking change.
type AuthMode string

const (
	// AuthModePathCheck is the INTERIM mode for unsigned builds: verify the
	// caller's executable lives inside the app's install dir. Weaker than pinning
	// (see auth.go for the threat model). Retire trigger = S6.5b.
	AuthModePathCheck AuthMode = "path_check"
	// AuthModeCodeSigning is the S6.5b mode: pin the caller's code-signing identity
	// (macOS XPC peer requirement / Windows Authenticode). Not yet compiled in.
	AuthModeCodeSigning AuthMode = "code_signing"
)

// authRank orders modes by strength. 0 = unknown/unsupported.
func authRank(m AuthMode) int {
	switch m {
	case AuthModePathCheck:
		return 1
	case AuthModeCodeSigning:
		return 2
	default:
		return 0
	}
}

// Negotiate returns the auth mode the helper will ENFORCE for this connection, or
// an error if the client cannot meet the helper's enforced minimum.
//
// The helper enforces exactly the mode it has a verifier compiled for (`enforced`)
// — today path_check, at S6.5b code_signing. `clientMax` is the strongest mode the
// app supports. If the app is too weak for the helper's enforced mode we REFUSE
// (no silent downgrade): once the helper is signed and enforces code_signing, a
// stale path_check-only app is rejected and must update. That one-way ratchet is
// the whole point of carrying auth_mode from day one.
// NegotiateVersion checks wire-protocol compatibility and, on mismatch, names WHICH
// side is stale so the app can act. The helper is the long-lived root daemon; the app
// upgrades it (a new app version installs its bundled helper via the lifecycle —
// SMAppService / the Windows service). So:
//   - client > helper → the installed helper is OLD (app upgraded, helper didn't):
//     "helper_outdated" — the app should (re)install its bundled helper via the
//     lifecycle, then retry. This is the NORMAL upgrade path.
//   - client < helper → the app is OLD relative to a newer helper: "client_outdated" —
//     REFUSE (a stale app must not drive a newer helper; a downgrade-refused ratchet,
//     mirroring the auth-mode ratchet in Negotiate). Update the app.
// v1 is the only version today; this is the framework the first bump uses.
func NegotiateVersion(client, helper int) error {
	switch {
	case client == helper:
		return nil
	case client > helper:
		return &ProtocolError{Code: "helper_outdated", Msg: "the Tunnex helper is older than this app — reinstall to upgrade it"}
	default:
		return &ProtocolError{Code: "client_outdated", Msg: "this app is older than the installed Tunnex helper — update the app"}
	}
}

func Negotiate(clientMax, enforced AuthMode) (AuthMode, error) {
	if authRank(enforced) == 0 {
		return "", &ProtocolError{Code: "auth_mode_unsupported", Msg: "helper enforced auth mode is unknown"}
	}
	if authRank(clientMax) == 0 {
		return "", &ProtocolError{Code: "auth_mode_unsupported", Msg: "client auth mode is unknown"}
	}
	if authRank(clientMax) < authRank(enforced) {
		return "", &ProtocolError{Code: "auth_downgrade_refused", Msg: "client cannot meet the helper's required auth mode"}
	}
	return enforced, nil
}

// Verb is the closed set of operations the helper exposes. No arbitrary command,
// no config-file path, no "run this binary".
type Verb string

const (
	VerbTunnelUp   Verb = "tunnel_up"   // bring the tunnel up from a validated config
	VerbTunnelDown Verb = "tunnel_down" // tear the tunnel down (idempotent)
	VerbStatus     Verb = "status"      // read-only handshake/transfer stats
)

func validVerb(v Verb) bool {
	return v == VerbTunnelUp || v == VerbTunnelDown || v == VerbStatus
}

// Request is one app→helper message. Config is REQUIRED for tunnel_up and must be
// nil otherwise (a config on a down/status request is rejected — no smuggling).
type Request struct {
	Version  int           `json:"version"`
	AuthMode AuthMode      `json:"auth_mode"`
	Verb     Verb          `json:"verb"`
	Config   *TunnelConfig `json:"config,omitempty"`
}

// Response is one helper→app reply. Status is set only for a successful status/up.
type Response struct {
	Version int           `json:"version"`
	OK      bool          `json:"ok"`
	Code    string        `json:"code,omitempty"`  // stable machine code on failure
	Error   string        `json:"error,omitempty"` // human message on failure
	Status  *TunnelStatus `json:"status,omitempty"`
}

// TunnelStatus is read-only live state (no secrets — never echoes keys).
type TunnelStatus struct {
	State            string `json:"state"` // "down" | "up" | "failed"
	Interface        string `json:"interface,omitempty"`
	LastHandshakeSec int64  `json:"last_handshake_sec,omitempty"` // unix seconds, 0 = never
	RxBytes          uint64 `json:"rx_bytes,omitempty"`
	TxBytes          uint64 `json:"tx_bytes,omitempty"`
	// UnsafeDevMode is TRUE only when a Windows full tunnel is running under the S6.10
	// dev bypass (TUNNEX_DANGEROUS_WINDOWS_FULLTUNNEL) — the WFP kill-switch does NOT yet
	// survive process death (Story B / S6.7 pending), so the box can leak on a hard kill.
	// The app surfaces this as a LOUD banner. Never set in production (the guard refuses).
	UnsafeDevMode bool `json:"unsafe_dev_mode,omitempty"`
}

// ProtocolError is a typed helper error carrying a stable machine code.
type ProtocolError struct {
	Code string
	Msg  string
}

func (e *ProtocolError) Error() string { return e.Code + ": " + e.Msg }

// ValidateRequest checks the envelope BEFORE any privileged work: version match,
// known auth mode + verb, and the config-presence rule. Field-level config
// validation is TunnelConfig.Validate (called by the up path). Returns a
// ProtocolError so the caller can surface a stable code.
func ValidateRequest(r *Request) error {
	if r == nil {
		return &ProtocolError{Code: "empty_request", Msg: "request is nil"}
	}
	if err := NegotiateVersion(r.Version, ProtocolVersion); err != nil {
		return err
	}
	if authRank(r.AuthMode) == 0 {
		return &ProtocolError{Code: "auth_mode_unsupported", Msg: "unknown auth mode"}
	}
	if !validVerb(r.Verb) {
		return &ProtocolError{Code: "unknown_verb", Msg: "unknown verb"}
	}
	if r.Verb == VerbTunnelUp && r.Config == nil {
		return &ProtocolError{Code: "config_required", Msg: "tunnel_up requires a config"}
	}
	if r.Verb != VerbTunnelUp && r.Config != nil {
		return &ProtocolError{Code: "unexpected_config", Msg: "config is only valid on tunnel_up"}
	}
	return nil
}
