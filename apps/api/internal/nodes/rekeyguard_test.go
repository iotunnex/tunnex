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

// TestRekeyNeverUnRevokes — THIS ASSERTION IS THE INVERSE OF THE ONE IT REPLACES, and that inversion is a
// security decision rather than a fix, so the reasoning lives here.
//
// The original red asserted that a REVOKED node AUTHORIZES re-key, on the reasoning that revocation is the
// strongest available evidence a node is gone. That is true, and it was the wrong question. The attack:
//
//  1. an attacker steals a gateway's state volume, which is its private key;
//  2. the operator notices and REVOKES that gateway — the product's answer to a stolen credential;
//  3. the attacker calls re-key, proving possession of the stolen key;
//  4. `revoked` authorizes it;
//  5. the attacker holds a fresh certificate for that node id — active, same site binding, same policy.
//
// Revocation defeated by the exact credential it was invoked against. The paper already forbade this in a
// condition on the same page; the evidence list contradicted it. The condition was right.
//
// A future reader who sees "revoked → refuse" without the chain above will eventually decide it is an
// inconvenience worth relaxing. That is why the chain is here and not only in the paper.
//
// EXPIRY IS AN ABSENCE OF ACTION; REVOCATION IS THE PRESENCE OF A DECISION. A cryptographic proof may overturn
// the first and must never overturn the second: the proof cannot distinguish the legitimate holder from whoever
// took the key, and revocation is precisely the response to that ambiguity.
func TestRekeyNeverUnRevokes(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	// Revoked, certificate still valid.
	if ok, reason := RekeyAuthorized("revoked", future, true, now); ok {
		t.Fatalf("a REVOKED node must NEVER authorize re-key: proof of possession cannot tell the real gateway "+
			"from whoever stole its key, so authorizing would let the stolen credential undo the revocation "+
			"invoked against it. Got authorized with %q", reason)
	}
	// Revoked AND expired — still refused. Expiry does not launder a revocation.
	if ok, reason := RekeyAuthorized("revoked", past, true, now); ok {
		t.Fatalf("revoked AND expired must still refuse — expiry must not launder away a human decision. Got %q", reason)
	}
	// The refusal must name the remedy, and it must be the HUMAN one.
	_, reason := RekeyAuthorized("revoked", past, true, now)
	if !strings.Contains(reason, "join token") {
		t.Errorf("the refusal must direct the operator to the human recovery path (a minted join token); got %q", reason)
	}
}

// TestExpiryIsTheONLYAuthorization — the positive half, and the completeness of the allowlist.
func TestExpiryIsTheONLYAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	if ok, _ := RekeyAuthorized("active", past, true, now); !ok {
		t.Error("an EXPIRED certificate on an ACTIVE node must authorize re-key: the agent cannot authenticate " +
			"and cannot renew, which is the whole condition this epic exists to recover from — and no human " +
			"decided anything, so no decision is being overturned")
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
	if !strings.Contains(reason, "no record") || !strings.Contains(reason, "join token") {
		t.Errorf("the refusal must state that the CP cannot establish absence, and name the human remedy; got %q", reason)
	}
	// SECOND INVERTED ASSERTION, same reasoning as TestRekeyNeverUnRevokes. This previously asserted that a
	// revoked node with unknown expiry IS authorized, on the grounds that "revocation is independent evidence".
	// It is independent evidence that the node is GONE — and simultaneously evidence that a human wanted the
	// key-holder locked out, which is exactly what proof of possession cannot adjudicate.
	if ok, _ := RekeyAuthorized("revoked", time.Time{}, false, now); ok {
		t.Error("revoked + unknown expiry must refuse: neither fact authorizes a return, and the revocation is a " +
			"decision a cryptographic proof must not overturn")
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
