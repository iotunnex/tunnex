package control

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrRekeyRefused is what the control plane's uniform refusal looks like from here.
//
// The CP deliberately does not say WHY (a live node, an unknown serial, a spent nonce and a wrong key are
// indistinguishable, so the endpoint cannot be used as an oracle). So the agent must not try to interpret the
// refusal — it reports what it knows LOCALLY instead, which is the only honest thing it can say.
var ErrRekeyRefused = errors.New("control plane refused the re-key")

// Rekey recovers an identity whose certificate has expired, by proving possession of the keypair the control plane
// already recorded for it (S13.1).
//
// It runs over the PUBLIC API listener, not the mTLS agent channel — necessarily, because the certificate that
// would authenticate there is the thing that has expired.
//
// Two round trips, and both are required: the nonce makes a captured request unreplayable, and signing over
// (nonce ‖ CSR DER) binds the proof to this exact request so a captured proof cannot be paired with someone else's
// CSR.
func Rekey(ctx context.Context, apiURL, certSerial string, oldKeyPEM []byte, agentVersion, commonName string) (newKeyPEM, certPEM, caPEM []byte, err error) {
	oldKey, err := parseRSAKey(oldKeyPEM)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stored key unusable: %w", err)
	}

	// A BRAND NEW keypair. The old private key may be compromised or about to be discarded, so recovery always
	// issues over fresh material — the old key's only job here is to prove who is asking.
	newKeyPEM, csrPEM, err := GenerateKeyAndCSR(commonName)
	if err != nil {
		return nil, nil, nil, err
	}
	csrBlk, _ := pem.Decode(csrPEM)
	if csrBlk == nil {
		return nil, nil, nil, errors.New("generated CSR is not PEM")
	}

	nonce, err := rekeyChallenge(ctx, apiURL, certSerial)
	if err != nil {
		return nil, nil, nil, err
	}

	// The signed message must match the server's construction exactly. Kept adjacent to the request that carries
	// it so the two cannot drift apart silently.
	msg := append(append([]byte{}, nonce...), csrBlk.Bytes...)
	sum := sha256.Sum256(msg)
	sig, err := rsa.SignPKCS1v15(rand.Reader, oldKey, crypto.SHA256, sum[:])
	if err != nil {
		return nil, nil, nil, err
	}

	body, _ := json.Marshal(map[string]string{
		"cert_serial":   certSerial,
		"nonce":         base64.StdEncoding.EncodeToString(nonce),
		"csr":           string(csrPEM),
		"signature":     base64.StdEncoding.EncodeToString(sig),
		"agent_version": agentVersion,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/v1/agent/rekey", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, ErrRekeyRefused
	}
	var out struct {
		CertPEM string `json:"cert_pem"`
		CAPEM   string `json:"ca_pem"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, nil, err
	}
	return newKeyPEM, []byte(out.CertPEM), []byte(out.CAPEM), nil
}

func rekeyChallenge(ctx context.Context, apiURL, certSerial string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"cert_serial": certSerial})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/v1/agent/rekey/challenge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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
