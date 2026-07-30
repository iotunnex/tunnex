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

// TestExpiredIdentityYieldsToTheToken — the defect WF-S11-11 named, as a red.
//
// An agent holding an expired certificate and handed a valid join token previously kept the certificate, logged a
// WARN, and looped forever: /agent/renew is behind the same client-cert requirement as every other agent route,
// so the only endpoint that could issue a new certificate requires the one that expired. The operator did what
// the docs prescribe and the product ignored them.
func TestExpiredIdentityYieldsToTheToken(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	expired := certFor(t, "aws-gw-1", now.Add(-72*time.Hour))

	v := Decide(expired, nil, "aws-gw-1", true, now)
	if v.Action != UseToken {
		t.Fatalf("an expired identity must yield to a join token, got action %v (%s). Keeping it is a deadlock: "+
			"renewal requires the certificate that expired", v.Action, v.Reason)
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

// TestMismatchAndExpiredYieldsToTheToken — the composition. Once the certificate is dead there is nothing left to
// protect, so the mismatch no longer argues for keeping it.
func TestMismatchAndExpiredYieldsToTheToken(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	dead := certFor(t, "azure-gw", now.Add(-1*time.Hour))

	v := Decide(dead, nil, "aws-gw-1", true, now)
	if v.Action != UseToken {
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

	v := Decide(certFor(t, "gw", now.Add(-time.Hour)), nil, "gw", false, now)
	if v.Action != Idle {
		t.Fatalf("expired with no token must idle, not proceed into a retry loop that cannot succeed; got %v", v.Action)
	}
	// The remedy, not just the condition — the teaching-text convention.
	for _, want := range []string{"CANNOT recover", "Re-enroll", "TUNNEX_JOIN_TOKEN"} {
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
	if v := Decide(c, nil, "gw", true, notAfter.Add(time.Nanosecond)); v.Action != UseToken {
		t.Errorf("one instant past NotAfter it is expired, got %v (%s)", v.Action, v.Reason)
	}
}
