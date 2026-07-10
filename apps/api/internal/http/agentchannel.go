package http

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tunnexio/tunnex/apps/api/internal/agentca"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
	"github.com/tunnexio/tunnex/apps/api/internal/nodes"
)

// AgentChannel is the mTLS control channel the tunnex-node agent reconciles
// against. It authorizes every request by the client CERTIFICATE (serial ->
// node), never by anything in the request body (the machine-edition IDOR rule).
type AgentChannel struct {
	svc       *nodes.Service
	ca        *agentca.CA
	hub       *nodepush.Hub
	logger    *slog.Logger
	watchHold time.Duration
}

// NewAgentChannel builds the channel handler. hub may be nil (watch then falls
// back to the timed long-poll only; the interval reconcile still converges).
func NewAgentChannel(svc *nodes.Service, ca *agentca.CA, hub *nodepush.Hub, logger *slog.Logger) *AgentChannel {
	return &AgentChannel{svc: svc, ca: ca, hub: hub, logger: logger, watchHold: 25 * time.Second}
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
	r.Post("/agent/status", a.status)
	return r
}

// status ingests per-peer live telemetry (handshake/bytes/endpoint) from the
// agent and upserts it against the node's devices.
func (a *AgentChannel) status(w http.ResponseWriter, r *http.Request) {
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
		Peers []struct {
			PublicKey     string `json:"public_key"`
			LastHandshake int64  `json:"last_handshake"`
			RxBytes       int64  `json:"rx_bytes"`
			TxBytes       int64  `json:"tx_bytes"`
			Endpoint      string `json:"endpoint"`
		} `json:"peers"`
	}
	// Bound the body (a report is capped at ~1000 peers agent-side).
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	stats := make([]nodes.PeerStatus, 0, len(body.Peers))
	for _, p := range body.Peers {
		stats = append(stats, nodes.PeerStatus{
			PublicKey: p.PublicKey, LastHandshake: p.LastHandshake,
			RxBytes: p.RxBytes, TxBytes: p.TxBytes,
		})
	}
	if err := a.svc.ReportStatus(r.Context(), node, stats); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		Endpoint  string `json:"endpoint"`
		EgressNAT bool   `json:"egress_nat"` // S3.7: gateway can source-NAT full-tunnel egress
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.PublicKey == "" {
		http.Error(w, "public_key required", http.StatusBadRequest)
		return
	}
	if err := a.svc.ReportWGInfo(r.Context(), node, body.PublicKey, body.Endpoint, body.EgressNAT); err != nil {
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
	// Read the change-version BEFORE the peer query so the reported version can
	// never be newer than the data it accompanies (a change landing after this
	// read leaves the agent with a stale version -> its next watch resyncs).
	var version uint64
	if a.hub != nil {
		version = a.hub.Version(node.ID)
	}
	ds, err := a.svc.DesiredState(r.Context(), node)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ds.Version = version
	writeJSON(w, ds)
}

// watch is a long-poll: it returns the instant this node's desired state changes
// (pushed via the hub) so revocations apply within the S3.1 <5s bound, or after
// watchHold as a safety net. The agent re-fetches on return.
func (a *AgentChannel) watch(w http.ResponseWriter, r *http.Request) {
	serial := ""
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		serial = hex.EncodeToString(r.TLS.PeerCertificates[0].SerialNumber.Bytes())
	}
	node, err := a.svc.AuthenticateCert(r.Context(), serial)
	if err != nil {
		http.Error(w, "unauthorized agent", http.StatusUnauthorized)
		return
	}
	var changed <-chan struct{}
	if a.hub != nil {
		// Subscribe BEFORE reading the version so no Notify is missed in between.
		ch, unsubscribe := a.hub.Subscribe(node.ID)
		defer unsubscribe()
		changed = ch
		// If the node changed since the version the agent last fetched, return
		// immediately — the change happened during the agent's fetch/apply gap.
		since, _ := strconv.ParseUint(r.URL.Query().Get("v"), 10, 64)
		if a.hub.Version(node.ID) != since {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	select {
	case <-r.Context().Done():
	case <-changed: // pushed change for this node -> return now
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
