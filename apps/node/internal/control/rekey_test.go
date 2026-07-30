package control

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rekeyServer is a control plane that RECORDS what the agent sent, so the reds can assert the request rather than
// only the outcome.
type rekeyServer struct {
	nonce     []byte
	lastBody  map[string]string
	challenge map[string]string // the identifier fields the challenge call carried
	refuse    bool
}

func (s *rekeyServer) start(t *testing.T) string {
	t.Helper()
	s.nonce = []byte("server-issued-nonce-0123456789ab")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/rekey/challenge", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.challenge = body
		_ = json.NewEncoder(w).Encode(map[string]string{"nonce": base64.StdEncoding.EncodeToString(s.nonce)})
	})
	mux.HandleFunc("/api/v1/agent/rekey", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.lastBody = body
		if s.refuse {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"cert_pem": "CERT", "ca_pem": "CA"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pubOf(t *testing.T, keyPEM []byte) *rsa.PublicKey {
	t.Helper()
	k, err := parseRSAKey(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &k.PublicKey
}

// TestRekeyIssuesOverThePENDINGKeyNotAFreshOne — the convergence property (S13.1 D10).
//
// The CSR must carry the key the CALLER persisted, because that is the key the control plane will record and the key
// the agent will have to prove possession of if this response is lost. If Rekey minted its own, every retry would
// record a different key and the agent would end up proving possession of one the control plane never saw.
func TestRekeyIssuesOverThePENDINGKeyNotAFreshOne(t *testing.T) {
	srv := &rekeyServer{}
	url := srv.start(t)
	pending, old := testKey(t), testKey(t)

	if _, _, err := Rekey(t.Context(), url, Identifier{CertSerial: "S1"}, pending, old, "0.1.0", "gw"); err != nil {
		t.Fatalf("rekey: %v", err)
	}

	blk, _ := pem.Decode([]byte(srv.lastBody["csr"]))
	if blk == nil {
		t.Fatal("no CSR was sent")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := csr.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("CSR key is not RSA")
	}
	if got.N.Cmp(pubOf(t, pending).N) != 0 {
		t.Fatal("the CSR must be over the PENDING key the caller persisted. Minting a fresh key here means a lost " +
			"response leaves the agent unable to prove possession of what the control plane recorded — the brick " +
			"D10 removes, reintroduced one layer down")
	}
}

// TestProofIsSignedByThePoPKeyAndBoundToTheCSR — the two keys have different jobs and must not be conflated: the
// pending key is what the certificate is issued OVER, the PoP key is what says who is asking.
func TestProofIsSignedByThePoPKeyAndBoundToTheCSR(t *testing.T) {
	srv := &rekeyServer{}
	url := srv.start(t)
	pending, old := testKey(t), testKey(t)

	if _, _, err := Rekey(t.Context(), url, Identifier{CertSerial: "S1"}, pending, old, "0.1.0", "gw"); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(srv.lastBody["signature"])
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(srv.lastBody["csr"]))
	sum := sha256.Sum256(append(append([]byte{}, srv.nonce...), blk.Bytes...))

	if err := rsa.VerifyPKCS1v15(pubOf(t, old), crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("the proof must verify against the PoP key over (nonce || CSR DER): %v", err)
	}
	if rsa.VerifyPKCS1v15(pubOf(t, pending), crypto.SHA256, sum[:], sig) == nil {
		t.Fatal("the proof must NOT be signed by the pending key when a separate PoP key was supplied — the control " +
			"plane verifies against the key it RECORDED, and signing with the new one would be a proof of nothing")
	}
}

// TestIdentifierIsCarriedOnBOTHRoundTRIPS — the nonce is bound to its identifier server-side, so a challenge taken
// out under one and spent under the other is refused. Sending the identifier only on the submit (or only on the
// challenge) would make fingerprint recovery fail in a way that looks like a refusal.
func TestIdentifierIsCarriedOnBOTHRoundTRIPS(t *testing.T) {
	fp := "1e98cb7cd8f91d59b2f90727f5543f9c9e5413332b160c93534c283ea3bdba94"
	for _, c := range []struct {
		name  string
		ident Identifier
		field string
		want  string
	}{
		{"serial", Identifier{CertSerial: "S1"}, "cert_serial", "S1"},
		{"fingerprint", Identifier{KeyFingerprint: fp}, "key_fingerprint", fp},
	} {
		srv := &rekeyServer{}
		url := srv.start(t)
		pending := testKey(t)
		if _, _, err := Rekey(t.Context(), url, c.ident, pending, pending, "0.1.0", "gw"); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if srv.challenge[c.field] != c.want {
			t.Errorf("%s: challenge carried %v, want %s=%s", c.name, srv.challenge, c.field, c.want)
		}
		if srv.lastBody[c.field] != c.want {
			t.Errorf("%s: submit carried %v, want %s=%s", c.name, srv.lastBody, c.field, c.want)
		}
		// EXACTLY ONE identifier on the wire: the control plane refuses a request carrying both, so sending an empty
		// second field would have to be handled as absent — a dependency on the server's emptiness semantics that
		// this simply does not create.
		other := "key_fingerprint"
		if c.field == other {
			other = "cert_serial"
		}
		if _, present := srv.lastBody[other]; present {
			t.Errorf("%s: the unused identifier must be ABSENT from the body, not empty — the control plane refuses "+
				"a request that names two identities", c.name)
		}
	}
}

// TestRefusalIsNotThrottled — the review-#10 distinction, kept at the boundary between the two identities: a 403
// refusal and a 429 must remain different errors, because the agent's retry behaviour and its diagnosis differ.
func TestRefusalIsNotThrottled(t *testing.T) {
	srv := &rekeyServer{refuse: true}
	url := srv.start(t)
	pending := testKey(t)
	_, _, err := Rekey(t.Context(), url, Identifier{CertSerial: "S1"}, pending, pending, "0.1.0", "gw")
	if !errors.Is(err, ErrRekeyRefused) {
		t.Fatalf("a 403 must be ErrRekeyRefused, got %v", err)
	}
	if errors.Is(err, ErrRekeyThrottled) {
		t.Fatal("a refusal must never read as a throttle")
	}
}

var _ = rand.Reader
