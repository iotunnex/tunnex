package http

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/agentca"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// AgentChannel is the mTLS control channel the tunnex-node agent reconciles
// against. It authorizes every request by the client CERTIFICATE (serial ->
// node), never by anything in the request body (the machine-edition IDOR rule).
type AgentChannel struct {
	svc      *nodes.Service
	ca       *agentca.CA
	logger   *slog.Logger
	watchHold time.Duration
}

// NewAgentChannel builds the channel handler.
func NewAgentChannel(svc *nodes.Service, ca *agentca.CA, logger *slog.Logger) *AgentChannel {
	return &AgentChannel{svc: svc, ca: ca, logger: logger, watchHold: 25 * time.Second}
}

// TLSConfig requires and verifies agent client certs against the CA, and
// presents a CA-signed server cert.
func (a *AgentChannel) TLSConfig(serverDNSName string) (*tls.Config, error) {
	serverCert, err := a.ca.ServerTLSCertificate(serverDNSName)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    a.ca.Pool(),
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// Handler returns the routes served on the mTLS listener.
func (a *AgentChannel) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/agent/desired-state", a.desiredState)
	r.Get("/agent/watch", a.watch)
	r.Post("/agent/renew", a.renew)
	r.Post("/agent/report", a.report)
	return r
}

// report records the agent's locally-generated WireGuard public key.
func (a *AgentChannel) report(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	serial := hex.EncodeToString(r.TLS.PeerCertificates[0].SerialNumber.Bytes())
	node, err := a.svc.AuthenticateCert(r.Context(), serial)
	if err != nil {
		http.Error(w, "unauthorized agent", http.StatusUnauthorized)
		return
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.PublicKey == "" {
		http.Error(w, "public_key required", http.StatusBadRequest)
		return
	}
	if err := a.svc.ReportWGKey(r.Context(), node, body.PublicKey); err != nil {
		var ae *apierr.Error
		if errors.As(err, &ae) {
			http.Error(w, ae.Message, ae.Status)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *AgentChannel) desiredState(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	serial := hex.EncodeToString(r.TLS.PeerCertificates[0].SerialNumber.Bytes())
	node, err := a.svc.AuthenticateCert(r.Context(), serial)
	if err != nil {
		http.Error(w, "unauthorized agent", http.StatusUnauthorized)
		return
	}
	ds, err := a.svc.DesiredState(r.Context(), node)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, ds)
}

// watch is a long-poll: it holds the connection (up to watchHold) then returns,
// prompting the agent to re-fetch. S3.2 will return early on an actual change.
func (a *AgentChannel) watch(w http.ResponseWriter, r *http.Request) {
	serial := ""
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		serial = hex.EncodeToString(r.TLS.PeerCertificates[0].SerialNumber.Bytes())
	}
	if _, err := a.svc.AuthenticateCert(r.Context(), serial); err != nil {
		http.Error(w, "unauthorized agent", http.StatusUnauthorized)
		return
	}
	select {
	case <-r.Context().Done():
	case <-time.After(a.watchHold):
	}
	w.WriteHeader(http.StatusOK)
}

func (a *AgentChannel) renew(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	serial := hex.EncodeToString(r.TLS.PeerCertificates[0].SerialNumber.Bytes())
	node, err := a.svc.AuthenticateCert(r.Context(), serial)
	if err != nil {
		http.Error(w, "unauthorized agent", http.StatusUnauthorized)
		return
	}
	csr, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	cert, err := a.svc.Renew(r.Context(), node, string(csr), r.Header.Get("X-Agent-Version"))
	if err != nil {
		http.Error(w, "renew refused", http.StatusUnauthorized) // revoked node lands here
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write([]byte(cert))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
