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
	// KindUnsupportedPolicyVersion (S8.1 D1): the agent REFUSED the compiled artifact — its
	// Version exceeds what the agent can apply — and went deny-all. UNIQUE remedy: upgrade the
	// agent (every other kind's remedy is CP-side). Highest priority: a refusing gateway isn't
	// stale or apply-failing, it's version-incapable — distinguish it so the operator upgrades.
	KindUnsupportedPolicyVersion PolicyDegradedKind = "unsupported_policy_version"
	// KindSiteHubDown / KindSiteLinkDown (S8.2, Item 7/9): a SITE gateway's site-link is down (stale/no
	// WG handshake), so site-to-site traffic blackholes though the policy may be perfectly synced — a
	// REACHABILITY failure, distinct from a policy desync (whose remedy is CP-side). HUB-down is separate
	// from a single spoke's link-down because the remedy differs entirely: hub-down kills EVERY spoke's
	// site traffic (fix the hub), a spoke link-down kills only that spoke (fix that spoke's tunnel/NAT).
	// Ranked directly below version-refused: for a site gateway, "my site link is dead" is the headline
	// infrastructure signal (the gateway can't do the one thing it exists for); hub outranks spoke.
	KindSiteHubDown  PolicyDegradedKind = "site_hub_down"
	KindSiteLinkDown PolicyDegradedKind = "site_link_down"
	// KindSiteSubnetUnreachable (S8.2c D3): a SITE gateway advertises a local subnet NO host address is
	// inside — it fronts a subnet it isn't actually on (bridge-trapped wg0, or a misconfigured
	// advertisement). The REASSURING-GREEN trap: wg0 is up and the handshake is fresh (so site_link_down
	// is FALSE), yet the LAN is unreachable and site traffic to it blackholes. Ranked directly below the
	// site-link kinds: a reachability/deploy fault whose remedy is operator-side (fix the gateway's host
	// networking — run host-mode / correct the advertised subnet), like the version-refused kind.
	KindSiteSubnetUnreachable PolicyDegradedKind = "site_subnet_unreachable"
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

// LogPolicyHealthTuning emits the assumed R + derived T at boot so an operator who tuned the
// agent report interval can DISCOVER the coupling (the doc caveat isn't findable at 3am).
func LogPolicyHealthTuning(log interface{ Info(string, ...any) }) {
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
	UnsupportedVersion bool          // agent-reported: it REFUSED a too-new artifact (S8.1 D1) → highest-priority kind
	SiteHubDown        bool          // S8.2: a site gateway whose HUB site-link has no fresh WG handshake (all spokes' site traffic dead)
	SiteLinkDown       bool          // S8.2: a site gateway with ≥1 spoke site-link with no fresh handshake (that spoke's traffic dead)
	// SiteSubnetUnreachable (S8.2c D3): the gateway advertises a local subnet no host address is inside —
	// the reassuring-green bridge-mode trap. INDEPENDENT of SiteLinkDown (fires when the link is FRESH).
	SiteSubnetUnreachable bool
}

// TransitionRule documents ONE state's authoritative evidence-in — mirrors the state × render
// × transition-evidence TABLE in docs/S7.4-decisions.md. Drift between this and degradedKind
// (or the paper) is caught at review; an evidence-less state is a paper finding.
type TransitionRule struct {
	Kind       PolicyDegradedKind
	EvidenceIn string
}

var transitionTable = []TransitionRule{
	{KindUnsupportedPolicyVersion, "agent REFUSED a too-new artifact (UnsupportedVersion) — checked FIRST, remedy = upgrade the agent"},
	{KindSiteHubDown, "site gateway, HUB site-link no fresh handshake (SiteHubDown) — remedy = fix the hub; outranks a single spoke link-down"},
	{KindSiteLinkDown, "site gateway, a spoke site-link no fresh handshake (SiteLinkDown) — remedy = fix that spoke's tunnel/NAT"},
	{KindSiteSubnetUnreachable, "site gateway advertises a local subnet no host addr is inside (SiteSubnetUnreachable) — reassuring-green trap; remedy = fix the gateway's host networking"},
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
	// S8.1 D1 — HIGHEST priority: the agent refused a too-new artifact and went deny-all. This is
	// a version-incapability, not a stale/failing apply; its remedy (upgrade the agent) is unique,
	// so it must not be masked by the desync/apply-error paths below.
	if in.UnsupportedVersion {
		return KindUnsupportedPolicyVersion
	}
	// S8.2 (Item 7/9) — a site gateway's site-link is down: site traffic is dead regardless of policy
	// state, and the remedy is infrastructure (fix the hub / that spoke), not CP-side. HUB-down first
	// (kills every spoke), then a single spoke link-down. Ranked above the policy apply/desync kinds:
	// for a site gateway this is the headline. (A gateway can be BOTH link-down and desync'd; the desync
	// stamp is retained and re-surfaces once the link recovers — the kind is a single summary.)
	if in.SiteHubDown {
		return KindSiteHubDown
	}
	if in.SiteLinkDown {
		return KindSiteLinkDown
	}
	// S8.2c D3 — the gateway advertises a local subnet it isn't on: site traffic to that LAN blackholes
	// even though the link handshake is fresh (the reassuring-green trap). Ranked below the link kinds
	// (a dead link is the louder failure) but above the policy apply/desync kinds — it's a reachability
	// fault, remedy operator-side (fix the gateway host networking), not a CP-side policy issue.
	if in.SiteSubnetUnreachable {
		return KindSiteSubnetUnreachable
	}
	// Agent-reported apply failure (from the last report — a reported fact, not a server compare).
	// [fold 3] mirror the bool's TERM-2: policy_failing_since alone (error empty) is a failing
	// enforcing apply — apply_failing, NEVER the benign desync path. Order: failing_since first.
	if in.PolicyFailingSince != "" {
		return KindApplyFailing // an enforcing apply is failing (onset stamped), with or without an error string
	}
	if in.PolicyError != "" {
		return KindStuckEnforcing // error + no failing_since = enforcing a disabled policy (S7.2 stuck branch)
	}
	// No error → desync territory (term-3). Can't compute pushed → can't determine.
	if !in.PushKnown {
		return KindDesyncUnknown
	}
	// pushed "" = non-enforcing (off/mesh) — no enforcement boundary, so never a desync
	// (mirrors the bool's term-3 `h != ""` guard). Equal hashes = in sync / reconverged.
	if in.PushedHash == "" || in.AppliedHash == in.PushedHash {
		return KindHealthy
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
