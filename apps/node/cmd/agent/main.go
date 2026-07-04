// Command agent is the tunnex-node data-plane agent (S3.1).
//
// On boot it enrolls (join token -> mTLS cert) if it has no cert yet, then runs
// the reconcile loop against the control plane's desired state. The WireGuard
// backend is in-memory in S3.1; the real wgctrl device lands in S3.2.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"log/slog"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/control"
	"github.com/tunnexio/tunnex/apps/node/internal/reconcile"
)

const protocolVersion = 1

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	apiURL := getenv("TUNNEX_API_URL", "http://api:8080")
	agentURL := getenv("TUNNEX_AGENT_URL", "https://api:8443")
	serverName := getenv("TUNNEX_AGENT_SERVERNAME", "tunnex-control")
	joinToken := os.Getenv("TUNNEX_JOIN_TOKEN")
	nodeName := getenv("TUNNEX_NODE_NAME", hostname())
	certDir := getenv("TUNNEX_NODE_STATE_DIR", "/var/lib/tunnex-node")
	healthAddr := getenv("TUNNEX_AGENT_HEALTH_ADDR", ":9091")

	var ready atomic.Bool
	go serveHealth(healthAddr, &ready, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	certPEM, keyPEM, caPEM, err := loadCreds(certDir)
	if err != nil {
		// Not enrolled yet. Enroll if we have a join token; otherwise idle
		// (liveness up, readiness false) until one is provided.
		if joinToken == "" {
			logger.Warn("agent_not_enrolled", slog.String("reason", "no cert and no TUNNEX_JOIN_TOKEN"))
			<-ctx.Done()
			return
		}
		logger.Info("agent_enrolling", slog.String("node_name", nodeName))
		key, csr, gerr := control.GenerateKeyAndCSR(nodeName)
		if gerr != nil {
			logger.Error("agent_csr_failed", slog.String("error", gerr.Error()))
			os.Exit(1)
		}
		res, eerr := control.Enroll(ctx, apiURL, joinToken, csr, nodeName, version(), protocolVersion)
		if eerr != nil {
			logger.Error("agent_enroll_failed", slog.String("error", eerr.Error()))
			os.Exit(1)
		}
		certPEM, keyPEM, caPEM = []byte(res.CertPEM), key, []byte(res.CAPEM)
		if serr := saveCreds(certDir, certPEM, keyPEM, caPEM); serr != nil {
			logger.Error("agent_save_creds_failed", slog.String("error", serr.Error()))
			os.Exit(1)
		}
		logger.Info("agent_enrolled", slog.String("node_id", res.NodeID))
	}

	client, err := control.NewClient(agentURL, serverName, nodeName, certPEM, keyPEM, caPEM)
	if err != nil {
		logger.Error("agent_client_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	backend := reconcile.NewMemBackend()
	r := reconcile.New(backend, logger)

	// Renew the cert at half-life (default 24h; the cert lives 48h) and hot-swap
	// it. Persist the rotated cert so a restart uses the current one. If renewal
	// fails until expiry, mTLS breaks and re-enrollment requires a fresh join
	// token (no silent re-admission).
	renewEvery := getdur("TUNNEX_AGENT_RENEW_INTERVAL", 24*time.Hour)
	go renewLoop(ctx, client, certDir, renewEvery, logger)

	// Readiness flips true once the first reconcile against the control plane
	// succeeds (enrolled + control session + backend healthy).
	go func() {
		if _, err := client.FetchDesired(ctx); err == nil {
			ready.Store(true)
			logger.Info("agent_ready")
		}
	}()

	logger.Info("agent_reconciling", slog.String("node_name", nodeName))
	r.Run(ctx, client, 60*time.Second, 5*time.Second)
	logger.Info("agent_stopped")
}

// serveHealth exposes liveness (process up) and readiness (enrolled + control
// session + backend healthy) — the split S8 multi-gateway views consume.
func serveHealth(addr string, ready *atomic.Bool, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "tunnex-node"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("agent_health_failed", slog.String("error", err.Error()))
	}
}

func loadCreds(dir string) (cert, key, ca []byte, err error) {
	cert, err = os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	key, err = os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	ca, err = os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, nil, nil, err
	}
	return cert, key, ca, nil
}

func saveCreds(dir string, cert, key, ca []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"cert.pem": cert, "key.pem": key, "ca.pem": ca} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func renewLoop(ctx context.Context, client *control.Client, certDir string, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			certPEM, keyPEM, err := client.Renew(ctx, version())
			if err != nil {
				logger.Warn("agent_renew_failed", slog.String("error", err.Error()))
				continue
			}
			ca, _ := os.ReadFile(filepath.Join(certDir, "ca.pem"))
			if err := saveCreds(certDir, certPEM, keyPEM, ca); err != nil {
				logger.Warn("agent_renew_persist_failed", slog.String("error", err.Error()))
				continue
			}
			logger.Info("agent_cert_renewed")
		}
	}
}

func getdur(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func hostname() string { h, _ := os.Hostname(); return h }
func version() string  { return getenv("TUNNEX_AGENT_VERSION", "0.1.0") }
