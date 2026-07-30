package control

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ErrRekeyRefused is what the control plane's uniform refusal looks like from here.
//
// The CP deliberately does not say WHY (a live node, an unknown serial, a spent nonce and a wrong key are
// indistinguishable, so the endpoint cannot be used as an oracle). So the agent must not try to interpret the
// refusal — it reports what it knows LOCALLY instead, which is the only honest thing it can say.
var ErrRekeyRefused = errors.New("control plane refused the re-key")

// ErrRekeyThrottled is DISTINCT from a refusal, and the distinction is the point (review #10).
//
// A refusal means "this will never work" — the node is revoked, or its key predates key recording — and the right
// response is to back off toward the hour ceiling and tell the operator to mint a join token. A 429 means "not
// right now", and treating it as a refusal defeats the honest-diagnosis property this slice was built around: the
// agent would double its backoff toward an hour and print a most-likely-cause pointing at revocation, for what is
// purely a rate limit. Throttled means retry SOONER and say so.
var ErrRekeyThrottled = errors.New("control plane throttled the re-key attempt")

// retryAfter reads the server's Retry-After when it is a plain seconds value, which is what the throttle sends.
// Returns 0 when absent or unparseable — the caller then picks its own short delay rather than guessing long.
func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 && secs < 3600 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// Identifier is how this agent names itself to an endpoint that cannot authenticate it (S13.1 D10).
//
// EXACTLY ONE of the two is set. The serial is what an agent normally knows; the fingerprint is what it still knows
// after a LOST RESPONSE, when the control plane has moved to a serial this agent never received. See PendingKey.
type Identifier struct {
	CertSerial     string
	KeyFingerprint string
}

func (i Identifier) fields() map[string]string {
	if i.KeyFingerprint != "" {
		return map[string]string{"key_fingerprint": i.KeyFingerprint}
	}
	return map[string]string{"cert_serial": i.CertSerial}
}

// Describe names the identifier for logs, without implying the CP said anything about it.
func (i Identifier) Describe() string {
	if i.KeyFingerprint != "" {
		return "key_fingerprint " + i.KeyFingerprint[:min(12, len(i.KeyFingerprint))]
	}
	return "cert_serial " + i.CertSerial
}

// GenerateKey mints a fresh RSA-2048 private key, WITHOUT a CSR.
//
// Separate from GenerateKeyAndCSR because re-key must PERSIST the new key BEFORE it submits anything. A key that
// exists only in memory when the request goes out is a key this agent cannot prove possession of if the response is
// lost — which is precisely how a dropped packet used to cost a gateway its identity.
func GenerateKey() (keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
}

// KeyFingerprintFromPEM computes the identifier for a private key's PUBLIC half: SHA-256 over the SPKI DER,
// lowercase hex.
//
// PINNED TO A GOLDEN VECTOR, in a test in this module and in the control plane's (nodes.KeyFingerprint) and in an
// integration test against the database's generated column. Three implementations of one digest, in two Go modules
// that cannot import each other plus one in SQL — so they agree on purpose rather than by luck. If you change this,
// the golden fails in all three places, which is the point.
func KeyFingerprintFromPEM(keyPEM []byte) (string, error) {
	k, err := parseRSAKey(keyPEM)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", err
	}
	return KeyFingerprintFromSPKI(der), nil
}

// KeyFingerprintFromSPKI is the digest itself, separated out so a test can pin it to a golden PUBLIC key without a
// private key ever entering the repository. Over the DER, not over any text encoding of it: a key's identity must not
// depend on how it was written down.
func KeyFingerprintFromSPKI(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return hex.EncodeToString(sum[:])
}

// csrForKey builds a CSR over an EXISTING key, rather than minting a new one.
func csrForKey(keyPEM []byte, commonName string) (csrPEM []byte, err error) {
	k, err := parseRSAKey(keyPEM)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}, k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// Rekey recovers an identity whose certificate has expired, by proving possession of the keypair the control plane
// already recorded for it (S13.1).
//
// It runs over the PUBLIC API listener, not the mTLS agent channel — necessarily, because the certificate that
// would authenticate there is the thing that has expired.
//
// Two round trips, and both are required: the nonce makes a captured request unreplayable, and signing over
// (nonce ‖ CSR DER) binds the proof to this exact request so a captured proof cannot be paired with someone else's
// CSR.
//
// THE CALLER OWNS THE NEW KEY, and that is a correctness requirement rather than an API preference. The pending key
// must be on disk BEFORE the request goes out, so a lost response leaves this agent able to prove possession of the
// key the control plane just recorded. Rekey therefore takes the pending key rather than minting one.
//
// popKeyPEM is whichever key the control plane RECORDED for this node — the expired certificate's key when
// identifying by serial, or the pending key itself when identifying by its fingerprint after a lost response.
func Rekey(ctx context.Context, apiURL string, ident Identifier, pendingKeyPEM, popKeyPEM []byte, agentVersion, commonName string) (certPEM, caPEM []byte, err error) {
	popKey, err := parseRSAKey(popKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("stored key unusable: %w", err)
	}

	// The CSR is over the PENDING key — fresh material, minted and persisted by the caller before this call. The
	// proving key's only job is to say who is asking; when the two are the same key, this is a retry re-asserting an
	// identity the control plane may already hold, which is what makes repeated lost responses CONVERGE instead of
	// walking the identity forward on every attempt.
	csrPEM, err := csrForKey(pendingKeyPEM, commonName)
	if err != nil {
		return nil, nil, err
	}
	csrBlk, _ := pem.Decode(csrPEM)
	if csrBlk == nil {
		return nil, nil, errors.New("generated CSR is not PEM")
	}

	nonce, err := rekeyChallenge(ctx, apiURL, ident)
	if err != nil {
		return nil, nil, err
	}

	// The signed message must match the server's construction exactly. Kept adjacent to the request that carries
	// it so the two cannot drift apart silently.
	msg := append(append([]byte{}, nonce...), csrBlk.Bytes...)
	sum := sha256.Sum256(msg)
	sig, err := rsa.SignPKCS1v15(rand.Reader, popKey, crypto.SHA256, sum[:])
	if err != nil {
		return nil, nil, err
	}

	fields := ident.fields()
	fields["nonce"] = base64.StdEncoding.EncodeToString(nonce)
	fields["csr"] = string(csrPEM)
	fields["signature"] = base64.StdEncoding.EncodeToString(sig)
	fields["agent_version"] = agentVersion
	body, _ := json.Marshal(fields)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/v1/agent/rekey", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("%w (retry after %s)", ErrRekeyThrottled, retryAfter(resp))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, ErrRekeyRefused
	}
	var out struct {
		CertPEM string `json:"cert_pem"`
		CAPEM   string `json:"ca_pem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	return []byte(out.CertPEM), []byte(out.CAPEM), nil
}

func rekeyChallenge(ctx context.Context, apiURL string, ident Identifier) ([]byte, error) {
	body, _ := json.Marshal(ident.fields())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/v1/agent/rekey/challenge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w (retry after %s)", ErrRekeyThrottled, retryAfter(resp))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrRekeyRefused
	}
	var out struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Nonce)
}

func parseRSAKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode(keyPEM)
	if blk == nil {
		return nil, errors.New("not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	any, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	k, ok := any.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return k, nil
}
