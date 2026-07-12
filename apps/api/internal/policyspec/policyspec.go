// Package policyspec is the NEUTRAL (edition-agnostic) wire shape of the compiled
// Zero Trust policy artifact — the typed structure the control plane pushes to a
// node agent. It lives outside internal/enterprise so the open-build desired-state
// path (S7.2) can reference the type even though only the enterprise build ever
// produces a non-trivial value. The compiler (internal/enterprise/policy) emits
// these; S7.2's agent programs them into the gateway forward chain.
//
// Contract: Compile is DETERMINISTIC — equal input DB state produces a
// byte-identical Compiled — so a steady-state reconcile is a no-op (the
// reconcile-idempotence guard). Allow entries are sorted + de-duplicated.
package policyspec

// ProtocolVersion is the compiled-artifact wire version. Bump on any breaking
// shape change so a mismatched agent can reject rather than misapply a ruleset.
const ProtocolVersion = 1

// Protocol scopes an allow entry to an L4 protocol. "any" ignores ports.
type Protocol string

const (
	ProtoAny Protocol = "any"
	ProtoTCP Protocol = "tcp"
	ProtoUDP Protocol = "udp"
)

// AllowEntry is ONE compiled default-deny grant: the source device (its assigned
// /32 host address) may reach DstCIDR on Protocol within [PortLow,PortHigh].
// PortLow==0 means "all ports" (Protocol=="any", or an unscoped tcp/udp resource).
// S7.2 programs each entry as an accept in the gateway forward chain; anything not
// covered by an entry is dropped.
type AllowEntry struct {
	SrcIP    string   `json:"src_ip"`             // device assigned host address, no prefix (e.g. "10.99.0.7")
	DstCIDR  string   `json:"dst_cidr"`           // destination prefix (e.g. "10.0.5.0/24", "10.99.0.9/32", "0.0.0.0/0")
	Protocol Protocol `json:"protocol"`           // any | tcp | udp
	PortLow  int      `json:"port_low,omitempty"` // 0 => all ports
	PortHigh int      `json:"port_high,omitempty"`
}

// Compiled is the per-node policy artifact. It expresses BOTH modes so S7.2 has a
// single code path (mode is a compiler INPUT, not an enforcement special-case):
//
//   - Mode "off"       => Mesh=true, Allow=nil. Legacy blanket mesh: S7.2 keeps the
//     pre-Zero-Trust wg0<->wg0 accept. No behavior change on upgrade.
//   - Mode "enforcing" => Mesh=false. ONLY the entries in Allow are permitted;
//     everything else is denied (default-deny). An EMPTY Allow with Mesh=false
//     means "deny all device-to-device" — a legitimate locked-down posture, and
//     structurally NOT the same as the blanket mesh (empty != permissive).
//
// Invariant (enforced by the compiler + guarded by test): Mesh==true ONLY when
// Mode=="off". The enforcing path can never emit a blanket allow.
type Compiled struct {
	Version int          `json:"version"` // == ProtocolVersion
	NodeID  string       `json:"node_id"`
	Mode    string       `json:"mode"` // "off" | "enforcing"
	Mesh    bool         `json:"mesh"`
	Allow   []AllowEntry `json:"allow"`
}
