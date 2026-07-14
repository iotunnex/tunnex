package nodes

import (
	"testing"
	"time"
)

// degradedKind reds — X-3's render set. The kind is advisory over the authoritative bool;
// these pin that a desync NEVER false-alarms within T, a stuck one IS flagged past T, and a
// can't-determine (compile fault OR stale reports) is a DISTINCT honest state — never healthy,
// never a specific kind.
func TestDegradedKind(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	fresh := 5 * time.Second // report age < F
	base := KindInput{PushKnown: true, PushedHash: "h", AppliedHash: "h", ReportAge: fresh, Now: now}

	cases := []struct {
		name string
		in   KindInput
		want PolicyDegradedKind
	}{
		{"healthy — in sync, fresh", base, KindHealthy},
		{"apply_failing — error + failing_since", KindInput{PolicyError: "boom", PolicyFailingSince: "t0", ReportAge: fresh, Now: now}, KindApplyFailing},
		{"stuck_enforcing — error + NO failing_since", KindInput{PolicyError: "boom", ReportAge: fresh, Now: now}, KindStuckEnforcing},
		// [fold 3] TERM-2: failing_since set with NO error must be apply_failing, NEVER the benign
		// converging path (the bool catches failing_since alone; the kind must too).
		{"term-2 — failing_since, no error → apply_failing (not converging)", KindInput{PolicyFailingSince: "t0", PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now}, KindApplyFailing},
		{"desync_unknown — pushed hash unavailable", KindInput{PushKnown: false, AppliedHash: "h", ReportAge: fresh, Now: now}, KindDesyncUnknown},
		{"non-enforcing — pushed '' → healthy (no enforcement boundary)", KindInput{PushKnown: true, PushedHash: "", AppliedHash: "old", ReportAge: fresh, Now: now}, KindHealthy},

		// [X-3 false-alarm] a fresh push settling: mismatch, fresh, age < T → converging, NEVER silent_desync.
		{"converging — mismatch, fresh, age < T", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now, DesyncSince: now.Add(-10 * time.Second)}, KindConverging},
		// [X-3 stuck] mismatch persisting past T with fresh reports → silent_desync.
		{"silent_desync — mismatch, fresh, age > T", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now, DesyncSince: now.Add(-90 * time.Second)}, KindSilentDesync},
		// [exactly-T boundary] age == T → silent_desync (>=).
		{"boundary — age == T → silent_desync", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now, DesyncSince: now.Add(-DesyncSettleWindow)}, KindSilentDesync},
		// [just under T] → still converging.
		{"boundary − 1ns → converging", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now, DesyncSince: now.Add(-DesyncSettleWindow + time.Nanosecond)}, KindConverging},
		// not-yet-stamped race (zero onset) on a fresh mismatch → converging, not silent.
		{"zero onset → converging (just-onset race)", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: fresh, Now: now}, KindConverging},

		// [X-3 reports-stop] stale reports + mismatch → desync_unknown, NEVER silent_desync, NEVER healthy —
		// even with an OLD stamped onset (silence is not evidence of ongoing desync).
		{"reports-stop — stale + mismatch + old onset → desync_unknown", KindInput{PushKnown: true, PushedHash: "new", AppliedHash: "old", ReportAge: ReportFreshnessWindow, Now: now, DesyncSince: now.Add(-10 * time.Minute)}, KindDesyncUnknown},
		// [X-3 reconverge] applied catches up to pushed → healthy (convergence is a STATE predicate).
		{"reconverge — applied == pushed → healthy", KindInput{PushKnown: true, PushedHash: "h", AppliedHash: "h", ReportAge: fresh, Now: now}, KindHealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := degradedKind(c.in); got != c.want {
				t.Fatalf("degradedKind = %q, want %q", got, c.want)
			}
		})
	}
}

// T = 2R, F = 2R — the report-cadence basis (not the push SLA). Pins the arithmetic so a
// silent edit can't quietly retune the debounce (the boundary red is only load-bearing if T holds).
func TestPolicyHealthWindowsAreTwiceReportInterval(t *testing.T) {
	if DesyncSettleWindow != 2*AssumedReportInterval || ReportFreshnessWindow != 2*AssumedReportInterval {
		t.Fatalf("T/F must be 2×R: T=%v F=%v R=%v", DesyncSettleWindow, ReportFreshnessWindow, AssumedReportInterval)
	}
	if AssumedReportInterval != 30*time.Second {
		t.Fatalf("R must mirror the agent default 30s, got %v", AssumedReportInterval)
	}
}
