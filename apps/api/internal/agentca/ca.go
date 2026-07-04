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

// SignCSR signs a PEM CSR as an agent leaf certificate valid for CertTTL. The
// returned serial is stored on the node record and IS the agent's identity.
func (c *CA) SignCSR(csrPEM []byte, commonName string) (certPEM string, serial string, err error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil {
		return "", "", errors.New("malformed CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return "", "", fmt.Errorf("CSR signature: %w", err)
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
		return "", "", err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(out), serialString(sn), nil
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
	certPEM, _, err := c.SignCSR(csrPEM, "selftest")
	if err != nil {
		return fmt.Errorf("selftest sign: %w", err)
	}
	blk, _ := pem.Decode([]byte(certPEM))
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
