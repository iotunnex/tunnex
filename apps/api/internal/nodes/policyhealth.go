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

// T (desyncDebounce) + the report-freshness window F are derived from the agent REPORT
// interval R (TUNNEX_AGENT_REPORT_INTERVAL, default 30s), NOT the <5s push latency — the
// convergence signal is a REPORT, so a desync younger than a report cycle is expected, not
// stuck. T = 2R: one R for the agent to apply + report the new hash after a push, one R of
// margin for a jittered/dropped report. F = 2R: a node silent for two report cycles can't
// have its desync confirmed → desync_unknown. CP-side constants tied to the DEFAULT R; if an
// operator tunes the agent's report interval, revisit these (the CP can't read the agent's
// env — it logs assumed-R + derived-T at boot for discoverability, see logPolicyHealthTuning).
const (
	// AssumedReportInterval (R) mirrors the agent default; the CP can't read the agent env.
	AssumedReportInterval = 30 * time.Second
	// DesyncSettleWindow (T = 2R) — a term-3 desync younger than this is CONVERGING (a normal
	// push settling), not stuck. The exactly-T boundary is load-bearing (red).
	DesyncSettleWindow = 2 * AssumedReportInterval
	// ReportFreshnessWindow (F = 2R) — a node silent this long can't have its desync confirmed
	// → desync_unknown.
	ReportFreshnessWindow = 2 * AssumedReportInterval
)

// logPolicyHealthTuning emits the assumed R + derived T at boot so an operator who tuned the
// agent report interval can DISCOVER the coupling (the doc caveat isn't findable at 3am).
func logPolicyHealthTuning(log interface{ Info(string, ...any) }) {
	log.Info("policy_health_tuning",
		"assumed_report_interval", AssumedReportInterval.String(),
		"desync_settle_window_T", DesyncSettleWindow.String(),
		"report_freshness_window_F", ReportFreshnessWindow.String())
}

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

// degradedKind projects the advisory kind (pure — mirrors transitionTable). Order matters:
// a live apply error is self-evident from the agent's last report; the desync path needs a
// FRESH applied hash (a server-side compare is meaningless on a stale one).
func degradedKind(in KindInput) PolicyDegradedKind {
	// Agent-reported apply error (from the last report — a reported fact, not a server compare).
	if in.PolicyError != "" {
		if in.PolicyFailingSince != "" {
			return KindApplyFailing
		}
		return KindStuckEnforcing // error + no failing_since = the stuck-enforcing branch (S7.2)
	}
	// No error → desync territory (term-3). Can't compute pushed → can't determine.
	if !in.PushKnown {
		return KindDesyncUnknown
	}
	if in.AppliedHash == in.PushedHash {
		return KindHealthy // in sync (or reconverged — convergence is a STATE predicate)
	}
	// pushed != applied. A stale report can't confirm ONGOING desync → desync_unknown (the
	// stamp is retained elsewhere; silence never clears it). NEVER healthy, NEVER silent_desync.
	if in.ReportAge >= ReportFreshnessWindow {
		return KindDesyncUnknown
	}
	// Fresh + mismatched: onset age decides converging vs stuck. A zero onset is a not-yet-
	// stamped race (this report's ingest stamps it) → just-onset = converging.
	if in.DesyncSince.IsZero() || in.Now.Sub(in.DesyncSince) < DesyncSettleWindow {
		return KindConverging
	}
	return KindSilentDesync // fresh, mismatched, age >= T
}
