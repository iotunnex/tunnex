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

import (
	"time"

	"github.com/google/uuid"
)

// ResourceInput and RuleInput are the NEUTRAL CRUD payload DTOs for the policy
// API. They live here (not in internal/enterprise/policy) so the open-build http
// port + handlers can reference them WITHOUT importing the enterprise package —
// which would link the enterprise compiler into the open binary and break the
// edition boundary (the SSO port takes primitives for the same reason). The
// enterprise service consumes these and does the validation.
type ResourceInput struct {
	Name     string
	CIDR     string
	Protocol string // any | tcp | udp
	PortLow  *int
	PortHigh *int
}

// RuleInput is a policy allow-rule create payload. S7.5.4: the SOURCE subject is
// a group (SrcKind="group", SrcGroupID) OR a single user (SrcKind="user",
// SrcUserID); ExpiresAt set makes it a temporary grant (nil = permanent). SrcKind
// blank is treated as "group" (back-compat with pre-S7.5.4 callers).
type RuleInput struct {
	SrcKind       string // "" | group | user
	SrcGroupID    uuid.UUID
	SrcUserID     *uuid.UUID
	DstKind       string // resource | group
	DstResourceID *uuid.UUID
	DstGroupID    *uuid.UUID
	ExpiresAt     *time.Time // nil = permanent; set = temporary grant
}

// AffectedDevice is a full-tunnel device whose internet egress becomes policy-
// governed when the org enters enforcing (S7.2 decision 2a). Neutral so the open-
// build http port can name it without importing the enterprise package.
type AffectedDevice struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ProtocolVersion is the compiled-artifact wire version. Bump on any shape change.
// v2 (S7.5.1): AllowEntry gains an OBSERVABILITY-only `rule_id` (flow-log
// attribution). v3 (S7.5.4 slice 3): AllowEntry gains an OBSERVABILITY-only
// `src_device_id` (the source device's uuid) so the agent stamps flow events with
// device identity from the ARTIFACT (never an src_ip→device DB guess) and the CP
// joins device→user CP-side. Both are additive, NEVER enforcement, and EXCLUDED
// from CanonicalHash (the enforcement-only projection), so old agents ignore the
// unknown field and artifacts with identical grants enforce IDENTICALLY across
// versions. A version-number bump does re-converge the fleet ONCE on deploy
// (A-4: one version served fleet-wide) — enforcement is unchanged, only the
// metadata grows. See docs/S7.5.1-decisions.md (D2 / A-4), docs/S7.5.4-decisions.md (D4).
//
// v4 (S8.1 Slice 3, EPIC 8): sites become a policy DESTINATION KIND. Option A (ruled) — NO new wire
// field: a `device→site-subnet` grant compiles to a same-shape AllowEntry{Src, Dst: site-subnet-CIDR}
// (the KIND is a CP-side rule fact, resolved to DstCIDR at compile). The enforcement-significant change
// IS this version bump — `Version` is IN CanonicalHash (hash.go), so v4 changes every gateway's hash and
// (with S8.1 D1's gate) an agent at maxSupported<4 REFUSES it (deny-all + unsupported_policy_version)
// rather than silently mis-enforcing. This is what makes Version-in-hash safe under mixed versions —
// the A-4 warning at hash.go:25-28 fires and its answer ships here. See docs/S8.1-decisions.md D2/D3.
const ProtocolVersion = 4

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
	// RuleID (v2, S7.5.1) is OBSERVABILITY metadata: the CP policy_rules.uuid whose
	// grant produced this entry, so the agent stamps flow/deny events with it and the
	// conntrack-kill agrees on identity. NEVER enforcement — EXCLUDED from CanonicalHash
	// (the enforcement projection), so staleness is metadata-blind. Empty on v1 wire /
	// default-deny with no matching rule.
	RuleID string `json:"rule_id,omitempty"`
	// SrcDeviceID (v3, S7.5.4) is OBSERVABILITY metadata: the source device's uuid
	// (devices.id). The agent maps a flow's src /32 -> this id from the APPLIED artifact
	// and stamps it on the flow event, so the CP attributes flows to a device (then to a
	// user, CP-side) WITHOUT any src_ip->device DB reconstruction. NEVER enforcement —
	// EXCLUDED from CanonicalHash. Empty on v1/v2 wire / a src with no grant.
	SrcDeviceID string `json:"src_device_id,omitempty"`
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
