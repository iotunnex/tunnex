package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/control"
)

// TestLoadOrCreateWGKey covers the node-side re-key flow (watch-item a): the key
// is generated locally and persisted; a reload returns the SAME key (stable
// pubkey to report); deleting the file re-keys (new private key, new pubkey).
func TestLoadOrCreateWGKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg.key")

	priv1, pub1, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if priv1 == "" || pub1 == "" {
		t.Fatal("empty key material")
	}

	// Reload must be stable — same private key, same public key to report.
	priv2, pub2, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if priv2 != priv1 || pub2 != pub1 {
		t.Fatal("reload changed the key: pubkey would spuriously re-report")
	}

	// Re-key: losing the file yields a fresh key (private AND public differ).
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	priv3, pub3, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("re-key: %v", err)
	}
	if priv3 == priv1 || pub3 == pub1 {
		t.Fatal("re-key produced the same key — new pubkey must be reported after key loss")
	}
}

// TestPendingKeyIsPersistedBeforeAnySubmit — the half of D10 that lives on the agent, and the half that makes the
// fingerprint identifier usable at all.
//
// A key that exists only in memory when the re-key request goes out is a key this agent cannot prove possession of if
// the response is lost — and the control plane will by then have RECORDED it. So the mint is a mint-and-persist, and
// a second call must return the SAME key rather than a fresh one: a fresh key per attempt would walk the identity
// forward on every retry, leaving the agent proving possession of something the control plane never saw. That is the
// same brick by a longer route.
func TestPendingKeyIsPersistedBeforeAnySubmit(t *testing.T) {
	dir := t.TempDir()

	first, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wasOnDisk {
		t.Fatal("a freshly minted key must report wasOnDisk=false — that flag is what decides whether the " +
			"fingerprint identity is worth trying, and on a first attempt the control plane cannot possibly hold it")
	}
	path := filepath.Join(dir, pendingKeyFile)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the pending key must be ON DISK before any request is built: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("pending key mode is %v, want 0600 — it is private key material like key.pem", fi.Mode().Perm())
	}

	second, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !wasOnDisk {
		t.Fatal("an existing pending key must report wasOnDisk=true")
	}
	if string(second) != string(first) {
		t.Fatal("the pending key must be REUSED across attempts. A fresh key each time means that after a lost " +
			"response the agent holds a key the control plane never recorded — so neither identifier resolves, " +
			"which is the brick D10 exists to remove")
	}

	// It must NOT be mistakable for the real identity: loadCreds and identity.Decide read cert.pem/key.pem, and a
	// pending key that landed in key.pem would be a key with no matching certificate.
	if _, err := os.Stat(filepath.Join(dir, "key.pem")); !os.IsNotExist(err) {
		t.Fatal("minting a pending key must not create key.pem — only a promotion may")
	}
}

// TestUnreadablePendingKeyIsReplacedNotCarried — garbage in the pending file would be submitted, refused, and read
// exactly like a control-plane refusal, sending the operator to look at the wrong side of the wire.
func TestUnreadablePendingKeyIsReplacedNotCarried(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pendingKeyFile), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wasOnDisk {
		t.Fatal("unusable material must not be reported as a key an earlier attempt submitted — the control plane " +
			"cannot be holding it, and trying its fingerprint spends a challenge to learn nothing")
	}
	if _, ferr := control.KeyFingerprintFromPEM(key); ferr != nil {
		t.Fatalf("the replacement must be a usable key: %v", ferr)
	}
}

// TestSaveCredsClearsThePendingKey — promotion is the end of the pending key's life, whichever path got there
// (re-key, renewal, or join-token enrolment; all three go through saveCreds).
//
// A superseded pending key left on disk would make the NEXT recovery try its fingerprint first, spend a challenge and
// a refusal on an identifier the control plane does not hold, and log a refusal that says nothing about the cause.
func TestSaveCredsClearsThePendingKey(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadOrCreatePendingKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := saveCreds(dir, []byte("cert"), []byte("key"), []byte("ca")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingKeyFile)); !os.IsNotExist(err) {
		t.Fatal("saveCreds must clear the pending key: it has been superseded by a real identity")
	}
	for _, f := range []string{"cert.pem", "key.pem", "ca.pem"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s must exist after a promotion: %v", f, err)
		}
	}
}

// --- Batch B: the retry loop, rebuilt --------------------------------------------------------------------

// testCAFor builds a CA and returns its PEM plus a certificate over pub, so a fake control plane can issue
// something the agent will actually accept (the trust-anchor fix means "CERT" no longer passes).
var issueAt func(*rsa.PublicKey, time.Time) []byte

func rekeyHarness(t *testing.T, dir string) (caPEM []byte, issue func(*rsa.PublicKey) []byte) {
	// (dir is unused; kept so callers read as "the harness for this state dir")
	_ = dir
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	issueAt = func(pub *rsa.PublicKey, notAfter time.Time) []byte {
		lt := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "gw"},
			NotBefore: time.Now().Add(-72 * time.Hour), NotAfter: notAfter,
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		d, err := x509.CreateCertificate(rand.Reader, lt, ca, pub, k)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	issue = func(pub *rsa.PublicKey) []byte { return issueAt(pub, time.Now().Add(48*time.Hour)) }
	return caPEM, issue
}

// fakeCP is a control plane that always issues, and counts how many certificates it handed out.
func fakeCP(t *testing.T, issue func(*rsa.PublicKey) []byte, issued *int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"nonce": base64.StdEncoding.EncodeToString([]byte("nonce-0123456789abcdef0123456789")),
		})
	})
	mux.HandleFunc("/api/v1/agent/rekey", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		blk, _ := pem.Decode([]byte(body["csr"]))
		csr, err := x509.ParseCertificateRequest(blk.Bytes)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		*issued++
		_ = json.NewEncoder(w).Encode(map[string]string{
			"cert_pem": string(issue(csr.PublicKey.(*rsa.PublicKey))), "ca_pem": "ignored",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestSaveFailureAfterCommitRETRIESRatherThanLosingTheIdentity — pass 3 claims 7/15.
//
// A saveCreds failure AFTER the control plane committed used to be terminal: the CP had spent its one issuance,
// the agent could not write it, and the loop gave up — falling through to a join token, which creates a NEW node
// and discards the site binding and every device homed on the old one. The identity was destroyed by a full disk.
//
// Under the UNDELIVERED predicate that certificate was never used, so the node still reads undelivered and a retry
// is LEGAL. The loop must take it. This test makes the state directory unwritable for the first attempt and
// restores it, then requires recovery WITHOUT the token being touched.
func TestSaveFailureAfterCommitRETRIESRatherThanLosingTheIdentity(t *testing.T) {
	dir := t.TempDir()
	caPEM, issue := rekeyHarness(t, dir)
	issued := 0
	url := fakeCP(t, issue, &issued)

	oldKey, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := pemToRSA(t, oldKey)
	// The state a recovering gateway is in: an EXPIRED certificate over a key it still holds.
	expired := issueAt(&ok.PublicKey, time.Now().Add(-2*time.Hour))
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	// FAIL THE FIRST PROMOTION, deterministically. Permission tricks do not work (the agent runs as root) and
	// timing tricks SKIP, which reads like a pass — the failure this repo keeps finding.
	real := saveCredsFn
	failures := 0
	saveCredsFn = func(d string, cert, key, ca []byte) error {
		if failures == 0 {
			failures++
			return errors.New("simulated: no space left on device")
		}
		return real(d, cert, key, ca)
	}
	t.Cleanup(func() { saveCredsFn = real })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cert, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), url, dir,
		expired, oldKey, caPEM, "gw", true /* haveToken — the destructive fallback is available and must NOT be taken */)

	if outcome == rekeyExhausted {
		t.Fatal("a LOCAL write failure must not exhaust the re-key path into the join token — that trades a full " +
			"disk for the node's identity, its site binding and every device homed on it")
	}
	if outcome != rekeyRecovered || len(cert) == 0 {
		t.Fatalf("the retry must recover: the certificate the control plane issued was NEVER DELIVERED, so "+
			"re-issuing it is legal under the undelivered predicate; got outcome=%v", outcome)
	}
	if failures != 1 {
		t.Fatalf("the harness should have failed exactly one promotion; got %d", failures)
	}
	if issued < 2 {
		t.Fatalf("expected a second issuance after the failed write, got %d", issued)
	}
}

// TestAValidCertificateCANCELSAReKeyInFlight — pass 3 claims 2/4/13/20.
//
// Expiry was decided ONCE, at boot, from the clock. A gateway that started before NTP settled concluded its
// certificate had expired and then never asked again — trapped in an unbounded refusal loop while holding a
// perfectly valid credential.
//
// DIRECTION, ASSERTED: re-reading the clock can only CANCEL. This test hands the loop a VALID certificate — the
// state a corrected clock produces — and requires it to stand down. Nothing in the loop can start a re-key.
func TestAValidCertificateCANCELSAReKeyInFlight(t *testing.T) {
	dir := t.TempDir()
	caPEM, issue := rekeyHarness(t, dir)
	key, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := pemToRSA(t, key)
	valid := issue(&k.PublicKey) // NOT expired

	issued := 0
	url := fakeCP(t, issue, &issued)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), url, dir,
		valid, key, caPEM, "gw", false)
	if outcome != rekeyNotNeeded {
		t.Fatalf("a re-key must STAND DOWN when the clock says the stored certificate is valid — otherwise a fast "+
			"clock at boot traps a healthy gateway in a refusal loop it can never leave; got %v", outcome)
	}
	if issued != 0 {
		t.Fatalf("nothing should have been requested from the control plane; got %d issuances", issued)
	}
}

func pemToRSA(t *testing.T, keyPEM []byte) (*rsa.PrivateKey, error) {
	t.Helper()
	blk, _ := pem.Decode(keyPEM)
	k, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k, nil
}

// TestTheLOSTRESPONSECaseUsesTheFingerprintOnItsNEXTPass — review pass 1 #6.
//
// This is the case D10 exists for, and the process that suffers it is the process that must recover from it.
// `pendingWasOnDisk` decides whether the fingerprint identity is worth trying, and it was sampled ONCE before the
// loop — so the agent persisted a pending key, lost the response, and then spent the rest of its life retrying the
// only identity that could no longer work. The mechanism built for the lost response was switched off in the lost
// response.
//
// The fake control plane here behaves exactly as the real one does after a committed-but-undelivered re-key: the
// old serial names nothing, and only the fingerprint of the key it recorded resolves.
func TestTheLOSTRESPONSECaseUsesTheFingerprintOnItsNEXTPass(t *testing.T) {
	dir := t.TempDir()
	caPEM, issue := rekeyHarness(t, dir)

	var committed *rsa.PublicKey // the key the CP recorded on the lost attempt
	sawFingerprint := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"nonce": base64.StdEncoding.EncodeToString([]byte("nonce-0123456789abcdef0123456789")),
		})
	})
	mux.HandleFunc("/api/v1/agent/rekey", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		blk, _ := pem.Decode([]byte(body["csr"]))
		csr, _ := x509.ParseCertificateRequest(blk.Bytes)
		pub := csr.PublicKey.(*rsa.PublicKey)

		if committed == nil {
			// FIRST SUBMIT: the control plane COMMITS and the answer is lost.
			committed = pub
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if body["key_fingerprint"] == "" {
			// The old serial names nothing now — exactly what the real CP does after the swap.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		sawFingerprint = true
		_ = json.NewEncoder(w).Encode(map[string]string{"cert_pem": string(issue(pub)), "ca_pem": "ignored"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	key, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := pemToRSA(t, key)
	expired := issueAt(&k.PublicKey, time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cert, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL, dir,
		expired, key, caPEM, "gw", false)

	if !sawFingerprint {
		t.Fatal("after a lost response the agent must try the FINGERPRINT identity on its next pass — the pending " +
			"key it persisted before submitting is the only handle it has left, and sampling that fact once before " +
			"the loop switches the mechanism off in the exact case it was built for")
	}
	if outcome != rekeyRecovered || len(cert) == 0 {
		t.Fatalf("the lost-response case must recover in-process; got %v", outcome)
	}
}

// fastBackoff shrinks the retry intervals so a test can reach the loop's EXIT without sitting through minutes of
// production backoff. It restores them afterwards.
func fastBackoff(t *testing.T) {
	t.Helper()
	f, c := rekeyBackoffFloor, rekeyBackoffCeiling
	rekeyBackoffFloor, rekeyBackoffCeiling = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { rekeyBackoffFloor, rekeyBackoffCeiling = f, c })
}

// TestPersistentRefusalREACHESTheJoinToken — review pass 1 #5.
//
// The loop had NO exit. A persistently refused gateway retried toward the hour ceiling forever while the join
// token the operator had already supplied sat unused — and the log told them to do the very thing the agent was
// refusing to act on. This re-created EPIC 11's WF-S11-11 (operator supplies a token, agent ignores it) one layer
// further in, and it broke D1's story end to end: a REVOKED gateway can only recover through a token, and re-key
// refuses revoked nodes by design, so the refusal that must hand over is exactly the one that never did.
func TestPersistentRefusalREACHESTheJoinToken(t *testing.T) {
	fastBackoff(t)
	dir := t.TempDir()
	caPEM, _ := rekeyHarness(t, dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"nonce": base64.StdEncoding.EncodeToString([]byte("nonce-0123456789abcdef0123456789")),
		})
	})
	// The control plane refuses, uniformly and forever — a revoked node, or one whose key it never recorded.
	mux.HandleFunc("/api/v1/agent/rekey", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	key, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := pemToRSA(t, key)
	expired := issueAt(&k.PublicKey, time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL, dir,
		expired, key, caPEM, "gw", true /* the operator supplied a token */)

	if outcome == rekeyCancelled {
		t.Fatal("the loop ran until the context expired instead of handing over — that is the defect: the operator " +
			"already did what the log asked and the agent never acted on it")
	}
	if outcome != rekeyExhausted {
		t.Fatalf("persistent refusal WITH a token available must exhaust into the fallback; got %v", outcome)
	}
}

// TestPersistentRefusalWithoutATokenKeepsTrying — the other half, asserted so the exit is not widened.
//
// With no token there is nothing to hand over TO, and retrying forever is strictly better than idling forever: the
// control plane may still be fixed underneath us. The loop must NOT exit here.
func TestPersistentRefusalWithoutATokenKeepsTrying(t *testing.T) {
	fastBackoff(t)
	dir := t.TempDir()
	caPEM, _ := rekeyHarness(t, dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"nonce": base64.StdEncoding.EncodeToString([]byte("nonce-0123456789abcdef0123456789")),
		})
	})
	mux.HandleFunc("/api/v1/agent/rekey", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	key, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := pemToRSA(t, key)
	expired := issueAt(&k.PublicKey, time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL, dir,
		expired, key, caPEM, "gw", false /* no token */)

	if outcome != rekeyCancelled {
		t.Fatalf("with no token there is nothing to hand over to, so the loop must keep trying until the context "+
			"ends — and a context end is a SHUTDOWN, never a refusal; got %v", outcome)
	}
}

// TestTransientFailuresNEVERSpendTheIdentity — review pass 1 #10, at the loop level.
//
// control.Rekey now distinguishes a 403 from everything else, but the consequence that matters is here: a
// transient fault must not COUNT as a refusal, because refusals are what hand the gateway over to the join token —
// and a token enrolment creates a NEW node, discarding the site binding and every device homed on the old one.
//
// So a control plane that is merely DOWN must never cost a gateway its identity. This runs against one returning
// 500 forever, with a token available and the refusal threshold long since passed in wall-clock terms.
func TestTransientFailuresNEVERSpendTheIdentity(t *testing.T) {
	fastBackoff(t)
	dir := t.TempDir()
	caPEM, _ := rekeyHarness(t, dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // the control plane is restarting behind a proxy
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	key, err := control.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := pemToRSA(t, key)
	expired := issueAt(&k.PublicKey, time.Now().Add(-2*time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _, outcome := attemptRekey(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL, dir,
		expired, key, caPEM, "gw", true /* a token IS available — and must not be reached */)

	if outcome == rekeyExhausted {
		t.Fatal("a control plane that is DOWN must never exhaust the re-key path into the join token: nothing " +
			"refused anything, and enrolling would discard this node's identity, its site binding and every " +
			"device homed on it — the worst possible response to an outage")
	}
	if outcome != rekeyCancelled {
		t.Fatalf("with only transient faults the loop must keep retrying until the context ends; got %v", outcome)
	}
}
