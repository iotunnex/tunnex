// Package nodepolicy is the agent-side mirror of the control plane's compiled Zero
// Trust policy artifact (apps/api internal/policyspec). apps/api and apps/node are
// separate modules, so — exactly as the agent already mirrors the desired-state
// Peer shape — the compiled policy rides the desired-state JSON and is decoded into
// these types. The egress package renders them into the gateway's nftables forward
// chain (S7.2 enforcement).
package nodepolicy

// Modes (mirror organizations.zero_trust_mode / policyspec).
const (
	ModeOff       = "off"
	ModeEnforcing = "enforcing"
)

// AllowEntry is one compiled default-deny grant: SrcIP (a device /32 host) may reach
// DstCIDR on Protocol within [PortLow,PortHigh]. PortLow==0 means all ports.
type AllowEntry struct {
	SrcIP    string `json:"src_ip"`
	DstCIDR  string `json:"dst_cidr"`
	Protocol string `json:"protocol"` // any | tcp | udp
	PortLow  int    `json:"port_low,omitempty"`
	PortHigh int    `json:"port_high,omitempty"`
}

// Compiled is the per-node policy artifact. Mesh=true (mode off) => the agent keeps
// the legacy blanket wg0<->wg0 forward accept (no behavior change when Zero Trust is
// off). Mesh=false (enforcing) => ONLY Allow is permitted; everything else is dropped
// by the forward chain's policy-drop base (default-deny; empty Allow = deny-all).
type Compiled struct {
	Version int          `json:"version"`
	NodeID  string       `json:"node_id"`
	Mode    string       `json:"mode"`
	Mesh    bool         `json:"mesh"`
	Allow   []AllowEntry `json:"allow"`
}
