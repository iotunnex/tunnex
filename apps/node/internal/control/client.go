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
	"strconv"
	"sync/atomic"
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
// The client certificate is served via GetClientCertificate reading an atomic
// holder, so Renew can hot-swap it mid-flight without rebuilding the client.
type Client struct {
	base     string
	nodeName string
	cert     atomic.Pointer[tls.Certificate]
	http     *http.Client
}

// NewClient builds an mTLS client presenting certPEM/keyPEM and trusting caPEM.
// serverName is the control-channel server cert's name (dialing host may differ,
// e.g. the compose service name).
func NewClient(agentURL, serverName, nodeName string, certPEM, keyPEM, caPEM []byte) (*Client, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("bad CA PEM")
	}
	c := &Client{base: agentURL, nodeName: nodeName}
	c.cert.Store(&cert)
	c.http = &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return c.cert.Load(), nil
			},
			RootCAs:    pool,
			ServerName: serverName,
			MinVersion: tls.VersionTLS12,
		}},
	}
	return c, nil
}

// Renew rotates the agent's certificate over the mTLS channel: it generates a
// FRESH key + CSR, posts it (authenticated by the CURRENT cert), hot-swaps the
// new cert in, and returns the new cert+key PEM for the caller to persist.
// Renewing at half-life keeps the agent from ever reaching cert expiry.
func (c *Client) Renew(ctx context.Context, agentVersion string) (newCertPEM, newKeyPEM []byte, err error) {
	keyPEM, csrPEM, err := GenerateKeyAndCSR(c.nodeName)
	if err != nil {
		return nil, nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/agent/renew", bytes.NewReader(csrPEM))
	req.Header.Set("X-Agent-Version", agentVersion)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("renew status %d: %s", resp.StatusCode, string(body))
	}
	newCert, err := tls.X509KeyPair(body, keyPEM)
	if err != nil {
		return nil, nil, err
	}
	c.cert.Store(&newCert) // hot-swap: subsequent requests use the fresh cert
	// Drop pooled TLS connections so the next request re-handshakes with the new
	// cert (an existing keep-alive connection would keep presenting the old one).
	c.http.CloseIdleConnections()
	return body, keyPEM, nil
}

// ReportInfo reports the node's locally-generated WireGuard public key and its
// public endpoint (host:port that peer configs dial).
func (c *Client) ReportInfo(ctx context.Context, publicKey, endpoint string) error {
	body, _ := json.Marshal(map[string]string{"public_key": publicKey, "endpoint": endpoint})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/agent/report", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("report status %d", resp.StatusCode)
	}
	return nil
}

// ReportStatus posts per-peer live telemetry (handshake/bytes/endpoint) over the
// mTLS channel. Fire-and-forget from the caller's view: a failed report just
// means a momentarily stale status view, not a data-plane problem.
func (c *Client) ReportStatus(ctx context.Context, stats []reconcile.PeerStat) error {
	body, _ := json.Marshal(map[string]any{"peers": stats})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/agent/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status report status %d", resp.StatusCode)
	}
	return nil
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
// (change or timeout), prompting a re-fetch. since is the version from the last
// fetch — the server returns immediately if its version has advanced past it.
func (c *Client) Watch(ctx context.Context, since uint64) error {
	url := c.base + "/agent/watch?v=" + strconv.FormatUint(since, 10)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
