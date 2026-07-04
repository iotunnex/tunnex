// Package control is the tunnex-node agent's client for the control plane:
// enrollment (plain HTTP, join-token) and the mTLS reconcile channel.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/reconcile"
)

// GenerateKeyAndCSR creates an agent private key and a CSR for commonName.
func GenerateKeyAndCSR(commonName string) (keyPEM, csrPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	return keyPEM, csrPEM, nil
}

// EnrollResult holds the credentials returned by enrollment.
type EnrollResult struct {
	NodeID  string
	CertPEM string
	CAPEM   string
}

// Enroll exchanges a join token + CSR for a signed certificate over plain HTTP.
func Enroll(ctx context.Context, apiURL, joinToken string, csrPEM []byte, nodeName, agentVersion string, protocolVersion int) (EnrollResult, error) {
	body, _ := json.Marshal(map[string]any{
		"join_token": joinToken, "csr": string(csrPEM), "node_name": nodeName,
		"agent_version": agentVersion, "protocol_version": protocolVersion,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/api/v1/agent/enroll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return EnrollResult{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return EnrollResult{}, fmt.Errorf("enroll failed (%d): %s", resp.StatusCode, string(data))
	}
	var r struct {
		NodeID      string `json:"node_id"`
		Certificate string `json:"certificate"`
		CACert      string `json:"ca_certificate"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return EnrollResult{}, err
	}
	return EnrollResult{NodeID: r.NodeID, CertPEM: r.Certificate, CAPEM: r.CACert}, nil
}

// Client is the mTLS reconcile-channel client (implements reconcile.ControlClient).
type Client struct {
	base string
	http *http.Client
}

// NewClient builds an mTLS client presenting certPEM/keyPEM and trusting caPEM.
// serverName is the control-channel server cert's name (dialing host may differ,
// e.g. the compose service name).
func NewClient(agentURL, serverName string, certPEM, keyPEM, caPEM []byte) (*Client, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("bad CA PEM")
	}
	return &Client{
		base: agentURL,
		http: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      pool,
				ServerName:   serverName,
				MinVersion:   tls.VersionTLS12,
			}},
		},
	}, nil
}

// FetchDesired GETs the desired state over mTLS.
func (c *Client) FetchDesired(ctx context.Context) (reconcile.DesiredState, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/agent/desired-state", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return reconcile.DesiredState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return reconcile.DesiredState{}, fmt.Errorf("desired-state status %d", resp.StatusCode)
	}
	var ds reconcile.DesiredState
	if err := json.NewDecoder(resp.Body).Decode(&ds); err != nil {
		return reconcile.DesiredState{}, err
	}
	return ds, nil
}

// Watch long-polls the control plane; it returns when the server responds
// (change or timeout), prompting a re-fetch.
func (c *Client) Watch(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/agent/watch", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("watch status %d", resp.StatusCode)
	}
	return nil
}
