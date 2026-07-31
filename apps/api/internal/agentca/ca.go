// Package agentca is the certificate authority that signs tunnex-node agent
// mTLS certificates. Its private key is a root of trust: sealed at rest under
// the S0.3 master key and stored in platform_secrets.
//
// It follows the master-key contract (S0.3): generated once, loaded thereafter,
// and NEVER silently regenerated — a new CA would orphan every enrolled agent.
package agentca

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

const secretName = "agent_ca"

// CertTTL is the lifetime of an issued agent certificate. Revocation = refuse
// renewal, so a short lifetime bounds a compromised cert's window (S3.1 decision).
const CertTTL = 48 * time.Hour

// sealer is the subset of crypto.Sealer we need.
type sealer interface {
	Seal([]byte) (string, error)
	Open(string) ([]byte, error)
}

// CA signs agent certificates and exposes the cert pool agents/servers verify against.
type CA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     *rsa.PrivateKey
}

// LoadOrCreate loads the CA from platform_secrets, generating it on first boot.
// Fails loudly (never regenerates) if the stored CA is present but unusable.
func LoadOrCreate(ctx context.Context, q *sqlc.Queries, s sealer) (*CA, bool, error) {
	row, err := q.GetPlatformSecret(ctx, secretName)
	if err == nil {
		ca, lerr := load(row, s)
		if lerr != nil {
			return nil, false, fmt.Errorf(
				"agent CA exists but is unusable; refusing to regenerate "+
					"(a new CA would orphan every enrolled agent): %w", lerr)
		}
		return ca, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	ca, sealedKey, certPEM, err := generate(s)
	if err != nil {
		return nil, false, err
	}
	if err := q.InsertPlatformSecret(ctx, sqlc.InsertPlatformSecretParams{
		Name: secretName, SecretSealed: []byte(sealedKey), PublicPem: ptr(string(certPEM)),
	}); err != nil {
		return nil, false, err
	}
	// Re-read in case a concurrent boot won the insert (ON CONFLICT DO NOTHING).
	row, err = q.GetPlatformSecret(ctx, secretName)
	if err != nil {
		return nil, false, err
	}
	loaded, err := load(row, s)
	if err != nil {
		return nil, false, err
	}
	_ = ca
	return loaded, true, nil
}

func generate(s sealer) (*CA, string, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, "", nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          bigSerial(),
		Subject:               pkix.Name{CommonName: "Tunnex Agent CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, "", nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	sealedKey, err := s.Seal(keyPEM)
	if err != nil {
		return nil, "", nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{cert: cert, certPEM: certPEM, key: key}, sealedKey, certPEM, nil
}

func load(row sqlc.PlatformSecret, s sealer) (*CA, error) {
	keyPEM, err := s.Open(string(row.SecretSealed))
	if err != nil {
		return nil, fmt.Errorf("decrypt CA key: %w", err)
	}
	blk, _ := pem.Decode(keyPEM)
	if blk == nil {
		return nil, errors.New("malformed CA key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	if row.PublicPem == nil {
		return nil, errors.New("missing CA certificate")
	}
	cblk, _ := pem.Decode([]byte(*row.PublicPem))
	if cblk == nil {
		return nil, errors.New("malformed CA cert PEM")
	}
	cert, err := x509.ParseCertificate(cblk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}
	return &CA{cert: cert, certPEM: []byte(*row.PublicPem), key: key}, nil
}

// CertPEM returns the CA certificate (safe to distribute).
func (c *CA) CertPEM() []byte { return c.certPEM }

// Pool returns a cert pool trusting this CA (for mTLS client-cert verification).
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.cert)
	return p
}

// Fingerprint is a short, non-reversible id of the CA cert, safe to log.
func (c *CA) Fingerprint() string {
	sum := sha256.Sum256(c.cert.Raw)
	return hex.EncodeToString(sum[:6])
}

// Issued is everything the control plane records about a certificate it just minted. Returned as a struct rather
// than a growing tuple: this is the third field to be added (serial, then NotAfter for S11 WF-S11-6, now the
// public key for S13.1 D7), and each addition existed because the CP had failed to record something it later
// needed to answer a question about its own fleet.
type Issued struct {
	CertPEM string
	Serial  string // stored on the node record; IS the agent's identity
	// NotAfter is the certificate's OWN expiry, returned rather than recomputed by callers. One truth: a caller
	// writing time.Now().Add(CertTTL) into the node row records what it BELIEVES the cert says, and the two can
	// drift. The CP stores this to answer "has this agent's certificate expired" from its signing record rather
	// than inferring it from silence.
	NotAfter time.Time
	// PublicKeySPKI is the DER-encoded SubjectPublicKeyInfo of the key this certificate binds (S13.1 D7).
	//
	// WHY THE CP MUST KEEP IT: gateway recovery authenticates a returning agent by PROOF OF POSSESSION of its
	// existing keypair (D1(c)), and a signature can only be verified against a public key the CP holds. It held
	// none — only the serial. nodes.wg_public_key cannot substitute: WireGuard keys are X25519, for
	// Diffie-Hellman, and cannot produce signatures at all. That is arithmetic, not policy.
	PublicKeySPKI []byte
}

// SignCSR signs a PEM CSR as an agent leaf certificate valid for CertTTL.
func (c *CA) SignCSR(csrPEM []byte, commonName string) (Issued, error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil {
		return Issued{}, errors.New("malformed CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return Issued{}, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return Issued{}, fmt.Errorf("CSR signature: %w", err)
	}
	// THE ISSUER ACCEPTS ONLY WHAT THE RECOVERY VERIFIER CAN ACCEPT (review pass 1 #17).
	//
	// rekey.Verify narrowed to RSA deliberately, and wrote down why: "it keeps the verifier from silently
	// accepting a key type whose signature semantics nobody here has reasoned about". The ISSUER that populates
	// the very field that verifier reads was never narrowed to match — so a node enrolling with an ECDSA or Ed25519
	// key got a perfectly good certificate and a `cert_public_key` its own recovery path can never verify.
	//
	// The failure is silent and permanent: proof-of-possession recovery is unavailable for that node forever, and
	// nothing says so until the day it needs it. Two components disagreeing about the accepted key set is the
	// defect; refusing at the door is where it costs nothing.
	if _, ok := csr.PublicKey.(*rsa.PublicKey); !ok {
		return Issued{}, fmt.Errorf("unsupported CSR public key type %T: this CA issues over RSA only, because "+
			"proof-of-possession recovery verifies RSA signatures — issuing over anything else would create an "+
			"identity that can never be recovered", csr.PublicKey)
	}
	sn := bigSerial()
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(CertTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return Issued{}, err
	}
	// Canonicalise the key as SPKI DER — the form x509.ParsePKIXPublicKey reads back, so verification never has
	// to guess an encoding. Taken from the CSR's key, which is the key this certificate binds by construction.
	spki, err := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if err != nil {
		return Issued{}, fmt.Errorf("marshal public key: %w", err)
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return Issued{
		CertPEM:       string(out),
		Serial:        serialString(sn),
		NotAfter:      tmpl.NotAfter,
		PublicKeySPKI: spki,
	}, nil
}

// ServerTLSCertificate mints an ephemeral server certificate (signed by the CA)
// for the agent control channel to present. Agents trust the CA, so they verify
// this server; the channel in turn verifies agents' client certs against the CA.
func (c *CA) ServerTLSCertificate(dnsName string) (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: bigSerial(),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: key}, nil
}

// SelfTest signs and verifies a probe cert so a misconfigured CA fails at boot.
func (c *CA) SelfTest() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "selftest"}}, key)
	if err != nil {
		return err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	iss, err := c.SignCSR(csrPEM, "selftest")
	if err != nil {
		return fmt.Errorf("selftest sign: %w", err)
	}
	blk, _ := pem.Decode([]byte(iss.CertPEM))
	leaf, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return err
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("selftest verify: %w", err)
	}
	return nil
}

func bigSerial() *big.Int {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, _ := rand.Int(rand.Reader, max)
	return n
}

func serialString(sn *big.Int) string { return hex.EncodeToString(sn.Bytes()) }

func ptr[T any](v T) *T { return &v }
