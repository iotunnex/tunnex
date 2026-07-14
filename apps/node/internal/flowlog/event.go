// Package flowlog is the agent-side flow-observation pipeline for S7.5.1 (the VISIBILITY
// half of Zero Trust). It reads flow verdicts the kernel emits from the gateway forward
// chain (nft log, rule_id carried in the log prefix — no packet re-match), buffers them in
// a BOUNDED, NON-BLOCKING ring, and hands batches to the reporter.
//
// ENFORCEMENT ISOLATION (the hard guarantee): this package imports NOTHING from egress and
// is never on the forward-chain apply path. Observation is best-effort and async — if the
// reader dies, the buffer overflows, or the reporter fails, packets still accept/drop
// correctly (the kernel's `log` statement is non-terminal + best-effort: an nflog buffer
// full drops LOG MESSAGES, never packets). Observability may die; enforcement may not.
package flowlog

import "time"

// Verdict is the packet fate the kernel recorded for a flow's first packet (ct state new).
type Verdict string

const (
	VerdictAllow Verdict = "allow"
	VerdictDeny  Verdict = "deny"
)

// Event is ONE flow observation the agent ships to the control plane. The agent stamps
// RuleID (kernel-carried via the nft log prefix — attribution rides the grant the kernel
// matched, NOT a userspace re-derivation) and PolicyHash (the applied artifact hash at
// observation, so the CP can detect a ruleset-swap-window skew). The agent ships IP-level
// facts only; identity enrichment (device / user / resource) is CP-side at ingest (4/n).
type Event struct {
	OccurredAt time.Time `json:"occurred_at"`
	Verdict    Verdict   `json:"verdict"`
	RuleID     string    `json:"rule_id,omitempty"` // "" = default-deny / no matching rule
	PolicyHash string    `json:"policy_hash"`       // applied CanonicalHash at observation (skew detection)
	SrcIP      string    `json:"src_ip"`
	DstIP      string    `json:"dst_ip"`
	Protocol   string    `json:"protocol"`
	DstPort    int       `json:"dst_port,omitempty"`
}
