package nodes

import (
	"testing"
	"time"
)

// PolicyStale measures from the mismatch ONSET (PolicyFailingSince), not the applied-
// hash age (finding #3). Healthy apply -> never stale; a failure -> stale only after
// PolicyStaleAfter (90s) of persistence, so a normal push that applies never false-alarms.
func TestPolicyStaleFromMismatchOnset(t *testing.T) {
	onset := time.Date(2026, 7, 13, 7, 0, 0, 0, time.UTC)

	// Healthy: no failing_since -> NEVER stale, regardless of how much time passes
	// (this is the false-positive the old applied-hash-age design created on every push).
	healthy := NodeCapabilities{PolicyHash: "abc"}
	if healthy.PolicyStale(onset.Add(time.Hour)) {
		t.Fatal("a healthy node (no failing_since) must never be stale")
	}

	// Failing: stale ONLY after > 90s from onset.
	failing := NodeCapabilities{PolicyHash: "abc", PolicyFailingSince: onset.Format(time.RFC3339)}
	if failing.PolicyStale(onset.Add(60 * time.Second)) {
		t.Fatal("at onset+60s (< 90s) must NOT be stale (transient window)")
	}
	if !failing.PolicyStale(onset.Add(95 * time.Second)) {
		t.Fatal("at onset+95s (> 90s) must be stale")
	}
	// Boundary: exactly the window is not yet stale (strictly greater-than).
	if failing.PolicyStale(onset.Add(PolicyStaleAfter)) {
		t.Fatal("at exactly the window must not yet be stale")
	}
}
