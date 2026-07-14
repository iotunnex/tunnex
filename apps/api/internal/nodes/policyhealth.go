package nodes

import "time"

// PolicyDegradedKind is the ADVISORY differentiated-health enum (S7.4b). It refines the
// authoritative `policy_degraded` BOOL with which-kind display detail — it adds NO decision
// logic and is UI-read-only (nothing in enforcement / push targeting / compilation reads it;
// asserted by a structural guard, S1.1 edition-isolation shape). The bool stays the sole
// load-bearing signal (the S7.2 collapse must not silently un-collapse).
//
// `desync_unknown` is a FIRST-CLASS honest state — it means "we could not determine", NEVER
// "healthy" and NEVER a specific kind. Rendering a known hash mismatch as unknown, or an
// unknown as healthy, is the failure-must-be-legible law's mirror polarity.
type PolicyDegradedKind string

const (
	KindHealthy        PolicyDegradedKind = "healthy"
	KindApplyFailing   PolicyDegradedKind = "apply_failing"    // enforcing apply currently failing
	KindStuckEnforcing PolicyDegradedKind = "stuck_enforcing"  // enforcing a disabled/off policy it can't swap out
	KindConverging     PolicyDegradedKind = "converging"       // pushed!=applied, fresh, age < T — a normal push settling
	KindSilentDesync   PolicyDegradedKind = "silent_desync"    // pushed!=applied, fresh, age >= T — stuck (the S7.2 nightmare)
	KindDesyncUnknown  PolicyDegradedKind = "desync_unknown"   // can't determine: pushed-hash unavailable, or stamped + reports stale
)

// ── DECIDE-POINT A (pending sign-off — values not final) ────────────────────────────────
// T (desyncDebounce) and the report-freshness window are derived from the agent REPORT
// interval (TUNNEX_AGENT_REPORT_INTERVAL, default 30s), NOT the <5s push latency — the
// convergence signal is a REPORT, so a desync younger than a report cycle is expected, not
// stuck. Placeholder values below pending the arithmetic (returned to the user before their
// finalization + the T-boundary reds).
const (
	desyncDebounce    = 60 * time.Second // TODO(decide-point-a): T = 2× report interval?
	reportFreshWindow = 60 * time.Second // TODO(decide-point-a): freshness F
)

// KindInput is the render signature: kind = f(stamp × report-freshness × hash) — plus the
// agent-reported apply error/onset. Every field is CP-known at compute time; nothing here is
// UI state. `pushKnown` false = the compiled (pushed) hash was unavailable (compile fault) →
// the desync term can't be evaluated → can't-determine.
type KindInput struct {
	PolicyError        string        // agent-reported: last apply error ("" = none)
	PolicyFailingSince string        // agent-reported: enforcing-apply failure onset ("" = none)
	PushKnown          bool          // could the CP compute the pushed hash this cycle?
	PushedHash         string        // CP-computed desired hash (valid iff PushKnown)
	AppliedHash        string        // agent-reported hash in force
	DesyncSince        time.Time     // CP-stamped onset of term-3 (zero = not stamped)
	ReportAge          time.Duration // now − last_seen_at (report freshness)
	Now                time.Time
}

// TransitionRule documents ONE state's authoritative evidence-in — mirrors the state × render
// × transition-evidence TABLE in docs/S7.4-decisions.md. Drift between this and degradedKind
// (or the paper) is caught at review; an evidence-less state is a paper finding.
type TransitionRule struct {
	Kind       PolicyDegradedKind
	EvidenceIn string
}

var transitionTable = []TransitionRule{
	{KindHealthy, "not degraded: no error, pushed==applied, reports fresh"},
	{KindApplyFailing, "policy_error set AND policy_failing_since set"},
	{KindStuckEnforcing, "policy_error set AND policy_failing_since EMPTY (pushed=='' && applied!='')"},
	{KindConverging, "term-3 (pushed!=applied), reports fresh, age < T"},
	{KindSilentDesync, "term-3 (pushed!=applied), reports fresh, age >= T"},
	{KindDesyncUnknown, "pushed-hash UNAVAILABLE, OR (stamped AND reports stale) — cannot determine"},
}

// degradedKind projects the advisory kind from the inputs. SKELETON: the branching body +
// its reds land after DECIDE-POINT A (T) is signed off — this stub returns the can't-determine
// state so nothing false-positives before the logic is finalized (never "healthy" by default).
func degradedKind(in KindInput) PolicyDegradedKind {
	_ = transitionTable
	_ = desyncDebounce
	_ = reportFreshWindow
	// TODO(decide-point-a): implement the transition table above once T is confirmed.
	return KindDesyncUnknown
}
