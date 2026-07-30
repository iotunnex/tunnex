package nodes

import (
	"strings"
	"testing"
	"time"
)

// TestRekeyRefusedAgainstALiveNode — THE FIRST RED OF SLICE 4, written before any re-key mechanism exists.
//
// Same ordering as S9.1's B1 boundary: prove the thing that must never happen is impossible, then build on it. A
// guard retrofitted after the mechanism works is a guard whose absence has already been shipped once.
//
// THE ATTACK. Re-key issues a fresh certificate for an EXISTING node id to a caller that proves possession of that
// node's original keypair. Against a live gateway that is a takeover: the caller's agent inherits the node's
// identity, site binding and policy, and the real gateway is silently displaced — it keeps running, keeps
// forwarding, and is no longer the node the control plane believes it is.
func TestRekeyRefusedAgainstALiveNode(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	ok, reason := RekeyAuthorized("active", now.Add(24*time.Hour), true, now)
	if ok {
		t.Fatalf("re-key MUST be refused against a live node with a valid certificate — authorizing it is a "+
			"takeover primitive, not a recovery path. Got authorized with reason %q", reason)
	}
	if !strings.Contains(reason, "still valid") {
		t.Errorf("the refusal must say WHY, so an operator knows the remedy is to revoke first; got %q", reason)
	}
	// The remedy must be named, not implied.
	if !strings.Contains(reason, "Revoke it first") {
		t.Errorf("the refusal must name the remedy (revoke first); got %q", reason)
	}
}

// TestRekeyAuthorizedOnlyByTheTwoRuledEvidences — D3 is an ALLOWLIST of two facts. This asserts both halves and,
// more importantly, that nothing else qualifies.
func TestRekeyAuthorizedOnlyByTheTwoRuledEvidences(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	if ok, _ := RekeyAuthorized("revoked", future, true, now); !ok {
		t.Error("a REVOKED node must authorize re-key even while its certificate is still technically valid — a " +
			"human decided, which is the strongest evidence available")
	}
	if ok, _ := RekeyAuthorized("active", past, true, now); !ok {
		t.Error("an EXPIRED certificate must authorize re-key: the agent cannot authenticate and cannot renew, " +
			"which is the whole condition this epic exists to recover from")
	}
	if ok, _ := RekeyAuthorized("active", future, true, now); ok {
		t.Error("valid certificate + active status must NOT authorize")
	}
}

// TestStalenessIsNotEvidenceOfGone — the inadmissible inference, asserted as a property.
//
// RekeyAuthorized takes no liveness argument AT ALL, which is the structural version of this rule: a caller cannot
// pass staleness in even by mistake. This test pins that the signature stays that way, because the tempting third
// condition is exactly "we haven't heard from it in days" — and a network partition would then authorize a
// takeover of a gateway that is running perfectly.
func TestStalenessIsNotEvidenceOfGone(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// A node silent for a month, with a VALID certificate. Silence is not proof that a credential cannot work.
	if ok, reason := RekeyAuthorized("active", now.Add(720*time.Hour), true, now); ok {
		t.Fatalf("a long-silent node with a valid certificate must NOT authorize re-key: silence has many "+
			"causes and none of them is proof the credential stopped working. Got %q", reason)
	}
}

// TestUnknownExpiryIsNotGone — a row predating migration 0054 that 0055 declined to bound (it had never reported)
// carries no expiry. UNKNOWN is not gone: the CP knows nothing, so it must not authorize replacement.
func TestUnknownExpiryIsNotGone(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	ok, reason := RekeyAuthorized("active", time.Time{}, false, now)
	if ok {
		t.Fatalf("an unknown expiry must NOT authorize re-key — the control plane cannot establish the node is "+
			"gone, and 'I cannot tell' is not 'it is fine'. Got %q", reason)
	}
	if !strings.Contains(reason, "no record") || !strings.Contains(reason, "Revoke it explicitly") {
		t.Errorf("the refusal must state that the CP cannot establish absence, and name the explicit remedy; got %q", reason)
	}
	// ...but a revoked node with unknown expiry IS authorized: the human decision stands on its own.
	if ok, _ := RekeyAuthorized("revoked", time.Time{}, false, now); !ok {
		t.Error("revocation authorizes regardless of what is known about expiry — it is an independent evidence")
	}
}

// TestRekeyGateTakesNoForceParameter is a SIGNATURE guard, and it is deliberate.
//
// The pressure to add a force flag arrives later, from a real operator stuck in a real incident. The answer is
// that a guard overridable by the party most motivated to override it is documentation. Encoding that as a
// compile-time property — the function simply has nowhere to put one — is stronger than a comment asking future
// authors not to.
func TestRekeyGateTakesNoForceParameter(t *testing.T) {
	// If a bool were added for "force", this assignment stops compiling and the author has to come back to the
	// paper. That is the point: the test's value is that it must be EDITED, not that it runs.
	var gate func(string, time.Time, bool, time.Time) (bool, string) = RekeyAuthorized
	if gate == nil {
		t.Fatal("unreachable")
	}
}
