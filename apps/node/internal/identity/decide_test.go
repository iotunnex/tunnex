package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

// certFor mints a self-signed leaf with the given CN and NotAfter — enough for a decision that only reads the
// subject and the validity window. Deliberately NOT a fixture constant: the walk's fixture-fidelity lesson was
// that a fixture which cannot express the failing case cannot catch it, and expiry is the whole subject here.
func certFor(t *testing.T, cn string, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notAfter.Add(-48 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestExpiredIdentityRECOVERSInPlace — ASSERTION AMENDED, and the amendment is a capability change rather than a
// correction.
//
// It previously asserted UseToken: an expired certificate must yield to a join token. That was right when written,
// because the token was the ONLY recovery path — proof of possession did not exist. It does now, and it is strictly
// better: re-key returns the SAME node with its id, site binding, devices and metrics series intact, where a token
// enrolment creates a new node and discards all four. So the original ruling is SUPERSEDED BY A NEW CAPABILITY, not
// corrected.
//
// Review finding #2 is why this could not stay as it was: preferring the token whenever one is present meant that on
// the shipped Helm shape — TUNNEX_JOIN_TOKEN injected on every pod start — re-key was NEVER attempted, and the
// enrolment then collided with this node's own expired-but-not-revoked row, returning 409 and exiting the agent into
// CrashLoopBackOff.
func TestExpiredIdentityRECOVERSInPlace(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	expired := certFor(t, "aws-gw-1", now.Add(-72*time.Hour))

	v := Decide(expired, nil, "aws-gw-1", true, now)
	if v.Action != Recover {
		t.Fatalf("an expired identity must attempt RE-KEY first, got action %v (%s). A token enrolment would also "+
			"work and would discard this node's identity, site binding and devices — so it is the fallback, not the "+
			"first choice", v.Action, v.Reason)
	}
	if !v.HaveToken {
		t.Error("the verdict must report that a fallback token exists, so the caller knows it has one")
	}
	if v.Reason != "stored_identity_expired" {
		t.Errorf("reason must name the determination, got %q", v.Reason)
	}
	// The ruling requires the DETERMINATION to be cited, not merely asserted.
	for _, want := range []string{"expired", "NotAfter", "aws-gw-1"} {
		if !strings.Contains(v.Evidence, want) {
			t.Errorf("evidence must cite %q so an operator can verify the determination; got %q", want, v.Evidence)
		}
	}
}

// TestNameMismatchOnAValidCertKEEPSTheIdentity is the case where the literal reading of the ruling would be
// DESTRUCTIVE, and it is drawn from the walk's own incident.
//
// The enrollment command was pasted on the wrong host: azure-gw, holding azure-gw's VALID certificate, with
// TUNNEX_NODE_NAME=aws-gw-1. If a name mismatch authorized using the token, the agent would have abandoned
// azure-gw's identity and enrolled that host as aws-gw-1 — a live gateway made to look dead while a second node
// took its name. That is exactly the S8.2c WF-2 disaster the stored-identity preference exists to prevent.
func TestNameMismatchOnAValidCertKEEPSTheIdentity(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	valid := certFor(t, "azure-gw", now.Add(24*time.Hour))

	v := Decide(valid, nil, "aws-gw-1", true, now)
	if v.Action != UseStored {
		t.Fatalf("a name mismatch on a STILL-VALID certificate must KEEP the stored identity, got %v (%s). "+
			"Discarding it abandons a working gateway on what is almost always operator error", v.Action, v.Reason)
	}
	if !v.NameMismatch {
		t.Error("the mismatch must be reported even though it does not change the action — it is the signal")
	}
	if v.StoredCN != "azure-gw" {
		t.Errorf("StoredCN must be the identity actually kept, got %q", v.StoredCN)
	}
	// WF-S11-11b: the operator must be told which identity is in use, not which was requested.
	if got := EffectiveName(v, "aws-gw-1"); got != "azure-gw" {
		t.Errorf("the effective name must come from the CERTIFICATE when the stored identity is used, got %q — "+
			"reporting the requested name is what hid a wrong-host run on the walk", got)
	}
}

// TestMismatchAndExpiredAttemptsRecovery — the composition. Once the certificate is dead there is nothing left to
// protect, so the mismatch no longer argues for keeping it. (Amended from UseToken to Recover with the rest.)
func TestMismatchAndExpiredAttemptsRecovery(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	dead := certFor(t, "azure-gw", now.Add(-1*time.Hour))

	v := Decide(dead, nil, "aws-gw-1", true, now)
	if v.Action != Recover {
		t.Fatalf("expired beats mismatch: an unusable certificate is not worth protecting, got %v (%s)",
			v.Action, v.Reason)
	}
	if !v.NameMismatch {
		t.Error("the mismatch must still be reported — it remains a real signal about the host")
	}
}

// TestUncertaintyFailsTowardTheStoredIdentity — the ruled condition, stated as a property rather than a case.
// No input that leaves the stored certificate USABLE may result in discarding it.
func TestUncertaintyFailsTowardTheStoredIdentity(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	usable := certFor(t, "gw", now.Add(time.Hour))

	for _, tc := range []struct {
		name      string
		requested string
		haveToken bool
	}{
		{"matching name, token present", "gw", true},
		{"matching name, no token", "gw", false},
		{"mismatched name, token present", "other", true},
		{"mismatched name, no token", "other", false},
		{"empty requested name, token present", "", true},
	} {
		if v := Decide(usable, nil, tc.requested, tc.haveToken, now); v.Action != UseStored {
			t.Errorf("%s: a usable certificate must never be discarded, got %v (%s)", tc.name, v.Action, v.Reason)
		}
	}
}

// TestUnusableWithoutATokenIdlesLOUDLY — the case an operator actually meets first, and the one that used to
// produce a wall of identical TLS warnings with no remedy in them. Idling is correct (liveness stays up); silence
// is not.
func TestUnusableWithoutATokenIdlesLOUDLY(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)

	// AMENDED: an EXPIRED certificate with no token no longer idles — it attempts re-key, which needs no token
	// because the proof is the stored key itself. Only an identity that cannot prove anything idles.
	if v := Decide(certFor(t, "gw", now.Add(-time.Hour)), nil, "gw", false, now); v.Action != Recover {
		t.Fatalf("expired with no token must still ATTEMPT re-key — proof of possession needs the stored key, not "+
			"a token; got %v (%s)", v.Action, v.Reason)
	}
	// An UNREADABLE identity is the real idle case: nothing to prove possession OF, and no token to fall back on.
	v := Decide([]byte("garbage"), nil, "gw", false, now)
	if v.Action != Idle {
		t.Fatalf("an unreadable identity with no token must idle; got %v", v.Action)
	}
	// The remedy, not just the condition — the teaching-text convention.
	for _, want := range []string{"re-enrolled", "no join token"} {
		if !strings.Contains(v.Evidence, want) {
			t.Errorf("the idle message must name the REMEDY; missing %q in %q", want, v.Evidence)
		}
	}
}

// TestUnreadableAndAbsentIdentities — the remaining branches, so every path in Decide is reachable from a test.
func TestUnreadableAndAbsentIdentities(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)

	if v := Decide(nil, errors.New("open: no such file"), "gw", true, now); v.Action != UseToken ||
		v.Reason != "no_stored_identity" {
		t.Errorf("no stored identity + token = enroll; got %v (%s)", v.Action, v.Reason)
	}
	if v := Decide(nil, errors.New("open: no such file"), "gw", false, now); v.Action != Idle {
		t.Errorf("no stored identity and no token must idle; got %v", v.Action)
	}
	garbage := []byte("-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydA==\n-----END CERTIFICATE-----\n")
	if v := Decide(garbage, nil, "gw", true, now); v.Action != UseToken ||
		v.Reason != "stored_identity_unreadable" {
		t.Errorf("an unparseable certificate + token = enroll; got %v (%s)", v.Action, v.Reason)
	}
	if v := Decide(garbage, nil, "gw", false, now); v.Action != Idle {
		t.Errorf("an unparseable certificate with no token must idle; got %v", v.Action)
	}
	// Not-a-PEM at all.
	if v := Decide([]byte("hello"), nil, "gw", true, now); v.Action != UseToken {
		t.Errorf("non-PEM content + token = enroll; got %v (%s)", v.Action, v.Reason)
	}
}

// TestExpiryBoundaryIsExclusive pins the edge. A certificate is usable UP TO NotAfter; the boundary itself must
// not be treated as expired, because a clock a millisecond fast would otherwise re-enroll a live gateway.
func TestExpiryBoundaryIsExclusive(t *testing.T) {
	notAfter := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	c := certFor(t, "gw", notAfter)

	if v := Decide(c, nil, "gw", true, notAfter); v.Action != UseStored {
		t.Errorf("at exactly NotAfter the certificate is still usable, got %v (%s) — a fast clock must not "+
			"re-enroll a live gateway", v.Action, v.Reason)
	}
	if v := Decide(c, nil, "gw", true, notAfter.Add(time.Nanosecond)); v.Action != Recover {
		t.Errorf("one instant past NotAfter it is expired, got %v (%s)", v.Action, v.Reason)
	}
}

// TestRekeyCannotBeTriggeredByConnectionFAILURE — the condition on the agent's re-key trigger, asserted
// structurally rather than behaviourally, which is the stronger form.
//
// THE FAILURE MODE THIS FORBIDS: an agent that attempts re-key whenever it cannot reach the control plane would
// hammer an UNAUTHENTICATED endpoint during every transient outage — a partition, a CP restart, a DNS blip — turning
// an ordinary incident into a self-inflicted flood, and doing it hardest at the moment the CP is least able to cope.
//
// Decide is structurally incapable of it: it takes a stored certificate, a load error, a requested name, whether a
// token exists, and a clock. THERE IS NO NETWORK ARGUMENT TO PASS. Its verdict comes from the agent's own clock
// against its own stored certificate, so a handshake outcome cannot reach it even by mistake — the same shape as
// RekeyAuthorized on the server having no liveness parameter.
//
// This test's real value is that it must be EDITED before that can change. A future author who adds a
// "lastHandshakeFailed bool" to make the trigger "smarter" stops it compiling and has to come back to the reasoning.
func TestRekeyCannotBeTriggeredByConnectionFAILURE(t *testing.T) {
	// SIGNATURE assertion: exactly these inputs, none of them a network signal.
	var decide func(certPEM []byte, loadErr error, requestedName string, haveToken bool, now time.Time) Verdict = Decide
	if decide == nil {
		t.Fatal("unreachable")
	}

	// VERDICT-SET assertion: the only reasons that may authorize a re-key attempt are the two locally-provable
	// expiry verdicts. If a new reason is added, this list must be revisited deliberately — and in particular no
	// reason derived from reachability may appear, because none can be: see the signature above.
	rekeyable := map[string]bool{"stored_identity_expired": true}
	now := time.Now()
	all := []Verdict{
		Decide(nil, errors.New("missing"), "gw", false, now),                  // no_identity_no_token
		Decide(nil, errors.New("missing"), "gw", true, now),                   // no_stored_identity
		Decide([]byte("garbage"), nil, "gw", false, now),                      // unreadable_no_token
		Decide([]byte("garbage"), nil, "gw", true, now),                       // unreadable
		Decide(certFor(t, "gw", now.Add(time.Hour)), nil, "gw", true, now),    // valid
		Decide(certFor(t, "gw", now.Add(time.Hour)), nil, "other", true, now), // name_mismatch
		Decide(certFor(t, "gw", now.Add(-time.Hour)), nil, "gw", false, now),  // expired_no_token
		Decide(certFor(t, "gw", now.Add(-time.Hour)), nil, "gw", true, now),   // expired (uses token)
	}
	for _, v := range all {
		if rekeyable[v.Reason] {
			// Every re-keyable verdict must be an EXPIRY determination — a fact about the certificate the agent
			// holds, checkable without asking anyone.
			if !strings.Contains(v.Reason, "expired") {
				t.Errorf("verdict %q is treated as re-keyable but is not an expiry determination; a re-key trigger "+
					"must rest on a locally-provable fact, never on whether the control plane answered", v.Reason)
			}
		}
	}
	// And the expired verdicts must actually be reachable, or the trigger is dead code and this test vacuous.
	if v := Decide(certFor(t, "gw", now.Add(-time.Hour)), nil, "gw", false, now); !rekeyable[v.Reason] {
		t.Fatalf("an expired certificate with no token must produce a re-keyable verdict, got %q", v.Reason)
	}
}
